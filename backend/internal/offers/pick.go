package offers

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"commute-backend/internal/scheduling"
	"commute-backend/internal/travel"
)

// CloseCounts is what one close_offers step did.
type CloseCounts struct {
	Closed   int `json:"closed"`
	Assigned int `json:"assigned"`
	Errors   int `json:"errors"`
}

// CloseDueOffers is the sweep's close_offers step: every open offer past
// expires_at (and every closing offer left by a crashed sweep for longer
// than ClosingRetryAfter) is moved to closing in its own transaction, and
// the pick runs on it. Only one sweep can claim an offer.
func (s *Service) CloseDueOffers(ctx context.Context) (CloseCounts, error) {
	var c CloseCounts
	now := s.Clock.Now()
	stale := now.Add(-s.cfg().ClosingRetryAfter)
	rows, err := s.DB.Query(ctx, `select id from offers
		where (status = 'open' and expires_at <= $1) or (status = 'closing' and closing_at <= $2)
		order by expires_at, id`, now, stale)
	if err != nil {
		return c, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return c, err
	}
	for _, id := range ids {
		tag, err := s.DB.Exec(ctx, `update offers set status = 'closing', closing_at = $2
			where id = $1 and ((status = 'open' and expires_at <= $2) or (status = 'closing' and closing_at <= $3))`, id, now, stale)
		if err != nil {
			c.Errors++
			s.log().Error("close_offers: claim", "offer", id, "err", err)
			continue
		}
		if tag.RowsAffected() == 0 {
			continue // another sweep has it
		}
		c.Closed++
		assigned, err := s.pick(ctx, id)
		if err != nil {
			c.Errors++
			s.log().Error("close_offers: pick", "offer", id, "err", err)
			continue
		}
		if assigned {
			c.Assigned++
		}
	}
	return c, nil
}

type offerRow struct {
	ID       uuid.UUID
	Kind     string
	SeriesID *uuid.UUID
	Attempt  int
	Round    int
	Status   string
}

func lockOffer(ctx context.Context, q querier, id uuid.UUID) (offerRow, error) {
	var o offerRow
	err := q.QueryRow(ctx, "select id, kind, series_id, attempt, round, status from offers where id = $1 for update", id).
		Scan(&o.ID, &o.Kind, &o.SeriesID, &o.Attempt, &o.Round, &o.Status)
	return o, err
}

type attempt int

const (
	tryNext attempt = iota
	assigned
	stop
)

// pick runs the pick on a closing offer: the interested drivers in shuffled
// order, each tried in its own transaction, first fit wins; with no fit the
// next round opens (or, after the last, no_driver_found).
func (s *Service) pick(ctx context.Context, offerID uuid.UUID) (bool, error) {
	var o offerRow
	err := s.DB.QueryRow(ctx, "select id, kind, series_id, attempt, round, status from offers where id = $1", offerID).
		Scan(&o.ID, &o.Kind, &o.SeriesID, &o.Attempt, &o.Round, &o.Status)
	if err != nil {
		return false, err
	}
	if o.Kind != "series" || o.SeriesID == nil {
		// Slot and one-ride offers are raised by later flows (R3, R4).
		_, err := s.DB.Exec(ctx, "update offers set status = 'closed', outcome = 'no_fit', closed_at = $2 where id = $1 and status = 'closing'",
			o.ID, s.Clock.Now())
		return false, err
	}
	rows, err := s.DB.Query(ctx, `select r.driver_id, d.name from offer_recipients r join drivers d on d.user_id = r.driver_id
		where r.offer_id = $1 and r.interested_at is not null and r.withdrawn_at is null and r.result is null
		order by d.name, r.driver_id`, o.ID)
	if err != nil {
		return false, err
	}
	cands, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Candidate, error) {
		var c Candidate
		err := r.Scan(&c.DriverID, &c.Name)
		return c, err
	})
	if err != nil {
		return false, err
	}
	s.shuffler().Shuffle(cands)
	for _, c := range cands {
		res, err := s.tryDriver(ctx, o, c)
		if err != nil {
			return false, err
		}
		switch res {
		case assigned:
			return true, nil
		case stop:
			return false, nil
		}
	}
	return false, s.finishNoFit(ctx, o)
}

var errRideOverlap = errors.New("the overlap constraint refused a ride")

// tryDriver is one attempt of the pick, in its own transaction: lock the
// driver, then the series, then the offer; re-check; check eligibility and
// clashes; assign.
func (s *Service) tryDriver(ctx context.Context, o offerRow, c Candidate) (attempt, error) {
	now := s.Clock.Now()
	var res attempt
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		d, err := lockDriver(ctx, tx, c.DriverID)
		if err != nil {
			return err
		}
		se, err := lockSeries(ctx, tx, *o.SeriesID)
		if err != nil {
			return err
		}
		off, err := lockOffer(ctx, tx, o.ID)
		if err != nil {
			return err
		}
		if se.Status != "finding_driver" || off.Status != "closing" {
			res = stop
			return s.closeStale(ctx, tx, off, se, now)
		}
		if !d.eligibleFor(se.VehicleType, se.City, s.today(now)) {
			res = tryNext
			return s.recordResult(ctx, tx, o.ID, c.DriverID, "ineligible", now)
		}
		slots, err := readSlots(ctx, tx, se.ID)
		if err != nil {
			return err
		}
		clash, why, err := s.findClash(ctx, tx, d, se, slots, now)
		if err != nil {
			return err
		}
		if clash {
			res = tryNext
			s.log().Info("pick: clash", "offer", o.ID, "driver", d.Name, "why", why)
			return s.recordResult(ctx, tx, o.ID, c.DriverID, "clashed", now)
		}
		if err := s.assign(ctx, tx, off, se, slots, d, now); err != nil {
			return err
		}
		res = assigned
		return nil
	})
	if errors.Is(err, errRideOverlap) {
		// The constraint caught what the clash check missed: a clash.
		s.log().Warn("pick: overlap constraint refused a ride", "offer", o.ID, "driver", c.DriverID)
		return tryNext, pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
			return s.recordResult(ctx, tx, o.ID, c.DriverID, "clashed", now)
		})
	}
	return res, err
}

// closeStale closes an offer whose series no longer needs a driver
// (cancelled, or assigned by another sweep), if it is still closing.
func (s *Service) closeStale(ctx context.Context, tx pgx.Tx, off offerRow, se seriesRow, now time.Time) error {
	if off.Status != "closing" {
		return nil
	}
	outcome := "no_fit"
	switch se.Status {
	case "ended":
		outcome = "cancelled"
	case "active", "paused":
		outcome = "assigned"
	}
	if _, err := tx.Exec(ctx, "update offers set status = 'closed', outcome = $2, closed_at = $3 where id = $1", off.ID, outcome, now); err != nil {
		return err
	}
	return event(ctx, tx, now, nil, "offer", off.ID, "offer.closed", map[string]any{"outcome": outcome, "series_status": se.Status})
}

func (s *Service) recordResult(ctx context.Context, tx pgx.Tx, offerID, driverID uuid.UUID, result string, now time.Time) error {
	if _, err := tx.Exec(ctx, "update offer_recipients set result = $3 where offer_id = $1 and driver_id = $2", offerID, driverID, result); err != nil {
		return err
	}
	return event(ctx, tx, now, nil, "offer", offerID, "offer.recipient_"+result, map[string]any{"driver_id": driverID})
}

// assign gives every slot to the driver, activates the series, creates the
// first 14 days of rides and tells everyone.
func (s *Service) assign(ctx context.Context, tx pgx.Tx, off offerRow, se seriesRow, slots []slotRow, d driverRow, now time.Time) error {
	if _, err := tx.Exec(ctx, "update series_slots set driver_id = $2 where series_id = $1 and active", se.ID, d.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "update series set status = 'active', version = version + 1, updated_at = $2 where id = $1", se.ID, now); err != nil {
		return err
	}
	if err := event(ctx, tx, now, nil, "series", se.ID, "series.assigned",
		map[string]any{"old_status": se.Status, "new_status": "active", "driver_id": d.ID, "offer_id": off.ID}); err != nil {
		return err
	}
	if _, err := s.generateRides(ctx, tx, se.ID, now, true); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "update offer_recipients set result = 'picked' where offer_id = $1 and driver_id = $2", off.ID, d.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update offer_recipients set result = 'not_picked'
		where offer_id = $1 and driver_id <> $2 and interested_at is not null and result is null`, off.ID, d.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "update offers set status = 'closed', outcome = 'assigned', closed_at = $2 where id = $1", off.ID, now); err != nil {
		return err
	}
	if err := event(ctx, tx, now, nil, "offer", off.ID, "offer.closed", map[string]any{"outcome": "assigned", "driver_id": d.ID}); err != nil {
		return err
	}

	var riderName string
	if err := tx.QueryRow(ctx, "select name from riders where user_id = $1", se.RiderID).Scan(&riderName); err != nil && !noRows(err) {
		return err
	}
	rider := label(FirstName(riderName), "your rider")
	days := outboundDays(slots)
	data := map[string]string{"series_id": se.ID.String(), "offer_id": off.ID.String()}
	start := se.StartDate.Format("Mon 2 Jan")
	if err := s.push(ctx, tx, now, se.RiderID, "series_active", "Driver confirmed",
		fmt.Sprintf("%s will drive you %s from %s", FirstName(d.Name), dayList(days), start), data); err != nil {
		return err
	}
	if err := s.push(ctx, tx, now, d.ID, "offer_won", "You got it",
		fmt.Sprintf("You're %s's regular driver: %s from %s.", rider, timesLine(slots), se.StartDate.Format("2 Jan")), data); err != nil {
		return err
	}
	return s.tellLosers(ctx, tx, off.ID, d.ID, now, data)
}

// tellLosers tells interested drivers who didn't get it (not picked,
// clashed or no longer eligible). No reason is given.
func (s *Service) tellLosers(ctx context.Context, tx pgx.Tx, offerID, winner uuid.UUID, now time.Time, data map[string]string) error {
	rows, err := tx.Query(ctx, `select driver_id from offer_recipients
		where offer_id = $1 and driver_id <> $2 and result in ('not_picked', 'clashed', 'ineligible')`, offerID, winner)
	if err != nil {
		return err
	}
	losers, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, l := range losers {
		if err := s.push(ctx, tx, now, l, "offer_lost", "Request closed", "This one went to another driver.", data); err != nil {
			return err
		}
	}
	return nil
}

// finishNoFit closes a round nobody fitted and opens the next, or after
// the last round sets no_driver_found.
func (s *Service) finishNoFit(ctx context.Context, o offerRow) error {
	now := s.Clock.Now()
	return pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		se, err := lockSeries(ctx, tx, *o.SeriesID)
		if err != nil {
			return err
		}
		off, err := lockOffer(ctx, tx, o.ID)
		if err != nil {
			return err
		}
		if off.Status != "closing" {
			return nil
		}
		if se.Status != "finding_driver" {
			return s.closeStale(ctx, tx, off, se, now)
		}
		if _, err := tx.Exec(ctx, "update offers set status = 'closed', outcome = 'no_fit', closed_at = $2 where id = $1", off.ID, now); err != nil {
			return err
		}
		if err := event(ctx, tx, now, nil, "offer", off.ID, "offer.closed", map[string]any{"outcome": "no_fit", "round": off.Round}); err != nil {
			return err
		}
		if err := s.tellLosers(ctx, tx, off.ID, uuid.Nil, now, map[string]string{"series_id": se.ID.String(), "offer_id": off.ID.String()}); err != nil {
			return err
		}
		if off.Round < s.cfg().MaxRounds {
			return s.openRound(ctx, tx, se, off.Round+1, now)
		}
		return s.noDriverFound(ctx, tx, se, now)
	})
}

// timesLine is "Mon, Wed, Fri at 08:30" (each distinct time listed).
func timesLine(slots []slotRow) string {
	line := scheduleLine(slots)
	var out []rune
	for _, r := range line {
		if r == '/' {
			out = append(out, ',', ' ')
			continue
		}
		out = append(out, r)
	}
	return replaceLastSpaceWithAt(string(out))
}

func replaceLastSpaceWithAt(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ' ' {
			return s[:i] + " at " + s[i+1:]
		}
	}
	return s
}

// ---------------------------------------------------------------- drivers

type driverRow struct {
	ID                uuid.UUID
	Name              string
	City              string
	VehicleType       string
	Status            string
	SubscriptionUntil *time.Time
	DayOff            *int
}

const driverCols = "user_id, name, city, vehicle_type, status, subscription_until, day_off"

func scanDriver(row pgx.Row) (driverRow, error) {
	var d driverRow
	err := row.Scan(&d.ID, &d.Name, &d.City, &d.VehicleType, &d.Status, &d.SubscriptionUntil, &d.DayOff)
	return d, err
}

func lockDriver(ctx context.Context, q querier, id uuid.UUID) (driverRow, error) {
	return scanDriver(q.QueryRow(ctx, "select "+driverCols+" from drivers where user_id = $1 for update", id))
}

func readDriver(ctx context.Context, q querier, id uuid.UUID) (driverRow, error) {
	return scanDriver(q.QueryRow(ctx, "select "+driverCols+" from drivers where user_id = $1", id))
}

// live is a driver who may receive offers: live, with an active subscription.
func (d driverRow) live(today time.Time) bool {
	return d.Status == "live" && d.SubscriptionUntil != nil && !d.SubscriptionUntil.Before(today)
}

func (d driverRow) eligibleFor(vehicleType, city string, today time.Time) bool {
	return d.live(today) && d.VehicleType == vehicleType && d.City == city
}

// ---------------------------------------------------------------- clash check

// findClash checks every slot of the series, for the dates rides would be
// created on, against the driver's day off, their other slots and their
// rides: each previous drop-off → next pickup must leave the travel time
// plus 10 minutes. Leave doesn't block (no leave exists yet).
func (s *Service) findClash(ctx context.Context, tx pgx.Tx, d driverRow, se seriesRow, slots []slotRow, now time.Time) (bool, string, error) {
	loc := s.loc(se.TimeZone)
	from, to := rideWindow(se.StartDate, scheduling.DateOf(now, loc))
	var candidate []scheduling.Trip
	for _, sl := range slots {
		if !sl.Active {
			continue
		}
		if d.DayOff != nil && *d.DayOff == sl.Weekday {
			return true, "day off " + scheduling.WeekdayName(sl.Weekday), nil
		}
		a, b := se.ends(sl.Direction)
		for _, date := range scheduling.RideDates(sl.Weekday, from, to) {
			p := scheduling.LocalInstant(date, sl.Pickup, loc)
			candidate = append(candidate, scheduling.Trip{Pickup: p, Drop: p.Add(time.Duration(se.TravelMinutes) * time.Minute),
				From: a, To: b, Label: fmt.Sprintf("new %s %s", scheduling.FormatDate(date), sl.Direction)})
		}
	}
	if len(candidate) == 0 {
		return false, "", nil
	}
	lo, hi := candidate[0].Pickup, candidate[0].Pickup
	for _, c := range candidate {
		if c.Pickup.Before(lo) {
			lo = c.Pickup
		}
		if c.Pickup.After(hi) {
			hi = c.Pickup
		}
	}
	existing, err := s.driverTrips(ctx, tx, d.ID, se.ID, lo.Add(-24*time.Hour), hi.Add(24*time.Hour))
	if err != nil {
		return false, "", err
	}
	speed, err := s.speed(ctx, tx, d.VehicleType)
	if err != nil {
		return false, "", err
	}
	tf := func(a, b travel.Point) int { return travel.MinutesBetween(a, b, speed) }
	if a, b, ok := scheduling.FirstClash(existing, candidate, tf); ok {
		return true, a.Label + " vs " + b.Label, nil
	}
	return false, "", nil
}

func (s *Service) speed(ctx context.Context, q querier, vehicleType string) (float64, error) {
	var v float64
	err := q.QueryRow(ctx, "select speed_kmh::float8 from ride_type_config where ride_type = $1", vehicleType).Scan(&v)
	if noRows(err) {
		return 18, nil
	}
	return v, err
}

// driverTrips is everything the driver drives between [lo] and [hi]: their
// assigned rides, and their other active slots on dates that have no ride
// yet (beyond the rides already created).
func (s *Service) driverTrips(ctx context.Context, tx pgx.Tx, driverID, exceptSeries uuid.UUID, lo, hi time.Time) ([]scheduling.Trip, error) {
	var trips []scheduling.Trip
	rows, err := tx.Query(ctx, `select r.id, r.pickup_at, r.drop_at, coalesce(sl.direction, 'outbound'),
			s.pickup_lat, s.pickup_lng, s.drop_lat, s.drop_lng
		from rides r left join series_slots sl on sl.id = r.slot_id left join series s on s.id = r.series_id
		where r.driver_id = $1 and r.status in ('assigned', 'on_the_way') and r.pickup_at >= $2 and r.pickup_at <= $3`,
		driverID, lo, hi)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id uuid.UUID
		var t scheduling.Trip
		var dir string
		var pLat, pLng, dLat, dLng *float64
		if err := rows.Scan(&id, &t.Pickup, &t.Drop, &dir, &pLat, &pLng, &dLat, &dLng); err != nil {
			rows.Close()
			return nil, err
		}
		if pLat != nil {
			p, d := travel.Point{Lat: *pLat, Lng: *pLng}, travel.Point{Lat: *dLat, Lng: *dLng}
			if dir == "return" {
				p, d = d, p
			}
			t.From, t.To = p, d
		}
		t.Label = "ride " + id.String()
		trips = append(trips, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = tx.Query(ctx, `select sl.id, sl.weekday, sl.direction, (extract(epoch from sl.local_pickup_time) / 60)::int,
			s.travel_minutes, s.time_zone, s.pickup_lat, s.pickup_lng, s.drop_lat, s.drop_lng
		from series_slots sl join series s on s.id = sl.series_id
		where sl.driver_id = $1 and sl.active and s.status in ('active', 'paused') and s.id <> $2`, driverID, exceptSeries)
	if err != nil {
		return nil, err
	}
	type slotPattern struct {
		id                     uuid.UUID
		weekday, pickup        int
		travel                 int
		dir, tz                string
		pLat, pLng, dLat, dLng float64
	}
	var pats []slotPattern
	for rows.Next() {
		var p slotPattern
		if err := rows.Scan(&p.id, &p.weekday, &p.dir, &p.pickup, &p.travel, &p.tz, &p.pLat, &p.pLng, &p.dLat, &p.dLng); err != nil {
			rows.Close()
			return nil, err
		}
		pats = append(pats, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(pats) == 0 {
		return trips, nil
	}
	// Dates that already have a ride (whatever its status) are covered above.
	ids := make([]uuid.UUID, len(pats))
	for i, p := range pats {
		ids[i] = p.id
	}
	rows, err = tx.Query(ctx, "select slot_id, local_date from rides where slot_id = any($1)", ids)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for rows.Next() {
		var id uuid.UUID
		var date time.Time
		if err := rows.Scan(&id, &date); err != nil {
			rows.Close()
			return nil, err
		}
		have[id.String()+scheduling.FormatDate(date)] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, p := range pats {
		loc := s.loc(p.tz)
		a, b := travel.Point{Lat: p.pLat, Lng: p.pLng}, travel.Point{Lat: p.dLat, Lng: p.dLng}
		if p.dir == "return" {
			a, b = b, a
		}
		for _, date := range scheduling.RideDates(p.weekday, scheduling.DateOf(lo, loc), scheduling.DateOf(hi, loc)) {
			if have[p.id.String()+scheduling.FormatDate(date)] {
				continue
			}
			pk := scheduling.LocalInstant(date, p.pickup, loc)
			trips = append(trips, scheduling.Trip{Pickup: pk, Drop: pk.Add(time.Duration(p.travel) * time.Minute), From: a, To: b,
				Label: fmt.Sprintf("slot %s %s %s", p.id, scheduling.FormatDate(date), p.dir)})
		}
	}
	slices.SortFunc(trips, func(a, b scheduling.Trip) int { return a.Pickup.Compare(b.Pickup) })
	return trips, nil
}
