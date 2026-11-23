package offers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"commute-backend/internal/scheduling"
	"commute-backend/internal/travel"
)

// ---------------------------------------------------------------- rows

type seriesRow struct {
	ID, RiderID   uuid.UUID
	Status        string
	City          string
	VehicleType   string
	Preferences   []string
	Pickup, Drop  Place
	TimeZone      string
	StartDate     time.Time
	DistanceKm    float64
	TravelMinutes int
	SuggestedFare int
	UsualPayment  string
	RequestedAt   time.Time
	Attempt       int
	Version       int
}

const seriesCols = `id, rider_id, status, city, vehicle_type, preferences, pickup_lat, pickup_lng, pickup_label,
	drop_lat, drop_lng, drop_label, time_zone, start_date, distance_km::float8, travel_minutes, suggested_fare,
	usual_payment, requested_at, attempt, version`

func scanSeries(row pgx.Row) (seriesRow, error) {
	var s seriesRow
	err := row.Scan(&s.ID, &s.RiderID, &s.Status, &s.City, &s.VehicleType, &s.Preferences,
		&s.Pickup.Lat, &s.Pickup.Lng, &s.Pickup.Label, &s.Drop.Lat, &s.Drop.Lng, &s.Drop.Label,
		&s.TimeZone, &s.StartDate, &s.DistanceKm, &s.TravelMinutes, &s.SuggestedFare,
		&s.UsualPayment, &s.RequestedAt, &s.Attempt, &s.Version)
	return s, err
}

// lockSeries reads a series FOR UPDATE.
func lockSeries(ctx context.Context, q querier, id uuid.UUID) (seriesRow, error) {
	return scanSeries(q.QueryRow(ctx, "select "+seriesCols+" from series where id = $1 for update", id))
}

func readSeries(ctx context.Context, q querier, id uuid.UUID) (seriesRow, error) {
	return scanSeries(q.QueryRow(ctx, "select "+seriesCols+" from series where id = $1", id))
}

type slotRow struct {
	ID        uuid.UUID
	SeriesID  uuid.UUID
	Weekday   int
	Direction string
	Timing    string
	ArriveBy  *int
	Pickup    int
	DriverID  *uuid.UUID
	Active    bool
}

const slotCols = `id, series_id, weekday, direction, timing,
	(extract(epoch from arrive_by_time) / 60)::int, (extract(epoch from local_pickup_time) / 60)::int, driver_id, active`

func scanSlot(row pgx.CollectableRow) (slotRow, error) {
	var s slotRow
	err := row.Scan(&s.ID, &s.SeriesID, &s.Weekday, &s.Direction, &s.Timing, &s.ArriveBy, &s.Pickup, &s.DriverID, &s.Active)
	return s, err
}

func readSlots(ctx context.Context, q querier, seriesID uuid.UUID) ([]slotRow, error) {
	rows, err := q.Query(ctx, "select "+slotCols+" from series_slots where series_id = $1 order by weekday, direction", seriesID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanSlot)
}

// from and to of a slot: a return slot swaps pickup and drop-off.
func (s seriesRow) ends(direction string) (from, to travel.Point) {
	if direction == "return" {
		return s.Drop.point(), s.Pickup.point()
	}
	return s.Pickup.point(), s.Drop.point()
}

// ---------------------------------------------------------------- views

// DriverView is a driver as a rider sees them.
type DriverView struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	VehicleType   string    `json:"vehicle_type"`
	VehicleNumber string    `json:"vehicle_number"`
	VehicleDesc   string    `json:"vehicle_desc"`
	Rating        float64   `json:"rating"`
}

// SlotView is one weekday and direction of a series.
type SlotView struct {
	ID                uuid.UUID   `json:"id"`
	Weekday           int         `json:"weekday"`
	Day               string      `json:"day"`
	Direction         string      `json:"direction"`
	Timing            string      `json:"timing"`
	ArriveByTime      *string     `json:"arrive_by_time"`
	LocalPickupTime   string      `json:"local_pickup_time"`
	EstimatedDropTime string      `json:"estimated_drop_time"`
	DriverID          *uuid.UUID  `json:"driver_id"`
	Driver            *DriverView `json:"driver"`
	Active            bool        `json:"active"`
}

// OfferSummary is a series' latest offer as the rider's app sees it.
type OfferSummary struct {
	ID        uuid.UUID `json:"id"`
	Kind      string    `json:"kind"`
	Round     int       `json:"round"`
	Status    string    `json:"status"`
	OpenedAt  time.Time `json:"opened_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Outcome   *string   `json:"outcome"`
}

// SeriesView is a series with its slots and latest offer.
type SeriesView struct {
	ID            uuid.UUID     `json:"id"`
	RiderID       uuid.UUID     `json:"rider_id"`
	Status        string        `json:"status"`
	City          string        `json:"city"`
	VehicleType   string        `json:"vehicle_type"`
	RideType      string        `json:"ride_type"`
	Preferences   []string      `json:"preferences"`
	Pickup        Place         `json:"pickup"`
	Drop          Place         `json:"drop"`
	TimeZone      string        `json:"time_zone"`
	StartDate     string        `json:"start_date"`
	DistanceKm    float64       `json:"distance_km"`
	TravelMinutes int           `json:"travel_minutes"`
	SuggestedFare int           `json:"suggested_fare"`
	UsualPayment  string        `json:"usual_payment"`
	RequestedAt   time.Time     `json:"requested_at"`
	Version       int           `json:"version"`
	Slots         []SlotView    `json:"slots"`
	Offer         *OfferSummary `json:"offer"`
	// The open offer's round and closing time (null when none is open).
	Round     *int       `json:"round"`
	ExpiresAt *time.Time `json:"expires_at"`
}

func (s *Service) seriesView(ctx context.Context, q querier, id uuid.UUID) (SeriesView, error) {
	se, err := readSeries(ctx, q, id)
	if err != nil {
		return SeriesView{}, err
	}
	v := SeriesView{
		ID: se.ID, RiderID: se.RiderID, Status: se.Status, City: se.City, VehicleType: se.VehicleType, RideType: se.VehicleType,
		Preferences: se.Preferences, Pickup: se.Pickup, Drop: se.Drop, TimeZone: se.TimeZone,
		StartDate: scheduling.FormatDate(se.StartDate), DistanceKm: se.DistanceKm, TravelMinutes: se.TravelMinutes,
		SuggestedFare: se.SuggestedFare, UsualPayment: se.UsualPayment, RequestedAt: se.RequestedAt, Version: se.Version,
		Slots: []SlotView{},
	}
	if v.Preferences == nil {
		v.Preferences = []string{}
	}
	slots, err := readSlots(ctx, q, id)
	if err != nil {
		return v, err
	}
	drivers := map[uuid.UUID]*DriverView{}
	for _, sl := range slots {
		sv := SlotView{ID: sl.ID, Weekday: sl.Weekday, Day: scheduling.WeekdayName(sl.Weekday), Direction: sl.Direction,
			Timing: sl.Timing, LocalPickupTime: scheduling.FormatClock(sl.Pickup),
			EstimatedDropTime: scheduling.FormatClock(sl.Pickup + se.TravelMinutes), DriverID: sl.DriverID, Active: sl.Active}
		if sl.ArriveBy != nil {
			sv.ArriveByTime = ptr(scheduling.FormatClock(*sl.ArriveBy))
		}
		if sl.DriverID != nil {
			d, ok := drivers[*sl.DriverID]
			if !ok {
				d = &DriverView{}
				err := q.QueryRow(ctx, `select user_id, name, vehicle_type, vehicle_number, vehicle_desc, rating::float8
					from drivers where user_id = $1`, *sl.DriverID).
					Scan(&d.ID, &d.Name, &d.VehicleType, &d.VehicleNumber, &d.VehicleDesc, &d.Rating)
				if err != nil {
					return v, err
				}
				drivers[*sl.DriverID] = d
			}
			sv.Driver = d
		}
		v.Slots = append(v.Slots, sv)
	}
	var o OfferSummary
	err = q.QueryRow(ctx, `select id, kind, round, status, opened_at, expires_at, outcome from offers
		where series_id = $1 and kind = 'series' order by opened_at desc, round desc limit 1`, id).
		Scan(&o.ID, &o.Kind, &o.Round, &o.Status, &o.OpenedAt, &o.ExpiresAt, &o.Outcome)
	if err != nil && !noRows(err) {
		return v, err
	}
	if err == nil {
		v.Offer = &o
		if o.Status != "closed" {
			v.Round, v.ExpiresAt = ptr(o.Round), ptr(o.ExpiresAt)
		}
	}
	return v, nil
}

// PreviewSlot is a slot in series.preview.
type PreviewSlot struct {
	Weekday           int     `json:"weekday"`
	Day               string  `json:"day"`
	Direction         string  `json:"direction"`
	Timing            string  `json:"timing"`
	ArriveByTime      *string `json:"arrive_by_time"`
	LocalPickupTime   string  `json:"local_pickup_time"`
	EstimatedDropTime string  `json:"estimated_drop_time"`
}

// PreviewView is series.preview's answer.
type PreviewView struct {
	VehicleType   string        `json:"vehicle_type"`
	DistanceKm    float64       `json:"distance_km"`
	TravelMinutes int           `json:"travel_minutes"`
	StartDate     string        `json:"start_date"`
	SuggestedFare int           `json:"suggested_fare"`
	RidesPerMonth int           `json:"rides_per_month"`
	TimeZone      string        `json:"time_zone"`
	Slots         []PreviewSlot `json:"slots"`
}

// Preview works out a request without saving anything.
func (s *Service) Preview(ctx context.Context, r SeriesRequest) (PreviewView, error) {
	p, err := s.Plan(ctx, s.DB, r)
	if err != nil {
		return PreviewView{}, err
	}
	v := PreviewView{VehicleType: p.VehicleType, DistanceKm: p.DistanceKm, TravelMinutes: p.TravelMinutes,
		StartDate: scheduling.FormatDate(p.StartDate), SuggestedFare: p.SuggestedFare,
		RidesPerMonth: scheduling.RidesPerMonth(len(p.Slots)), TimeZone: p.TimeZone}
	for _, sl := range p.Slots {
		ps := PreviewSlot{Weekday: sl.Weekday, Day: scheduling.WeekdayName(sl.Weekday), Direction: sl.Direction, Timing: sl.Timing,
			LocalPickupTime: scheduling.FormatClock(sl.Pickup), EstimatedDropTime: scheduling.FormatClock(sl.Pickup + p.TravelMinutes)}
		if sl.ArriveBy != nil {
			ps.ArriveByTime = ptr(scheduling.FormatClock(*sl.ArriveBy))
		}
		v.Slots = append(v.Slots, ps)
	}
	return v, nil
}

// ---------------------------------------------------------------- rider operations

func insertSlots(ctx context.Context, q querier, seriesID uuid.UUID, slots []PlannedSlot) error {
	for _, sl := range slots {
		var arrive *string
		if sl.ArriveBy != nil {
			arrive = ptr(scheduling.FormatClock(*sl.ArriveBy))
		}
		if _, err := q.Exec(ctx, `insert into series_slots (series_id, weekday, direction, timing, arrive_by_time, local_pickup_time)
			values ($1, $2, $3, $4, $5::time, $6::time)`,
			seriesID, sl.Weekday, sl.Direction, sl.Timing, arrive, scheduling.FormatClock(sl.Pickup)); err != nil {
			return err
		}
	}
	return nil
}

// Create saves a series as finding_driver and opens round 1 (or, with no
// eligible driver, goes straight to no_driver_found).
func (s *Service) Create(ctx context.Context, riderID uuid.UUID, r SeriesRequest) (SeriesView, error) {
	now := s.Clock.Now()
	var id uuid.UUID
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		p, err := s.Plan(ctx, tx, r)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `insert into series (rider_id, status, city, vehicle_type, preferences,
			pickup_lat, pickup_lng, pickup_label, drop_lat, drop_lng, drop_label, time_zone, start_date,
			distance_km, travel_minutes, suggested_fare, usual_payment, requested_at, created_at, updated_at)
			values ($1, 'finding_driver', $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $17, $17)
			returning id`,
			riderID, p.City, p.VehicleType, p.Preferences, p.Pickup.Lat, p.Pickup.Lng, p.Pickup.Label,
			p.Drop.Lat, p.Drop.Lng, p.Drop.Label, p.TimeZone, p.StartDate, p.DistanceKm, p.TravelMinutes,
			p.SuggestedFare, p.UsualPayment, now).Scan(&id)
		if err != nil {
			return err
		}
		if err := insertSlots(ctx, tx, id, p.Slots); err != nil {
			return err
		}
		if err := event(ctx, tx, now, &riderID, "series", id, "series.requested",
			map[string]any{"old_status": nil, "new_status": "finding_driver"}); err != nil {
			return err
		}
		se, err := lockSeries(ctx, tx, id)
		if err != nil {
			return err
		}
		return s.openRound(ctx, tx, se, 1, now)
	})
	if err != nil {
		return SeriesView{}, err
	}
	return s.seriesView(ctx, s.DB, id)
}

// Get is series.get for the series' rider.
func (s *Service) Get(ctx context.Context, riderID, id uuid.UUID) (SeriesView, error) {
	if err := s.checkOwner(ctx, s.DB, riderID, id); err != nil {
		return SeriesView{}, err
	}
	return s.seriesView(ctx, s.DB, id)
}

// List is the rider's series, newest first.
func (s *Service) List(ctx context.Context, riderID uuid.UUID) ([]SeriesView, error) {
	rows, err := s.DB.Query(ctx, "select id from series where rider_id = $1 order by requested_at desc", riderID)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	out := []SeriesView{}
	for _, id := range ids {
		v, err := s.seriesView(ctx, s.DB, id)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Service) checkOwner(ctx context.Context, q querier, riderID, id uuid.UUID) error {
	var owner uuid.UUID
	err := q.QueryRow(ctx, "select rider_id from series where id = $1", id).Scan(&owner)
	if noRows(err) {
		return errNotFound("series")
	}
	if err != nil {
		return err
	}
	if owner != riderID {
		return errf(403, "forbidden", "This isn't your series")
	}
	return nil
}

// CancelRequest ends a series that is still finding a driver (or found
// none) and closes its open offer; interested drivers are told. Once a
// driver is assigned it is 409 already_assigned with the series as state.
func (s *Service) CancelRequest(ctx context.Context, riderID, id uuid.UUID) error {
	now := s.Clock.Now()
	var conflict bool
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if err := s.checkOwner(ctx, tx, riderID, id); err != nil {
			return err
		}
		se, err := lockSeries(ctx, tx, id)
		if err != nil {
			return err
		}
		switch se.Status {
		case "ended":
			return nil
		case "active", "paused":
			conflict = true
			return nil
		}
		if _, err := tx.Exec(ctx, "update series set status = 'ended', version = version + 1, updated_at = $2 where id = $1", id, now); err != nil {
			return err
		}
		if err := event(ctx, tx, now, &riderID, "series", id, "series.cancelled",
			map[string]any{"old_status": se.Status, "new_status": "ended"}); err != nil {
			return err
		}
		return s.cancelLiveOffers(ctx, tx, se, &riderID, now)
	})
	if err != nil {
		return err
	}
	if conflict {
		v, err := s.seriesView(ctx, s.DB, id)
		if err != nil {
			return err
		}
		name := "Your driver"
		for _, sl := range v.Slots {
			if sl.Driver != nil {
				name = FirstName(sl.Driver.Name)
				break
			}
		}
		return &Error{Status: 409, Code: "already_assigned",
			Message: fmt.Sprintf("%s was just assigned. Keep them, or end the series?", name), State: v}
	}
	return nil
}

// cancelLiveOffers closes the series' open or closing offer as cancelled
// and tells its interested drivers.
func (s *Service) cancelLiveOffers(ctx context.Context, tx pgx.Tx, se seriesRow, actor *uuid.UUID, now time.Time) error {
	rows, err := tx.Query(ctx, `update offers set status = 'closed', outcome = 'cancelled', closed_at = $2
		where series_id = $1 and status in ('open', 'closing') returning id`, se.ID, now)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, oid := range ids {
		if err := event(ctx, tx, now, actor, "offer", oid, "offer.cancelled", map[string]any{"outcome": "cancelled"}); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `select driver_id from offer_recipients
			where offer_id = $1 and interested_at is not null and result is null`, oid)
		if err != nil {
			return err
		}
		drivers, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		for _, d := range drivers {
			if err := s.push(ctx, tx, now, d, "offer_withdrawn", "Request withdrawn", "The rider withdrew this request.",
				map[string]string{"offer_id": oid.String(), "series_id": se.ID.String()}); err != nil {
				return err
			}
		}
	}
	return nil
}

// Retry starts a fresh set of rounds for a no_driver_found series,
// optionally with new times or ride type (the slots are rewritten).
func (s *Service) Retry(ctx context.Context, riderID, id uuid.UUID, r SeriesRequest) (SeriesView, error) {
	now := s.Clock.Now()
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if err := s.checkOwner(ctx, tx, riderID, id); err != nil {
			return err
		}
		se, err := lockSeries(ctx, tx, id)
		if err != nil {
			return err
		}
		if se.Status != "no_driver_found" {
			v, err := s.seriesView(ctx, tx, id)
			if err != nil {
				return err
			}
			return &Error{Status: 409, Code: "not_retryable", Message: "Only a request with no driver yet can be tried again.", State: v}
		}
		// The request as it stands, with the changes laid over it.
		slots, err := readSlots(ctx, tx, id)
		if err != nil {
			return err
		}
		merged := SeriesRequest{Pickup: &se.Pickup, Drop: &se.Drop, VehicleType: se.VehicleType,
			Preferences: se.Preferences, UsualPayment: se.UsualPayment}
		for _, sl := range slots {
			t := sl.Pickup
			if sl.ArriveBy != nil {
				t = *sl.ArriveBy
			}
			merged.Slots = append(merged.Slots, SlotRequest{sl.Weekday, sl.Direction, sl.Timing, scheduling.FormatClock(t)})
		}
		if r.Pickup != nil {
			merged.Pickup = r.Pickup
		}
		if r.Drop != nil {
			merged.Drop = r.Drop
		}
		if vt := r.vehicleType(); vt != "" {
			merged.VehicleType = vt
		}
		if r.Preferences != nil {
			merged.Preferences = r.Preferences
		}
		if r.UsualPayment != "" {
			merged.UsualPayment = r.UsualPayment
		}
		if len(r.Slots) > 0 {
			merged.Slots = r.Slots
		}
		merged.StartDate = r.StartDate
		p, err := s.Plan(ctx, tx, merged)
		if err != nil {
			return err
		}
		// No rides exist yet (no driver was ever assigned): rewrite the slots.
		if _, err := tx.Exec(ctx, "delete from series_slots where series_id = $1", id); err != nil {
			return err
		}
		if err := insertSlots(ctx, tx, id, p.Slots); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `update series set status = 'finding_driver', vehicle_type = $2, preferences = $3,
			pickup_lat = $4, pickup_lng = $5, pickup_label = $6, drop_lat = $7, drop_lng = $8, drop_label = $9,
			start_date = $10, distance_km = $11, travel_minutes = $12, suggested_fare = $13, usual_payment = $14,
			requested_at = $15, attempt = attempt + 1, version = version + 1, updated_at = $15 where id = $1`,
			id, p.VehicleType, p.Preferences, p.Pickup.Lat, p.Pickup.Lng, p.Pickup.Label, p.Drop.Lat, p.Drop.Lng, p.Drop.Label,
			p.StartDate, p.DistanceKm, p.TravelMinutes, p.SuggestedFare, p.UsualPayment, now)
		if err != nil {
			return err
		}
		if err := event(ctx, tx, now, &riderID, "series", id, "series.retried",
			map[string]any{"old_status": se.Status, "new_status": "finding_driver", "changed": len(r.Slots) > 0 || r.vehicleType() != ""}); err != nil {
			return err
		}
		se, err = lockSeries(ctx, tx, id)
		if err != nil {
			return err
		}
		return s.openRound(ctx, tx, se, 1, now)
	})
	if err != nil {
		return SeriesView{}, err
	}
	return s.seriesView(ctx, s.DB, id)
}

// ---------------------------------------------------------------- wording

var shortDays = []string{"", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}

// dayList is "Mon, Wed and Fri".
func dayList(days []int) string {
	var names []string
	for _, d := range days {
		names = append(names, shortDays[d])
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// scheduleLine is "Mon/Wed/Fri 08:30", or "Mon/Wed 08:30, Fri 09:30" when
// the outbound times differ.
func scheduleLine(slots []slotRow) string {
	var order []int
	byTime := map[int][]string{}
	for _, sl := range slots {
		if sl.Direction != "outbound" {
			continue
		}
		if _, ok := byTime[sl.Pickup]; !ok {
			order = append(order, sl.Pickup)
		}
		byTime[sl.Pickup] = append(byTime[sl.Pickup], shortDays[sl.Weekday])
	}
	var parts []string
	for _, t := range order {
		parts = append(parts, strings.Join(byTime[t], "/")+" "+scheduling.FormatClock(t))
	}
	return strings.Join(parts, ", ")
}

func outboundDays(slots []slotRow) []int {
	var days []int
	for _, sl := range slots {
		if len(days) == 0 || days[len(days)-1] != sl.Weekday {
			days = append(days, sl.Weekday)
		}
	}
	return days
}
