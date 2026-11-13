package app

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"commute-backend/internal/db"
	"commute-backend/internal/domain"
)

// RideRow is one ride record.
type RideRow struct {
	ID              string
	CommuteID       string
	RiderID         string
	DriverID        string
	RideDate        string
	Direction       string
	Pickup          domain.Place
	DropOff         domain.Place
	PickupAt        time.Time
	WindowStart     time.Time
	WindowEnd       time.Time
	TripMinutes     int
	Fare            int
	RideType        string
	Pin             string
	PinTries        int
	State           string
	DepartedAt      *time.Time
	ArrivedAt       *time.Time
	StartedAt       *time.Time
	EndedAt         *time.Time
	ClosedAt        *time.Time
	WaitEndsAt      *time.Time
	RiderLateUsed   bool
	LateFlaggedAt   *time.Time
	EndReason       *string
	RiderPayment    *string
	RiderMarkedAt   *time.Time
	DriverPayment   *string
	DriverMarkedAt  *time.Time
	Route           *string
	ApproachRoute   *string
	ApproachMinutes *int
	ApproachKm      *float64
	ApproachAt      *time.Time
}

const rideColumns = `r.id, r.commute_id, r.rider_id, r.driver_id, to_char(r.ride_date, 'YYYY-MM-DD'), r.direction, r.pickup,
	r.drop_off, r.pickup_at, r.window_start, r.window_end, r.trip_minutes, r.fare, r.ride_type, r.pin, r.pin_tries, r.state,
	r.departed_at, r.arrived_at, r.started_at, r.ended_at, r.closed_at, r.wait_ends_at, r.rider_late_used, r.late_flagged_at,
	r.end_reason, r.rider_payment, r.rider_marked_at, r.driver_payment, r.driver_marked_at, r.route, r.approach_route,
	r.approach_minutes, r.approach_km, r.approach_at`

func (r *RideRow) dest() []any {
	return []any{&r.ID, &r.CommuteID, &r.RiderID, &r.DriverID, &r.RideDate, &r.Direction, &r.Pickup,
		&r.DropOff, &r.PickupAt, &r.WindowStart, &r.WindowEnd, &r.TripMinutes, &r.Fare, &r.RideType, &r.Pin, &r.PinTries, &r.State,
		&r.DepartedAt, &r.ArrivedAt, &r.StartedAt, &r.EndedAt, &r.ClosedAt, &r.WaitEndsAt, &r.RiderLateUsed, &r.LateFlaggedAt,
		&r.EndReason, &r.RiderPayment, &r.RiderMarkedAt, &r.DriverPayment, &r.DriverMarkedAt, &r.Route, &r.ApproachRoute,
		&r.ApproachMinutes, &r.ApproachKm, &r.ApproachAt}
}

// queryRides runs a query that selects rideColumns.
func queryRides(ctx context.Context, q db.Q, sql string, args ...any) ([]RideRow, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (RideRow, error) {
		var r RideRow
		err := row.Scan(r.dest()...)
		return r, err
	})
}

// LoadRide is ride [id], or nil if there is none.
func LoadRide(ctx context.Context, q db.Q, id string, forUpdate bool) (*RideRow, error) {
	sql := "select " + rideColumns + " from rides r where r.id = $1"
	if forUpdate {
		sql += " for update of r"
	}
	var r RideRow
	err := q.QueryRow(ctx, sql, id).Scan(r.dest()...)
	if noRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (a *App) mustLoadRide(ctx context.Context, id string) (*RideRow, error) {
	r, err := LoadRide(ctx, a.DB, id, false)
	if err == nil && r == nil {
		err = apiErr(404, "ride_not_found", "No such ride")
	}
	return r, err
}

// ---------------------------------------------------------------- creation

// CreateRides creates the ride records for active commutes (or just
// [commuteID] when given) from today to 14 days ahead. Unique on (commute,
// date, direction), so running it twice does nothing. Runs nightly and
// straight after a commute becomes active.
func (a *App) CreateRides(ctx context.Context, commuteID string) (int, error) {
	now := a.now()
	today := domain.LocalDateString(now, a.Config.UTCOffsetMinutes)
	until := domain.AddDays(today, domain.CreateDaysAhead)
	var filter *string
	if commuteID != "" {
		filter = &commuteID
	}
	type commute struct {
		id, riderID, driverID, rideType string
		plan                            domain.CommuteForPlan
		tripMinutes, fare               int
		routeOut, routeBack             *string
	}
	rows, err := a.DB.Query(ctx, `select id, rider_id, driver_id, pickup, drop_off, weekdays, pickup_time, return_trip, return_time,
		  to_char(start_date, 'YYYY-MM-DD'), trip_minutes, fare, ride_type, route_out, route_back
		from commutes where status = 'active' and ($1::uuid is null or id = $1)`, filter)
	if err != nil {
		return 0, err
	}
	commutes, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (commute, error) {
		var c commute
		p := &c.plan
		err := row.Scan(&c.id, &c.riderID, &c.driverID, &p.Pickup, &p.Drop, &p.Weekdays, &p.PickupTime, &p.ReturnTrip, &p.ReturnTime,
			&p.StartDate, &c.tripMinutes, &c.fare, &c.rideType, &c.routeOut, &c.routeBack)
		return c, err
	})
	if err != nil || len(commutes) == 0 {
		return 0, err
	}
	rows, err = a.DB.Query(ctx, `select to_char(day, 'YYYY-MM-DD') from holidays where city = $1 and day between $2::text::date and $3::text::date`,
		a.Config.City, today, until)
	if err != nil {
		return 0, err
	}
	days, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	holidays := map[string]bool{}
	for _, d := range days {
		holidays[d] = true
	}
	created := 0
	for _, c := range commutes {
		plan := domain.PlanRides(c.plan, domain.PlanOptions{From: today, To: until, Now: now, UTCOffsetMinutes: a.Config.UTCOffsetMinutes, Holidays: holidays})
		for _, p := range plan {
			state := "scheduled"
			var closedAt *time.Time
			if p.Skipped {
				state = "skipped"
				closedAt = &now
			}
			route := c.routeOut
			if p.Direction != "outbound" {
				route = c.routeBack
			}
			tag, err := a.DB.Exec(ctx, `insert into rides (commute_id, rider_id, driver_id, ride_date, direction, pickup, drop_off, pickup_at,
				  window_start, window_end, trip_minutes, fare, ride_type, pin, state, closed_at, route)
				values ($1, $2, $3, $4::text::date, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
				on conflict (commute_id, ride_date, direction) do nothing`,
				c.id, c.riderID, c.driverID, p.RideDate, p.Direction, p.Pickup, p.Drop, p.PickupAt,
				p.WindowStart, p.WindowEnd, c.tripMinutes, c.fare, c.rideType, domain.NewPin(), state, closedAt, route)
			if err != nil {
				return created, err
			}
			created += int(tag.RowsAffected())
		}
	}
	return created, nil
}

// ------------------------------------------------------------------- moves

func facts(r *RideRow) domain.RideFacts {
	last := r.PickupAt
	found := false
	for _, t := range []*time.Time{r.DepartedAt, r.ArrivedAt, r.StartedAt} {
		if t != nil && (!found || t.After(last)) {
			last, found = *t, true
		}
	}
	return domain.RideFacts{State: r.State, PickupAt: r.PickupAt, WindowStart: r.WindowStart, WindowEnd: r.WindowEnd,
		ArrivedAt: r.ArrivedAt, WaitEndsAt: r.WaitEndsAt, RiderLateUsed: r.RiderLateUsed, Pin: r.Pin, PinTries: r.PinTries,
		LastActivityAt: last}
}

// Actor is who makes a move: rider, driver, system or ops.
type Actor struct {
	Type string
	ID   string // "" for system
}

// MoveInput is what a driver sends with a move.
type MoveInput struct {
	Pin      string
	Lat, Lng *float64
	Reason   string
}

// ApplyMove makes [move] on ride [rideID] as [actor], in one transaction:
// checks the rules, writes the ride and a ride_events row, and queues the
// notifications the move causes. Only this function changes a ride's state.
func (a *App) ApplyMove(ctx context.Context, rideID, move string, actor Actor, input MoveInput) (*RideRow, error) {
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		ride, err := LoadRide(ctx, tx, rideID, true)
		if err != nil {
			return err
		}
		if ride == nil {
			return apiErr(404, "ride_not_found", "No such ride")
		}
		if (actor.Type == "driver" && ride.DriverID != actor.ID) || (actor.Type == "rider" && ride.RiderID != actor.ID) {
			return apiErr(403, "not_your_ride", "This ride is not yours")
		}
		now := a.now()
		c := domain.MoveContext{Now: now, Actor: actor.Type}
		switch move {
		case "depart":
			// Lock the driver so two departures can't both pass the "busy" check.
			err := tx.QueryRow(ctx, "select on_duty from drivers where id = $1 for update", ride.DriverID).Scan(&c.DriverOnDuty)
			if err != nil && !noRows(err) {
				return err
			}
			if err := tx.QueryRow(ctx, "select exists (select 1 from rides where driver_id = $1 and id <> $2 and state = any($3))",
				ride.DriverID, ride.ID, domain.OpenStates).Scan(&c.DriverBusy); err != nil {
				return err
			}
		case "arrive":
			var loc domain.LatLng
			err := tx.QueryRow(ctx, "select lat, lng from driver_locations where driver_id = $1", ride.DriverID).Scan(&loc.Lat, &loc.Lng)
			if err == nil {
				m := domain.MetresBetween(loc, ride.Pickup.Point())
				c.MetresToPickup = &m
			} else if !noRows(err) {
				return err
			}
		case "start":
			c.Pin = input.Pin
		case "end":
			if input.Lat != nil && input.Lng != nil {
				m := domain.MetresBetween(domain.LatLng{Lat: *input.Lat, Lng: *input.Lng}, ride.DropOff.Point())
				c.MetresToDrop = &m
			}
			c.EndReason = input.Reason
		case "no_show":
			if err := tx.QueryRow(ctx, "select exists (select 1 from ride_contacts where ride_id = $1)", ride.ID).Scan(&c.ContactLogged); err != nil {
				return err
			}
		}

		result, err := domain.Transition(facts(ride), move, c)
		if err != nil {
			if domain.IsWrongPin(err) && ride.PinTries < domain.PinTries {
				// Count the wrong try even though the move fails.
				if _, e := tx.Exec(ctx, "update rides set pin_tries = pin_tries + 1 where id = $1", ride.ID); e != nil {
					return e
				}
				if e := tx.Commit(ctx); e != nil {
					return e
				}
			}
			return err
		}

		cols, vals := result.Set.Columns()
		sets := ""
		for i, col := range cols {
			sets += fmt.Sprintf(", %s = $%d", col, i+3)
		}
		if _, err := tx.Exec(ctx, "update rides set state = $2"+sets+" where id = $1", append([]any{ride.ID, result.To}, vals...)...); err != nil {
			return err
		}
		data := map[string]any{}
		if move == "end" && input.Reason != "" {
			data["reason"] = input.Reason
		}
		var actorID *string
		if actor.ID != "" {
			actorID = &actor.ID
		}
		if _, err := tx.Exec(ctx, `insert into ride_events (ride_id, from_state, to_state, actor_type, actor_id, at, data)
			values ($1, $2, $3, $4, $5, $6, $7)`, ride.ID, ride.State, result.To, actor.Type, actorID, now, data); err != nil {
			return err
		}
		kind := map[string]string{"depart": "driver_on_the_way", "arrive": "driver_arrived"}[move]
		if kind != "" {
			payload, err := a.notifyPayload(ctx, tx, ride)
			if err != nil {
				return err
			}
			return queueNotification(ctx, tx, notification{To: Recipient{"rider", ride.RiderID}, Kind: kind, RideID: ride.ID, SendAfter: now, Payload: payload})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return LoadRide(ctx, a.DB, rideID, false)
}

// LocalTimeLabel is "8:40 AM" for [at] in the rider's time zone.
func LocalTimeLabel(at time.Time, utcOffsetMinutes int) string {
	m := int(domain.JSRound(math.Mod(float64(at.UnixMilli())/60_000+float64(utcOffsetMinutes), 1440)+1440)) % 1440
	h := m / 60
	ampm := "AM"
	if h >= 12 {
		ampm = "PM"
	}
	return fmt.Sprintf("%d:%02d %s", (h+11)%12+1, m%60, ampm)
}

func (a *App) notifyPayload(ctx context.Context, q db.Q, ride *RideRow) (map[string]any, error) {
	first, err := driverFirstName(ctx, q, ride.DriverID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"driverFirstName": first, "pickupTime": LocalTimeLabel(ride.PickupAt, a.Config.UTCOffsetMinutes)}, nil
}

func driverFirstName(ctx context.Context, q db.Q, driverID string) (string, error) {
	var name string
	err := q.QueryRow(ctx, "select name from drivers where id = $1", driverID).Scan(&name)
	if noRows(err) {
		return "Your driver", nil
	}
	return firstName(name), err
}

// --------------------------------------------------------------- live view

type driverPosition struct {
	Lat     float64  `json:"lat"`
	Lng     float64  `json:"lng"`
	Heading *float64 `json:"heading"`
	At      string   `json:"at"`
}

type liveView struct {
	State      string          `json:"state"`
	Driver     *driverPosition `json:"driver"`
	EtaMinutes *int            `json:"etaMinutes"`
	EtaAt      *string         `json:"etaAt"`
	WaitEndsAt *string         `json:"waitEndsAt"`
	Late       bool            `json:"late"`
	PinLocked  bool            `json:"pinLocked"`
	// While on the way: the driver's road route to the pickup (encoded), or null.
	ApproachRoute *string `json:"approachRoute"`
	UpdatedAt     *string `json:"updatedAt"`
	// The server's clock, so the app's countdowns don't depend on the phone's.
	ServerTime string `json:"serverTime"`
}

// liveView is where the driver is and when they'll get there. Also sets the
// late flag the moment the ETA crosses the threshold (the worker does the
// same for riders who aren't looking), and queues "Driver running late" once.
func (a *App) liveView(ctx context.Context, ride *RideRow) (liveView, error) {
	now := a.now()
	v := liveView{State: ride.State, WaitEndsAt: isoPtr(ride.WaitEndsAt), PinLocked: ride.PinTries >= domain.PinTries, ServerTime: iso(now)}
	var loc *domain.LatLng
	if slices.Contains(domain.OpenStates, ride.State) {
		var p driverPosition
		var at time.Time
		err := a.DB.QueryRow(ctx, "select lat, lng, heading, at from driver_locations where driver_id = $1", ride.DriverID).
			Scan(&p.Lat, &p.Lng, &p.Heading, &at)
		if err == nil {
			p.At = iso(at)
			v.Driver = &p
			v.UpdatedAt = &p.At
			loc = &domain.LatLng{Lat: p.Lat, Lng: p.Lng}
		} else if !noRows(err) {
			return v, err
		}
	}
	var eta *int
	switch {
	case loc != nil && ride.State == "on_the_way":
		e := approachEta(ride, *loc)
		eta = &e
	case loc != nil && ride.State == "in_ride":
		e := inRideEta(ride, *loc)
		eta = &e
	case ride.State == "arrived":
		e := 0
		eta = &e
	}
	v.EtaMinutes = eta
	v.Late = ride.LateFlaggedAt != nil
	if !v.Late && eta != nil {
		late, err := a.flagIfLate(ctx, ride, *eta)
		if err != nil {
			return v, err
		}
		v.Late = late
	}
	if eta != nil {
		s := iso(now.Add(time.Duration(*eta) * time.Minute))
		v.EtaAt = &s
	}
	if ride.State == "on_the_way" {
		v.ApproachRoute = ride.ApproachRoute
	}
	return v, nil
}

// inRideEta is the minutes left in the ride: the trip time it was booked
// with (a road time from the maps provider) times the share of the
// straight-line distance still to go. Agrees with the trip time shown at
// booking, and needs no maps call on every 5-second poll.
func inRideEta(ride *RideRow, at domain.LatLng) int {
	total := domain.HaversineKm(ride.Pickup.Lat, ride.Pickup.Lng, ride.DropOff.Lat, ride.DropOff.Lng)
	left := domain.HaversineKm(at.Lat, at.Lng, ride.DropOff.Lat, ride.DropOff.Lng)
	if total < 0.05 {
		return 1
	}
	return max(1, int(domain.JSRound(float64(ride.TripMinutes)*math.Min(1, left/total))))
}

// approachEta is the minutes until the driver reaches the pickup: the drive
// time of their road route (from the maps provider) scaled by how much of
// the straight-line distance is left; the straight-line estimate before
// there is a route.
func approachEta(ride *RideRow, at domain.LatLng) int {
	now := domain.HaversineKm(at.Lat, at.Lng, ride.Pickup.Lat, ride.Pickup.Lng)
	start := 0.0
	if ride.ApproachKm != nil {
		start = *ride.ApproachKm
	}
	if ride.ApproachMinutes == nil || start <= 0.05 {
		return domain.EtaMinutes(now)
	}
	return max(1, int(domain.JSRound(float64(*ride.ApproachMinutes)*math.Min(1, now/start))))
}

const (
	approachOffRouteKm = 0.3
	approachRefresh    = time.Minute
)

// refreshApproachRoute keeps the driver's road route to the pickup current
// while they're on the way: asked for once when there is none, and again
// when they've left it by more than 300 m (at most once a minute). Errors
// leave the old route.
func (a *App) refreshApproachRoute(ctx context.Context, ride *RideRow, at domain.LatLng, force bool) error {
	if ride.State != "on_the_way" {
		return nil
	}
	now := a.now()
	if !force {
		if ride.ApproachAt != nil && now.Sub(*ride.ApproachAt) < approachRefresh {
			return nil
		}
		if ride.ApproachRoute != nil {
			// A damaged route is asked for again.
			if path, err := domain.DecodePolyline(*ride.ApproachRoute); err == nil && domain.DistanceToPathKm(at, path) < approachOffRouteKm {
				return nil
			}
		}
		// No route (the demo city gives none): the drive time is refreshed once a minute.
	}
	trip, err := a.maps().EstimateTrip(ctx, at, ride.Pickup.Point())
	if err != nil {
		return nil
	}
	var route *string
	if trip.Polyline != "" {
		route = &trip.Polyline
	}
	_, err = a.DB.Exec(ctx, "update rides set approach_route = $2, approach_minutes = $3, approach_km = $4, approach_at = $5 where id = $1",
		ride.ID, route, trip.Minutes, domain.HaversineKm(at.Lat, at.Lng, ride.Pickup.Lat, ride.Pickup.Lng), now)
	return err
}

// flagIfLate sets late_flagged_at once and queues the push. Returns whether
// the ride is late.
func (a *App) flagIfLate(ctx context.Context, ride *RideRow, eta int) (bool, error) {
	if ride.State != "scheduled" && ride.State != "on_the_way" {
		return ride.LateFlaggedAt != nil, nil
	}
	now := a.now()
	if !domain.IsLate(now, eta, ride.WindowEnd) {
		return false, nil
	}
	tag, err := a.DB.Exec(ctx, "update rides set late_flagged_at = $2 where id = $1 and late_flagged_at is null", ride.ID, now)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() > 0 {
		payload, err := a.notifyPayload(ctx, a.DB, ride)
		if err != nil {
			return false, err
		}
		if err := queueNotification(ctx, a.DB, notification{To: Recipient{"rider", ride.RiderID}, Kind: "driver_running_late",
			RideID: ride.ID, SendAfter: now, Payload: payload}); err != nil {
			return false, err
		}
	}
	return true, nil
}

// ------------------------------------------------------------ JSON shapes

type driverCard struct {
	driverJSON
	Phone *string `json:"phone"`
}

type ratingJSON struct {
	Stars     int      `json:"stars"`
	Tags      []string `json:"tags"`
	Note      *string  `json:"note"`
	DontMatch bool     `json:"dontMatch"`
}

// RiderRide is a ride as the rider sees it. The PIN shows once the driver
// has departed.
type RiderRide struct {
	ID          string       `json:"id"`
	CommuteID   string       `json:"commuteId"`
	Date        string       `json:"date"`
	Direction   string       `json:"direction"`
	State       string       `json:"state"`
	Pickup      domain.Place `json:"pickup"`
	Drop        domain.Place `json:"drop"`
	PickupAt    string       `json:"pickupAt"`
	WindowStart string       `json:"windowStart"`
	WindowEnd   string       `json:"windowEnd"`
	TripMinutes int          `json:"tripMinutes"`
	Fare        int          `json:"fare"`
	RideType    string       `json:"rideType"`
	// Encoded polyline of the road route, or null (draw a straight line).
	Route         *string     `json:"route"`
	Pin           *string     `json:"pin"`
	PinLocked     bool        `json:"pinLocked"`
	DepartedAt    *string     `json:"departedAt"`
	ArrivedAt     *string     `json:"arrivedAt"`
	StartedAt     *string     `json:"startedAt"`
	EndedAt       *string     `json:"endedAt"`
	WaitEndsAt    *string     `json:"waitEndsAt"`
	RiderLateUsed bool        `json:"riderLateUsed"`
	Late          bool        `json:"late"`
	RiderPayment  *string     `json:"riderPayment"`
	DriverPayment *string     `json:"driverPayment"`
	PaymentStatus string      `json:"paymentStatus"`
	Rating        *ratingJSON `json:"rating"`
	Driver        driverCard  `json:"driver"`
}

func (a *App) riderRideJSON(ctx context.Context, r *RideRow) (RiderRide, error) {
	out := RiderRide{ID: r.ID, CommuteID: r.CommuteID, Date: r.RideDate, Direction: r.Direction, State: r.State,
		Pickup: r.Pickup, Drop: r.DropOff, PickupAt: iso(r.PickupAt), WindowStart: iso(r.WindowStart), WindowEnd: iso(r.WindowEnd),
		TripMinutes: r.TripMinutes, Fare: r.Fare, RideType: r.RideType, Route: r.Route, PinLocked: r.PinTries >= domain.PinTries,
		DepartedAt: isoPtr(r.DepartedAt), ArrivedAt: isoPtr(r.ArrivedAt), StartedAt: isoPtr(r.StartedAt), EndedAt: isoPtr(r.EndedAt),
		WaitEndsAt: isoPtr(r.WaitEndsAt), RiderLateUsed: r.RiderLateUsed, Late: r.LateFlaggedAt != nil,
		RiderPayment: r.RiderPayment, DriverPayment: r.DriverPayment, PaymentStatus: domain.PaymentStatus(str(r.RiderPayment), str(r.DriverPayment))}
	if r.State == "on_the_way" || r.State == "arrived" {
		out.Pin = &r.Pin
	}
	var rating ratingJSON
	err := a.DB.QueryRow(ctx, "select stars, tags, note, dont_match from ride_ratings where ride_id = $1", r.ID).
		Scan(&rating.Stars, &rating.Tags, &rating.Note, &rating.DontMatch)
	if err == nil {
		rating.Tags = orEmpty(rating.Tags)
		out.Rating = &rating
	} else if !noRows(err) {
		return out, err
	}
	d := &out.Driver
	err = a.DB.QueryRow(ctx, "select id, name, vehicle, plate, rating, upi_id, phone from drivers where id = $1", r.DriverID).
		Scan(&d.ID, &d.Name, &d.Vehicle, &d.Plate, &d.Rating, &d.UPIID, &d.Phone)
	return out, err
}

func (a *App) riderRidesJSON(ctx context.Context, rows []RideRow) ([]RiderRide, error) {
	out := []RiderRide{}
	for i := range rows {
		j, err := a.riderRideJSON(ctx, &rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, nil
}

type driverRide struct {
	ID            string       `json:"id"`
	Date          string       `json:"date"`
	Direction     string       `json:"direction"`
	State         string       `json:"state"`
	RiderName     string       `json:"riderName"`
	RiderPhone    *string      `json:"riderPhone"`
	Pickup        domain.Place `json:"pickup"`
	Drop          domain.Place `json:"drop"`
	PickupAt      string       `json:"pickupAt"`
	WindowStart   string       `json:"windowStart"`
	WindowEnd     string       `json:"windowEnd"`
	Fare          int          `json:"fare"`
	UsualPayment  *string      `json:"usualPayment"`
	PinTriesLeft  int          `json:"pinTriesLeft"`
	WaitEndsAt    *string      `json:"waitEndsAt"`
	RiderLateUsed bool         `json:"riderLateUsed"`
	RiderPayment  *string      `json:"riderPayment"`
	DriverPayment *string      `json:"driverPayment"`
	PaymentStatus string       `json:"paymentStatus"`
}

// driverRideJSON is a ride as the driver sees it on their run.
func (a *App) driverRideJSON(ctx context.Context, r *RideRow) (driverRide, error) {
	out := driverRide{ID: r.ID, Date: r.RideDate, Direction: r.Direction, State: r.State, RiderName: "Rider",
		Pickup: r.Pickup, Drop: r.DropOff, PickupAt: iso(r.PickupAt), WindowStart: iso(r.WindowStart), WindowEnd: iso(r.WindowEnd),
		Fare: r.Fare, PinTriesLeft: max(0, domain.PinTries-r.PinTries), WaitEndsAt: isoPtr(r.WaitEndsAt), RiderLateUsed: r.RiderLateUsed,
		RiderPayment: r.RiderPayment, DriverPayment: r.DriverPayment, PaymentStatus: domain.PaymentStatus(str(r.RiderPayment), str(r.DriverPayment))}
	var name *string
	var phone string
	err := a.DB.QueryRow(ctx, "select name, phone from riders where id = $1", r.RiderID).Scan(&name, &phone)
	if err != nil && !noRows(err) {
		return out, err
	}
	if name != nil {
		out.RiderName = *name
	}
	if err == nil && slices.Contains(domain.OpenStates, r.State) {
		out.RiderPhone = &phone
	}
	err = a.DB.QueryRow(ctx, "select usual_payment from commutes where id = $1", r.CommuteID).Scan(&out.UsualPayment)
	if err != nil && !noRows(err) {
		return out, err
	}
	return out, nil
}

// nextRideFor is the rider's next ride that hasn't finished, for Home.
func (a *App) nextRideFor(ctx context.Context, riderID string) (*RiderRide, error) {
	rows, err := queryRides(ctx, a.DB, `select `+rideColumns+` from rides r
		where r.rider_id = $1 and r.state in ('scheduled', 'on_the_way', 'arrived', 'in_ride')
		order by r.pickup_at limit 1`, riderID)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	j, err := a.riderRideJSON(ctx, &rows[0])
	return &j, err
}

// ---------------------------------------------------------------- location

// LocationPoint is one position a driver's phone sends.
type LocationPoint struct {
	Lat     *float64 `json:"lat"`
	Lng     *float64 `json:"lng"`
	Heading *float64 `json:"heading"`
	Speed   *float64 `json:"speed"`
	At      string   `json:"at"`

	at time.Time
}

const (
	trackEvery = 30 * time.Second
	maxAge     = 2 * time.Minute
	maxAhead   = 30 * time.Second
)

func (p *LocationPoint) check(c *domain.Check, i int) {
	c.If(p.Lat == nil || *p.Lat < -90 || *p.Lat > 90, "Latitude must be between -90 and 90", "points", i, "lat")
	c.If(p.Lng == nil || *p.Lng < -180 || *p.Lng > 180, "Longitude must be between -180 and 180", "points", i, "lng")
	c.If(p.Heading != nil && (*p.Heading < 0 || *p.Heading > 360), "Heading must be between 0 and 360", "points", i, "heading")
	c.If(p.Speed != nil && *p.Speed < 0, "Speed can't be negative", "points", i, "speed")
	t, err := time.Parse(time.RFC3339Nano, p.At)
	c.If(err != nil, "Invalid datetime", "points", i, "at")
	p.at = t
}

// Point makes a LocationPoint at [at].
func Point(lat, lng float64, at time.Time) LocationPoint {
	return LocationPoint{Lat: &lat, Lng: &lng, At: iso(at), at: at}
}

// RecordLocation stores the driver's latest position, and a track point
// every 30 s while a ride is under way.
func (a *App) RecordLocation(ctx context.Context, driverID string, points []LocationPoint) error {
	now := a.now()
	for i := range points {
		if points[i].at.IsZero() {
			points[i].at, _ = time.Parse(time.RFC3339Nano, points[i].At)
		}
		at := points[i].at
		if at.Before(now.Add(-maxAge)) || at.After(now.Add(maxAhead)) {
			return apiErr(400, "stale_location", "Location points must be from the last 2 minutes")
		}
	}
	latest := points[0]
	for _, p := range points[1:] {
		if p.at.After(latest.at) {
			latest = p
		}
	}
	pos := domain.LatLng{Lat: *latest.Lat, Lng: *latest.Lng}
	rides, err := queryRides(ctx, a.DB, `select `+rideColumns+` from rides r where r.driver_id = $1 and r.state = any($2)
		order by r.pickup_at limit 1`, driverID, domain.OpenStates)
	if err != nil {
		return err
	}
	var current *RideRow
	var currentID *string
	if len(rides) > 0 {
		current = &rides[0]
		currentID = &current.ID
	}
	_, err = a.DB.Exec(ctx, `insert into driver_locations (driver_id, lat, lng, heading, speed, at, ride_id) values ($1, $2, $3, $4, $5, $6, $7)
		on conflict (driver_id) do update set lat = excluded.lat, lng = excluded.lng, heading = excluded.heading,
		  speed = excluded.speed, at = excluded.at, ride_id = excluded.ride_id
		where driver_locations.at <= excluded.at`,
		driverID, pos.Lat, pos.Lng, latest.Heading, latest.Speed, latest.at, currentID)
	if err != nil || current == nil {
		return err
	}
	if err := a.refreshApproachRoute(ctx, current, pos, false); err != nil {
		return err
	}
	var last *time.Time
	if err := a.DB.QueryRow(ctx, "select max(at) from ride_track where ride_id = $1", current.ID).Scan(&last); err != nil {
		return err
	}
	if last == nil || latest.at.Sub(*last) >= trackEvery {
		_, err = a.DB.Exec(ctx, "insert into ride_track (ride_id, lat, lng, at) values ($1, $2, $3, $4)", current.ID, pos.Lat, pos.Lng, latest.at)
	}
	return err
}

// LogContact records a call or alert before a no-show.
func (a *App) LogContact(ctx context.Context, rideID, driverID, kind string) error {
	ride, err := a.mustLoadRide(ctx, rideID)
	if err != nil {
		return err
	}
	if ride.DriverID != driverID {
		return apiErr(403, "not_your_ride", "This ride is not yours")
	}
	if ride.State != "on_the_way" && ride.State != "arrived" {
		return apiErr(409, "invalid_transition", "You can call or alert the rider once you are on the way")
	}
	now := a.now()
	if _, err := a.DB.Exec(ctx, "insert into ride_contacts (ride_id, driver_id, kind, at) values ($1, $2, $3, $4)", rideID, driverID, kind, now); err != nil {
		return err
	}
	if kind == "alert" {
		payload, err := a.notifyPayload(ctx, a.DB, ride)
		if err != nil {
			return err
		}
		return queueNotification(ctx, a.DB, notification{To: Recipient{"rider", ride.RiderID}, Kind: "driver_alert", RideID: rideID, SendAfter: now, Payload: payload})
	}
	return nil
}

// SetDuty puts a driver on or off duty. Not off while a ride is under way.
func (a *App) SetDuty(ctx context.Context, driverID string, onDuty bool) error {
	if !onDuty {
		var busy bool
		if err := a.DB.QueryRow(ctx, "select exists (select 1 from rides where driver_id = $1 and state = any($2))", driverID, domain.OpenStates).Scan(&busy); err != nil {
			return err
		}
		if busy {
			return apiErr(409, "rides_in_progress", "Finish your current ride before going off duty")
		}
	}
	_, err := a.DB.Exec(ctx, "update drivers set on_duty = $2 where id = $1", driverID, onDuty)
	return err
}

// ---------------------------------------------------------------- payment

type paymentJSON struct {
	PaymentStatus string  `json:"paymentStatus"`
	RiderPayment  *string `json:"riderPayment"`
	DriverPayment *string `json:"driverPayment"`
}

func (a *App) markPayment(ctx context.Context, rideID string, side Actor, mark string) (paymentJSON, error) {
	var out paymentJSON
	ride, err := a.mustLoadRide(ctx, rideID)
	if err != nil {
		return out, err
	}
	own := ride.DriverID
	col, at := "driver_payment", "driver_marked_at"
	if side.Type == "rider" {
		own = ride.RiderID
		col, at = "rider_payment", "rider_marked_at"
	}
	if own != side.ID {
		return out, apiErr(403, "not_your_ride", "This ride is not yours")
	}
	if ride.State != "completed" {
		return out, apiErr(409, "not_completed", "Mark payment once the ride has ended")
	}
	err = a.DB.QueryRow(ctx, fmt.Sprintf("update rides set %s = $2, %s = $3 where id = $1 returning rider_payment, driver_payment", col, at),
		rideID, mark, a.now()).Scan(&out.RiderPayment, &out.DriverPayment)
	out.PaymentStatus = domain.PaymentStatus(str(out.RiderPayment), str(out.DriverPayment))
	return out, err
}

const ratingEditDays = 7

// ------------------------------------------------------------------ routes

func (a *App) riderRide(ctx context.Context, riderID, id string) (*RideRow, error) {
	ride, err := a.mustLoadRide(ctx, id)
	if err != nil {
		return nil, err
	}
	if ride.RiderID != riderID {
		return nil, apiErr(403, "not_your_ride", "This ride is not yours")
	}
	return ride, nil
}

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func (a *App) rideRoutes(handle func(string, handler)) {
	// ---- rider ----

	handle("GET /v1/rides/upcoming", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		riderID, err := a.requireRider(r)
		if err != nil {
			return err
		}
		days := 7
		if v := r.URL.Query().Get("days"); v != "" {
			n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil || n != math.Trunc(n) || n < 1 || n > 14 {
				var c domain.Check
				c.Fail("Days must be a whole number from 1 to 14", "days")
				return c.Err()
			}
			days = int(n)
		}
		today := domain.LocalDateString(a.now(), a.Config.UTCOffsetMinutes)
		rows, err := queryRides(ctx, a.DB, `select `+rideColumns+` from rides r
			where r.rider_id = $1 and r.ride_date >= $2::text::date and r.ride_date < $3::text::date
			order by r.pickup_at`, riderID, today, domain.AddDays(today, days))
		if err != nil {
			return err
		}
		out, err := a.riderRidesJSON(ctx, rows)
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"rides": out})
		return nil
	})

	handle("GET /v1/rides/unsettled", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		riderID, err := a.requireRider(r)
		if err != nil {
			return err
		}
		rows, err := queryRides(ctx, a.DB, `select `+rideColumns+` from rides r
			where r.rider_id = $1 and r.state = 'completed' and r.rider_payment is null
			  and r.ended_at > $2::timestamptz - interval '7 days'
			  and not exists (select 1 from ride_ratings x where x.ride_id = r.id)
			order by r.ended_at desc`, riderID, a.now())
		if err != nil {
			return err
		}
		out, err := a.riderRidesJSON(ctx, rows)
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"rides": out})
		return nil
	})

	// riderRideRoute resolves the signed-in rider's ride from the path.
	riderRideRoute := func(r *http.Request) (string, *RideRow, error) {
		riderID, err := a.requireRider(r)
		if err != nil {
			return "", nil, err
		}
		id, err := pathID(r)
		if err != nil {
			return "", nil, err
		}
		ride, err := a.riderRide(r.Context(), riderID, id)
		return riderID, ride, err
	}

	handle("GET /v1/rides/{id}", func(w http.ResponseWriter, r *http.Request) error {
		_, ride, err := riderRideRoute(r)
		if err != nil {
			return err
		}
		out, err := a.riderRideJSON(r.Context(), ride)
		if err != nil {
			return err
		}
		writeJSON(w, 200, out)
		return nil
	})

	handle("GET /v1/rides/{id}/live", func(w http.ResponseWriter, r *http.Request) error {
		_, ride, err := riderRideRoute(r)
		if err != nil {
			return err
		}
		out, err := a.liveView(r.Context(), ride)
		if err != nil {
			return err
		}
		writeJSON(w, 200, out)
		return nil
	})

	// "I'm 5 min late": once per leg; the driver's wait becomes 10 minutes.
	handle("POST /v1/rides/{id}/running-late", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		riderID, ride, err := riderRideRoute(r)
		if err != nil {
			return err
		}
		if ride.RiderLateUsed {
			return apiErr(409, "already_used", "You can only do this once per ride")
		}
		if ride.State != "scheduled" && ride.State != "on_the_way" && ride.State != "arrived" {
			return apiErr(409, "invalid_transition", "This ride has already started")
		}
		var waitEnds *time.Time
		if ride.ArrivedAt != nil {
			t := domain.WaitEndsAt(*ride.ArrivedAt, ride.WindowStart, true)
			waitEnds = &t
		}
		now := a.now()
		if _, err := a.DB.Exec(ctx, "update rides set rider_late_used = true, wait_ends_at = coalesce($2, wait_ends_at) where id = $1", ride.ID, waitEnds); err != nil {
			return err
		}
		if _, err := a.DB.Exec(ctx, `insert into ride_events (ride_id, from_state, to_state, actor_type, actor_id, at, data)
			values ($1, $2, $2, 'rider', $3, $4, '{"riderLate":true}')`, ride.ID, ride.State, riderID, now); err != nil {
			return err
		}
		if err := queueNotification(ctx, a.DB, notification{To: Recipient{"driver", ride.DriverID}, Kind: "rider_running_late", RideID: ride.ID, SendAfter: now}); err != nil {
			return err
		}
		updated, err := a.mustLoadRide(ctx, ride.ID)
		if err != nil {
			return err
		}
		out, err := a.riderRideJSON(ctx, updated)
		if err != nil {
			return err
		}
		writeJSON(w, 200, out)
		return nil
	})

	handle("POST /v1/rides/{id}/payment", func(w http.ResponseWriter, r *http.Request) error {
		riderID, err := a.requireRider(r)
		if err != nil {
			return err
		}
		var body struct{ Method string }
		if err := decode(r, &body); err != nil {
			return err
		}
		var c domain.Check
		c.OneOf(body.Method, []string{"upi", "cash"}, "method")
		if err := c.Err(); err != nil {
			return err
		}
		id, err := pathID(r)
		if err != nil {
			return err
		}
		out, err := a.markPayment(r.Context(), id, Actor{"rider", riderID}, body.Method)
		if err != nil {
			return err
		}
		writeJSON(w, 200, out)
		return nil
	})

	handle("POST /v1/rides/{id}/rating", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		riderID, err := a.requireRider(r)
		if err != nil {
			return err
		}
		var body struct {
			Stars     *int
			Tags      []string
			Note      *string
			DontMatch bool
		}
		if err := decode(r, &body); err != nil {
			return err
		}
		var c domain.Check
		c.If(body.Stars == nil || *body.Stars < 1 || *body.Stars > 5, "Stars must be from 1 to 5", "stars")
		c.If(len(body.Tags) > 10, "At most 10 tags", "tags")
		for i, t := range body.Tags {
			c.If(len([]rune(t)) < 1 || len([]rune(t)) > 40, "Tags are 1 to 40 characters", "tags", i)
		}
		c.If(body.Note != nil && len([]rune(*body.Note)) > 500, "Notes are at most 500 characters", "note")
		if err := c.Err(); err != nil {
			return err
		}
		id, err := pathID(r)
		if err != nil {
			return err
		}
		ride, err := a.riderRide(ctx, riderID, id)
		if err != nil {
			return err
		}
		if ride.State != "completed" {
			return apiErr(409, "not_completed", "Rate the ride once it has ended")
		}
		now := a.now()
		if ride.EndedAt != nil && now.Sub(*ride.EndedAt) > ratingEditDays*24*time.Hour {
			return apiErr(409, "rating_closed", fmt.Sprintf("Ratings can be given or changed for %d days after the ride", ratingEditDays))
		}
		_, err = a.DB.Exec(ctx, `insert into ride_ratings (ride_id, stars, tags, note, dont_match, created_at, updated_at)
			values ($1, $2, $3, $4, $5, $6, $6)
			on conflict (ride_id) do update set stars = excluded.stars, tags = excluded.tags, note = excluded.note,
			  dont_match = excluded.dont_match, updated_at = excluded.updated_at`,
			ride.ID, *body.Stars, orEmpty(body.Tags), body.Note, body.DontMatch, now)
		if err != nil {
			return err
		}
		w.WriteHeader(204)
		return nil
	})

	// Push token for this device; riders and drivers both register.
	handle("PUT /v1/devices", func(w http.ResponseWriter, r *http.Request) error {
		role, id, err := a.requireSession(r)
		if err != nil {
			return err
		}
		var body struct{ PushToken, Platform string }
		if err := decode(r, &body); err != nil {
			return err
		}
		var c domain.Check
		c.If(len(body.PushToken) < 10 || len(body.PushToken) > 4096, "Push tokens are 10 to 4096 characters", "pushToken")
		c.OneOf(body.Platform, []string{"android", "ios", "web"}, "platform")
		if err := c.Err(); err != nil {
			return err
		}
		_, err = a.DB.Exec(r.Context(), `insert into devices (push_token, owner_type, owner_id, platform, updated_at) values ($1, $2, $3, $4, $5)
			on conflict (push_token) do update set owner_type = excluded.owner_type, owner_id = excluded.owner_id,
			  platform = excluded.platform, updated_at = excluded.updated_at`,
			body.PushToken, role, id, body.Platform, a.now())
		if err != nil {
			return err
		}
		w.WriteHeader(204)
		return nil
	})

	// ---- driver (flow D3's API; the driver app's screens come later) ----

	handle("GET /v1/driver/run", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		driverID, err := a.requireDriver(r)
		if err != nil {
			return err
		}
		date := r.URL.Query().Get("date")
		if date == "" {
			date = domain.LocalDateString(a.now(), a.Config.UTCOffsetMinutes)
		}
		if _, err := domain.ParseDate(date); !dateRe.MatchString(date) || err != nil {
			var c domain.Check
			c.Fail("Dates are YYYY-MM-DD", "date")
			return c.Err()
		}
		rows, err := queryRides(ctx, a.DB, `select `+rideColumns+` from rides r where r.driver_id = $1 and r.ride_date = $2::text::date
			order by r.pickup_at`, driverID, date)
		if err != nil {
			return err
		}
		out := []driverRide{}
		for i := range rows {
			j, err := a.driverRideJSON(ctx, &rows[i])
			if err != nil {
				return err
			}
			out = append(out, j)
		}
		writeJSON(w, 200, map[string]any{"date": date, "rides": out})
		return nil
	})

	handle("PUT /v1/driver/duty", func(w http.ResponseWriter, r *http.Request) error {
		driverID, err := a.requireDriver(r)
		if err != nil {
			return err
		}
		var body struct{ OnDuty *bool }
		if err := decode(r, &body); err != nil {
			return err
		}
		if body.OnDuty == nil {
			var c domain.Check
			c.Fail("Required", "onDuty")
			return c.Err()
		}
		if err := a.SetDuty(r.Context(), driverID, *body.OnDuty); err != nil {
			return err
		}
		w.WriteHeader(204)
		return nil
	})

	handle("POST /v1/driver/location", func(w http.ResponseWriter, r *http.Request) error {
		driverID, err := a.requireDriver(r)
		if err != nil {
			return err
		}
		var body struct{ Points []LocationPoint }
		if err := decode(r, &body); err != nil {
			return err
		}
		var c domain.Check
		c.If(len(body.Points) < 1 || len(body.Points) > 20, "Send 1 to 20 points", "points")
		for i := range body.Points {
			body.Points[i].check(&c, i)
		}
		if err := c.Err(); err != nil {
			return err
		}
		if err := a.RecordLocation(r.Context(), driverID, body.Points); err != nil {
			return err
		}
		w.WriteHeader(204)
		return nil
	})

	driverMove := func(path, move string, input func(r *http.Request) (MoveInput, error)) {
		handle("POST /v1/driver/rides/{id}/"+path, func(w http.ResponseWriter, r *http.Request) error {
			driverID, err := a.requireDriver(r)
			if err != nil {
				return err
			}
			id, err := pathID(r)
			if err != nil {
				return err
			}
			in, err := input(r)
			if err != nil {
				return err
			}
			ride, err := a.ApplyMove(r.Context(), id, move, Actor{"driver", driverID}, in)
			if err != nil {
				return err
			}
			out, err := a.driverRideJSON(r.Context(), ride)
			if err != nil {
				return err
			}
			writeJSON(w, 200, out)
			return nil
		})
	}
	none := func(*http.Request) (MoveInput, error) { return MoveInput{}, nil }
	pinRe := regexp.MustCompile(`^\d{4}$`)

	driverMove("depart", "depart", none)
	driverMove("arrive", "arrive", none)
	driverMove("start", "start", func(r *http.Request) (MoveInput, error) {
		var body struct{ Pin string }
		if err := decode(r, &body); err != nil {
			return MoveInput{}, err
		}
		var c domain.Check
		c.If(!pinRe.MatchString(body.Pin), "The PIN is 4 digits", "pin")
		return MoveInput{Pin: body.Pin}, c.Err()
	})
	driverMove("end", "end", func(r *http.Request) (MoveInput, error) {
		var body struct {
			Lat, Lng *float64
			Reason   *string
		}
		if err := decode(r, &body); err != nil {
			return MoveInput{}, err
		}
		var c domain.Check
		c.If(body.Lat == nil, "Required", "lat")
		c.If(body.Lng == nil, "Required", "lng")
		c.If(body.Reason != nil && len([]rune(*body.Reason)) > 300, "Reasons are at most 300 characters", "reason")
		return MoveInput{Lat: body.Lat, Lng: body.Lng, Reason: str(body.Reason)}, c.Err()
	})
	driverMove("no-show", "no_show", none)

	handle("POST /v1/driver/rides/{id}/contact", func(w http.ResponseWriter, r *http.Request) error {
		driverID, err := a.requireDriver(r)
		if err != nil {
			return err
		}
		var body struct{ Kind string }
		if err := decode(r, &body); err != nil {
			return err
		}
		var c domain.Check
		c.OneOf(body.Kind, []string{"call", "alert"}, "kind")
		if err := c.Err(); err != nil {
			return err
		}
		id, err := pathID(r)
		if err != nil {
			return err
		}
		if err := a.LogContact(r.Context(), id, driverID, body.Kind); err != nil {
			return err
		}
		w.WriteHeader(204)
		return nil
	})

	handle("POST /v1/driver/rides/{id}/payment", func(w http.ResponseWriter, r *http.Request) error {
		driverID, err := a.requireDriver(r)
		if err != nil {
			return err
		}
		var body struct{ Mark string }
		if err := decode(r, &body); err != nil {
			return err
		}
		var c domain.Check
		c.OneOf(body.Mark, []string{"upi", "cash", "not_paid"}, "mark")
		if err := c.Err(); err != nil {
			return err
		}
		id, err := pathID(r)
		if err != nil {
			return err
		}
		out, err := a.markPayment(r.Context(), id, Actor{"driver", driverID}, body.Mark)
		if err != nil {
			return err
		}
		writeJSON(w, 200, out)
		return nil
	})
}
