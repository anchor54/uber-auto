package offers

// The ride endpoints (R2 rider, D3 driver). Every change is one transaction
// with a conditional update; a driver's change locks the driver row first,
// then the ride. A refused move is a 409 with the ride as it is now.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"commute-backend/internal/lifecycle"
	"commute-backend/internal/scheduling"
)

// driverMove runs [f] on a ride of [driverID]'s under the driver → ride
// locks. It refuses a ride that isn't the driver's: 409 ride_reassigned (or
// [handedOverCode]) when it was theirs and went to someone else, else 403.
func (s *Service) driverMove(ctx context.Context, driverID, rideID uuid.UUID, handedOverCode string,
	f func(tx pgx.Tx, r rideRow, now time.Time) error) (RideView, error) {
	now := s.Clock.Now()
	var v RideView
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := lockDriver(ctx, tx, driverID); err != nil {
			if noRows(err) {
				return errf(403, "not_a_driver", "This needs a driver account.")
			}
			return err
		}
		r, err := lockRide(ctx, tx, rideID)
		if noRows(err) {
			return errNotFound("ride")
		}
		if err != nil {
			return err
		}
		if r.DriverID == nil || *r.DriverID != driverID {
			if r.PreviousDriverID != nil && *r.PreviousDriverID == driverID {
				return &Error{Status: 409, Code: handedOverCode, Message: "This ride has gone to another driver."}
			}
			return errf(403, "forbidden", "This isn't your ride.")
		}
		if err := f(tx, r, now); err != nil {
			return err
		}
		r, err = readRide(ctx, tx, rideID)
		if err != nil {
			return err
		}
		v, err = s.rideView(ctx, tx, r, false)
		return err
	})
	return v, s.withRideState(ctx, err, rideID, false)
}

// withRideState fills a 4xx's state with the ride as it is now.
func (s *Service) withRideState(ctx context.Context, err error, rideID uuid.UUID, forRider bool) error {
	e, ok := err.(*Error)
	if !ok || e.State != nil || e.Status == 404 || e.Status == 403 {
		return err
	}
	if r, rerr := readRide(ctx, s.DB, rideID); rerr == nil {
		if v, verr := s.rideView(ctx, s.DB, r, forRider); verr == nil {
			e.State = v
		}
	}
	return e
}

func wrongStatus(r rideRow) *Error {
	return errf(409, "wrong_status", "This ride is %s.", strings.ReplaceAll(r.Status, "_", " "))
}

// changeStatus is the conditional update: zero rows means someone got there first.
func changeStatus(ctx context.Context, tx pgx.Tx, r rideRow, to string, now time.Time, set string, args ...any) error {
	q := "update rides set status = $3, version = version + 1, updated_at = $4"
	if set != "" {
		q += ", " + set
	}
	q += " where id = $1 and status = $2"
	tag, err := tx.Exec(ctx, q, append([]any{r.ID, r.Status, to, now}, args...)...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return wrongStatus(r)
	}
	return nil
}

// OnMyWay is ride.on_my_way: assigned → on_the_way from 60 minutes before
// pickup; the rider is told. (HLD: no PIN step for now, so no PIN is made.)
func (s *Service) OnMyWay(ctx context.Context, driverID, rideID uuid.UUID) (RideView, error) {
	return s.driverMove(ctx, driverID, rideID, "ride_reassigned", func(tx pgx.Tx, r rideRow, now time.Time) error {
		if r.Status != lifecycle.Assigned {
			return wrongStatus(r)
		}
		if opens := lifecycle.OnMyWayOpensAt(r.PickupAt); now.Before(opens) {
			return &Error{Status: 409, Code: "too_early", Message: "On my way opens 60 minutes before pickup.",
				Extra: map[string]any{"opens_at": opens}}
		}
		if err := changeStatus(ctx, tx, r, lifecycle.OnTheWay, now, "on_the_way_at = $4"); err != nil {
			return err
		}
		if err := rideEvent(ctx, tx, now, &driverID, r, "ride.on_the_way", lifecycle.OnTheWay, nil); err != nil {
			return err
		}
		c, err := s.rideContext(ctx, tx, r)
		if err != nil {
			return err
		}
		return s.push(ctx, tx, now, r.RiderID, "ride_on_the_way", c.driverFirst()+" is on the way",
			fmt.Sprintf("%s is on his way.", c.driverFirst()), rideData(r))
	})
}

// Arrived is ride.arrived ("I've arrived").
func (s *Service) Arrived(ctx context.Context, driverID, rideID uuid.UUID) (RideView, error) {
	return s.driverMove(ctx, driverID, rideID, "wrong_status", func(tx pgx.Tx, r rideRow, now time.Time) error {
		if r.Status != lifecycle.OnTheWay || r.ArrivedAt != nil || r.PickedUpAt != nil {
			return wrongStatus(r)
		}
		if _, err := tx.Exec(ctx, "update rides set arrived_at = $2, version = version + 1, updated_at = $2 where id = $1", r.ID, now); err != nil {
			return err
		}
		if err := rideEvent(ctx, tx, now, &driverID, r, "ride.arrived", r.Status, nil); err != nil {
			return err
		}
		c, err := s.rideContext(ctx, tx, r)
		if err != nil {
			return err
		}
		return s.push(ctx, tx, now, r.RiderID, "ride_arrived", c.driverFirst()+" has arrived",
			fmt.Sprintf("%s has arrived at %s.", c.driverFirst(), label(c.From.Label, "your pickup")), rideData(r))
	})
}

// Start is ride.start ("Start ride"). HLD: no PIN step for now; the spec's
// PIN check (422 wrong_pin) returns with the PIN.
func (s *Service) Start(ctx context.Context, driverID, rideID uuid.UUID) (RideView, error) {
	return s.driverMove(ctx, driverID, rideID, "wrong_status", func(tx pgx.Tx, r rideRow, now time.Time) error {
		if r.Status != lifecycle.OnTheWay || r.PickedUpAt != nil {
			return wrongStatus(r)
		}
		if _, err := tx.Exec(ctx, "update rides set picked_up_at = $2, version = version + 1, updated_at = $2 where id = $1", r.ID, now); err != nil {
			return err
		}
		return rideEvent(ctx, tx, now, &driverID, r, "ride.started", r.Status, nil)
	})
}

// NoShow is ride.no_show: 10 minutes after the later of arrival and pickup.
func (s *Service) NoShow(ctx context.Context, driverID, rideID uuid.UUID) (RideView, error) {
	return s.driverMove(ctx, driverID, rideID, "wrong_status", func(tx pgx.Tx, r rideRow, now time.Time) error {
		if r.Status != lifecycle.OnTheWay || r.PickedUpAt != nil {
			return wrongStatus(r)
		}
		if opens := lifecycle.NoShowOpensAt(r.PickupAt, r.ArrivedAt); now.Before(opens) {
			return &Error{Status: 409, Code: "too_soon", Message: "Wait 10 minutes before marking a no-show.",
				Extra: map[string]any{"opens_at": opens}}
		}
		if err := changeStatus(ctx, tx, r, lifecycle.RiderNoShow, now, "no_show_at = $4"); err != nil {
			return err
		}
		if err := rideEvent(ctx, tx, now, &driverID, r, "ride.rider_no_show", lifecycle.RiderNoShow, nil); err != nil {
			return err
		}
		c, err := s.rideContext(ctx, tx, r)
		if err != nil {
			return err
		}
		return s.push(ctx, tx, now, r.RiderID, "ride_no_show", "Ride missed",
			fmt.Sprintf("%s waited 10 minutes and had to leave. This ride is marked as missed.", c.driverFirst()), rideData(r))
	})
}

// Complete is ride.complete ("End ride"), only after Start ride.
func (s *Service) Complete(ctx context.Context, driverID, rideID uuid.UUID) (RideView, error) {
	return s.driverMove(ctx, driverID, rideID, "wrong_status", func(tx pgx.Tx, r rideRow, now time.Time) error {
		if r.Status != lifecycle.OnTheWay {
			return wrongStatus(r)
		}
		if r.PickedUpAt == nil {
			return errf(409, "not_started", "Enter the rider's PIN and start the ride first.")
		}
		if err := changeStatus(ctx, tx, r, lifecycle.Completed, now, "completed_at = $4"); err != nil {
			return err
		}
		if err := rideEvent(ctx, tx, now, &driverID, r, "ride.completed", lifecycle.Completed, nil); err != nil {
			return err
		}
		c, err := s.rideContext(ctx, tx, r)
		if err != nil {
			return err
		}
		return s.push(ctx, tx, now, r.RiderID, "ride_completed", "You've arrived",
			fmt.Sprintf("You've arrived. Pay %s ₹%d by UPI or cash.", c.driverFirst(), r.Fare), rideData(r))
	})
}

// DriverPaid is ride.driver_paid: upi, cash or not_paid; changeable until
// midnight (local) at the end of the ride's day.
func (s *Service) DriverPaid(ctx context.Context, driverID, rideID uuid.UUID, mark string) (RideView, error) {
	mark = normaliseMark(mark)
	if !slices.Contains(lifecycle.DriverPaidMarks, mark) {
		return RideView{}, errf(422, "bad_mark", "Choose upi, cash or not_paid.")
	}
	return s.driverMove(ctx, driverID, rideID, "wrong_status", func(tx pgx.Tx, r rideRow, now time.Time) error {
		if r.Status != lifecycle.Completed {
			return errf(409, "not_completed", "The ride isn't complete yet.")
		}
		tz, err := s.rideZone(ctx, tx, r)
		if err != nil {
			return err
		}
		if closes := lifecycle.PaymentMarksCloseAt(r.LocalDate, s.loc(tz)); !now.Before(closes) {
			return &Error{Status: 409, Code: "too_late", Message: "Payment marks can be changed until midnight.",
				Extra: map[string]any{"closed_at": closes}}
		}
		if _, err := tx.Exec(ctx, "update rides set driver_paid_mark = $2, driver_paid_at = $3, version = version + 1, updated_at = $3 where id = $1",
			r.ID, mark, now); err != nil {
			return err
		}
		return rideEvent(ctx, tx, now, &driverID, r, "ride.driver_paid", r.Status, map[string]any{"mark": mark, "old_mark": r.DriverPaidMark})
	})
}

// FlagRider is ride.flag_rider.
func (s *Service) FlagRider(ctx context.Context, driverID, rideID uuid.UUID, tags []string) (RideView, error) {
	clean := []string{}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t != "" && !slices.Contains(clean, t) {
			clean = append(clean, t)
		}
	}
	return s.driverMove(ctx, driverID, rideID, "wrong_status", func(tx pgx.Tx, r rideRow, now time.Time) error {
		if r.Status != lifecycle.Completed {
			return errf(409, "not_completed", "The ride isn't complete yet.")
		}
		if _, err := tx.Exec(ctx, "update rides set driver_flags = $2, flagged_at = $3, version = version + 1, updated_at = $3 where id = $1",
			r.ID, clean, now); err != nil {
			return err
		}
		return rideEvent(ctx, tx, now, &driverID, r, "ride.rider_flagged", r.Status, map[string]any{"tags": clean})
	})
}

// GiveUp is ride.give_up ("Can't do this ride"): allowed while the ride is
// assigned and before pickup. The ride goes to needs_driver with no driver,
// a one-ride offer opens to every other eligible driver, and the rider is
// told; all in one transaction. Later rides stay with this driver.
func (s *Service) GiveUp(ctx context.Context, driverID, rideID uuid.UUID, reason string) (RideView, error) {
	reason = strings.TrimSpace(reason)
	return s.driverMove(ctx, driverID, rideID, "wrong_status", func(tx pgx.Tx, r rideRow, now time.Time) error {
		if r.Status != lifecycle.Assigned {
			return wrongStatus(r)
		}
		if !now.Before(r.PickupAt) {
			return errf(409, "too_late", "The pickup time has passed.")
		}
		var why *string
		if reason != "" {
			why = &reason
		}
		if err := changeStatus(ctx, tx, r, lifecycle.NeedsDriver, now,
			"driver_id = null, previous_driver_id = $5, give_up_reason = $6, reminder_sent_at = null", driverID, why); err != nil {
			return err
		}
		if err := rideEvent(ctx, tx, now, &driverID, r, "ride.given_up", lifecycle.NeedsDriver,
			map[string]any{"driver_id": driverID, "reason": reason}); err != nil {
			return err
		}
		c, err := s.rideContext(ctx, tx, r)
		if err != nil {
			return err
		}
		if err := s.push(ctx, tx, now, r.RiderID, "ride_needs_driver", "Finding another driver",
			fmt.Sprintf("%s can't make your %s ride on %s. We're finding you another driver.",
				c.driverFirst(), s.localClock(r.PickupAt, c.Series.TimeZone), dayWord(r.LocalDate)), rideData(r)); err != nil {
			return err
		}
		r.Status, r.DriverID, r.PreviousDriverID = lifecycle.NeedsDriver, nil, &driverID
		return s.openRideOffer(ctx, tx, r, c, 1, now)
	})
}

// Location is ride.location: the driver's position while on the way, one
// row per driver, overwritten. 409 not_on_the_way otherwise.
func (s *Service) Location(ctx context.Context, driverID, rideID uuid.UUID, lat, lng float64, accuracy *float64) error {
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 || (lat == 0 && lng == 0) {
		return errf(400, "invalid_request", "lat and lng are required")
	}
	now := s.Clock.Now()
	r, err := readRide(ctx, s.DB, rideID)
	if noRows(err) {
		return errNotFound("ride")
	}
	if err != nil {
		return err
	}
	if r.DriverID == nil || *r.DriverID != driverID {
		if r.PreviousDriverID != nil && *r.PreviousDriverID == driverID {
			return s.rideError(ctx, 409, "not_on_the_way", "This ride isn't on the way.", rideID, false)
		}
		return errf(403, "forbidden", "This isn't your ride.")
	}
	if r.Status != lifecycle.OnTheWay {
		return s.rideError(ctx, 409, "not_on_the_way", "This ride isn't on the way.", rideID, false)
	}
	_, err = s.DB.Exec(ctx, `insert into driver_locations (driver_id, ride_id, lat, lng, accuracy_m, recorded_at)
		values ($1, $2, $3, $4, $5, $6)
		on conflict (driver_id) do update set ride_id = excluded.ride_id, lat = excluded.lat, lng = excluded.lng,
			accuracy_m = excluded.accuracy_m, recorded_at = excluded.recorded_at`, driverID, rideID, lat, lng, accuracy, now)
	return err
}

func (s *Service) rideZone(ctx context.Context, q querier, r rideRow) (string, error) {
	if r.SeriesID == nil {
		return s.cfg().TimeZone, nil
	}
	var tz string
	err := q.QueryRow(ctx, "select time_zone from series where id = $1", *r.SeriesID).Scan(&tz)
	return tz, err
}

func normaliseMark(m string) string {
	m = strings.ToLower(strings.TrimSpace(m))
	m = strings.ReplaceAll(m, " ", "_")
	m = strings.ReplaceAll(m, "-", "_")
	switch m {
	case "notpaid", "not_paid_yet", "unpaid":
		return "not_paid"
	}
	return m
}

// ---------------------------------------------------------------- rider

// riderRide reads a ride for its rider: 404 unknown, 403 another rider's.
func (s *Service) riderRide(ctx context.Context, q querier, riderID, rideID uuid.UUID, lock bool) (rideRow, error) {
	var r rideRow
	var err error
	if lock {
		r, err = lockRide(ctx, q, rideID)
	} else {
		r, err = readRide(ctx, q, rideID)
	}
	if noRows(err) {
		return r, errNotFound("ride")
	}
	if err != nil {
		return r, err
	}
	if r.RiderID != riderID {
		return r, errf(403, "forbidden", "This isn't your ride.")
	}
	return r, nil
}

// riderMove runs [f] on the rider's ride. A ride with a driver is locked
// driver first, then ride (read, lock, re-check).
func (s *Service) riderMove(ctx context.Context, riderID, rideID uuid.UUID, f func(tx pgx.Tx, r rideRow, now time.Time) error) (RideView, error) {
	now := s.Clock.Now()
	var v RideView
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		r, err := s.riderRide(ctx, tx, riderID, rideID, false)
		if err != nil {
			return err
		}
		if r.DriverID != nil {
			if _, err := lockDriver(ctx, tx, *r.DriverID); err != nil {
				return err
			}
		}
		locked, err := lockRide(ctx, tx, rideID)
		if err != nil {
			return err
		}
		if (locked.DriverID == nil) != (r.DriverID == nil) || (locked.DriverID != nil && *locked.DriverID != *r.DriverID) {
			return errf(409, "wrong_status", "This ride just changed. Try again.")
		}
		if err := f(tx, locked, now); err != nil {
			return err
		}
		locked, err = readRide(ctx, tx, rideID)
		if err != nil {
			return err
		}
		v, err = s.rideView(ctx, tx, locked, true)
		return err
	})
	return v, s.withRideState(ctx, err, rideID, true)
}

// Ride is ride.get for the rider (or the ride's driver).
func (s *Service) Ride(ctx context.Context, userID, rideID uuid.UUID, role string) (RideView, error) {
	r, err := readRide(ctx, s.DB, rideID)
	if noRows(err) {
		return RideView{}, errNotFound("ride")
	}
	if err != nil {
		return RideView{}, err
	}
	switch role {
	case "rider":
		if r.RiderID != userID {
			return RideView{}, errf(403, "forbidden", "This isn't your ride.")
		}
		return s.rideView(ctx, s.DB, r, true)
	case "driver":
		mine := r.DriverID != nil && *r.DriverID == userID
		if !mine && (r.PreviousDriverID == nil || *r.PreviousDriverID != userID) {
			return RideView{}, errf(403, "forbidden", "This isn't your ride.")
		}
		v, err := s.rideView(ctx, s.DB, r, false)
		v.Mine = &mine
		return v, err
	}
	return RideView{}, errf(403, "forbidden", "This isn't your ride.")
}

// RunningLate is ride.running_late: one push to the driver, once per ride.
func (s *Service) RunningLate(ctx context.Context, riderID, rideID uuid.UUID) error {
	_, err := s.riderMove(ctx, riderID, rideID, func(tx pgx.Tx, r rideRow, now time.Time) error {
		if r.LateNoticeAt != nil {
			return errf(409, "already_sent", "You've already told your driver.")
		}
		if r.Status != lifecycle.OnTheWay || r.PickedUpAt != nil || r.DriverID == nil {
			return wrongStatus(r)
		}
		if _, err := tx.Exec(ctx, "update rides set late_notice_at = $2, version = version + 1, updated_at = $2 where id = $1", r.ID, now); err != nil {
			return err
		}
		if err := rideEvent(ctx, tx, now, &riderID, r, "ride.rider_running_late", r.Status, nil); err != nil {
			return err
		}
		c, err := s.rideContext(ctx, tx, r)
		if err != nil {
			return err
		}
		return s.push(ctx, tx, now, *r.DriverID, "ride_rider_late", "Rider running late",
			fmt.Sprintf("%s is running about 5 minutes late.", c.riderFirst()), rideData(r))
	})
	return err
}

// RiderPaid is ride.rider_paid: upi or cash.
func (s *Service) RiderPaid(ctx context.Context, riderID, rideID uuid.UUID, mark string) (RideView, error) {
	mark = normaliseMark(mark)
	if !slices.Contains(lifecycle.RiderPaidMarks, mark) {
		return RideView{}, errf(422, "bad_mark", "Choose upi or cash.")
	}
	return s.riderMove(ctx, riderID, rideID, func(tx pgx.Tx, r rideRow, now time.Time) error {
		if r.Status != lifecycle.Completed {
			return errf(409, "not_completed", "The ride isn't complete yet.")
		}
		if _, err := tx.Exec(ctx, "update rides set rider_paid_mark = $2, rider_paid_at = $3, version = version + 1, updated_at = $3 where id = $1",
			r.ID, mark, now); err != nil {
			return err
		}
		return rideEvent(ctx, tx, now, &riderID, r, "ride.rider_paid", r.Status, map[string]any{"mark": mark})
	})
}

// Rate is ride.rate: 1–5 stars, tags and a note, once.
func (s *Service) Rate(ctx context.Context, riderID, rideID uuid.UUID, stars int, tags []string, note string) (RideView, error) {
	if stars < 1 || stars > 5 {
		return RideView{}, errf(422, "bad_stars", "Stars are 1 to 5.")
	}
	clean := []string{}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t != "" && !slices.Contains(clean, t) {
			clean = append(clean, t)
		}
	}
	note = strings.TrimSpace(note)
	return s.riderMove(ctx, riderID, rideID, func(tx pgx.Tx, r rideRow, now time.Time) error {
		if r.Status != lifecycle.Completed {
			return errf(409, "not_completed", "The ride isn't complete yet.")
		}
		if r.RiderStars != nil {
			return errf(409, "already_rated", "You've already rated this ride.")
		}
		var n *string
		if note != "" {
			n = &note
		}
		if _, err := tx.Exec(ctx, `update rides set rider_stars = $2, rider_tags = $3, rider_note = $4, rated_at = $5,
				version = version + 1, updated_at = $5 where id = $1`, r.ID, stars, clean, n, now); err != nil {
			return err
		}
		if err := rideEvent(ctx, tx, now, &riderID, r, "ride.rated", r.Status, map[string]any{"stars": stars}); err != nil {
			return err
		}
		if r.DriverID != nil {
			// The driver's rating is the mean of their rated rides.
			_, err := tx.Exec(ctx, `update drivers set rating = coalesce((select round(avg(rider_stars)::numeric, 1)
				from rides where driver_id = $1 and rider_stars is not null), rating) where user_id = $1`, *r.DriverID)
			return err
		}
		return nil
	})
}

// LiveView is ride.live: the ride's state and, while it is on the way, the
// driver's position, its age and an ETA to pickup (or, once picked up, to
// the drop-off).
type LiveView struct {
	RideID          uuid.UUID     `json:"ride_id"`
	Status          string        `json:"status"`
	StateLabel      string        `json:"state_label"`
	PickupAt        time.Time     `json:"pickup_at"`
	SetsOffBy       time.Time     `json:"sets_off_by"`
	OnTheWayAt      *time.Time    `json:"on_the_way_at"`
	ArrivedAt       *time.Time    `json:"arrived_at"`
	PickedUpAt      *time.Time    `json:"picked_up_at"`
	CompletedAt     *time.Time    `json:"completed_at"`
	Position        *LivePosition `json:"position"`
	DriverPosition  *LivePosition `json:"driver_position"`
	PositionAgeSecs *int          `json:"position_age_seconds"`
	Stale           bool          `json:"stale"`
	EtaMinutes      *int          `json:"eta_minutes"`
	ArrivalAt       *time.Time    `json:"arrival_at"`
	Pin             *string       `json:"pin"`
	RunningLate     bool          `json:"running_late"`
	Driver          *RideDriver   `json:"driver"`
	Pickup          Place         `json:"pickup"`
	Drop            Place         `json:"drop"`
	Ride            RideView      `json:"ride"`
}

// LivePosition is the driver's last position.
type LivePosition struct {
	Lat        float64   `json:"lat"`
	Lng        float64   `json:"lng"`
	AccuracyM  *float64  `json:"accuracy_m"`
	RecordedAt time.Time `json:"recorded_at"`
	AgeSeconds int       `json:"age_seconds"`
}

// Live is ride.live, for the ride's rider only (403 otherwise).
func (s *Service) Live(ctx context.Context, riderID, rideID uuid.UUID) (LiveView, error) {
	now := s.Clock.Now()
	r, err := s.riderRide(ctx, s.DB, riderID, rideID, false)
	if err != nil {
		return LiveView{}, err
	}
	c, err := s.rideContext(ctx, s.DB, r)
	if err != nil {
		return LiveView{}, err
	}
	rv := s.buildRideView(r, c, true)
	v := LiveView{RideID: r.ID, Status: r.Status, StateLabel: rv.StateLabel, PickupAt: r.PickupAt, SetsOffBy: rv.SetsOffBy,
		OnTheWayAt: r.OnTheWayAt, ArrivedAt: r.ArrivedAt, PickedUpAt: r.PickedUpAt, CompletedAt: r.CompletedAt,
		Pin: rv.Pin, RunningLate: r.LateNoticeAt != nil, Driver: rv.Driver, Pickup: c.From, Drop: c.To, Ride: rv}
	if r.Status != lifecycle.OnTheWay || r.DriverID == nil {
		return v, nil
	}
	var p LivePosition
	var forRide *uuid.UUID
	err = s.DB.QueryRow(ctx, "select lat, lng, accuracy_m, recorded_at, ride_id from driver_locations where driver_id = $1", *r.DriverID).
		Scan(&p.Lat, &p.Lng, &p.AccuracyM, &p.RecordedAt, &forRide)
	if noRows(err) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	// Only a position sent for this ride, since it went on the way.
	if forRide == nil || *forRide != r.ID || (r.OnTheWayAt != nil && p.RecordedAt.Before(*r.OnTheWayAt)) {
		return v, nil
	}
	age := int(now.Sub(p.RecordedAt).Seconds())
	if age < 0 {
		age = 0
	}
	p.AgeSeconds = age
	v.Position, v.DriverPosition, v.PositionAgeSecs = &p, &p, &age
	v.Stale = now.Sub(p.RecordedAt) > lifecycle.PositionStaleAfter
	target := c.From
	if r.PickedUpAt != nil {
		target = c.To
	}
	speed, err := s.speed(ctx, s.DB, c.Series.VehicleType)
	if err != nil {
		return v, err
	}
	eta := lifecycle.EtaMinutes(lifecycle.HaversineKm(p.Lat, p.Lng, target.Lat, target.Lng), speed)
	v.EtaMinutes = &eta
	v.ArrivalAt = ptr(now.Add(time.Duration(eta) * time.Minute))
	return v, nil
}

// NextRides is rides.next: the next ride of each of the rider's active (or
// paused) series, with its driver and state. "Next" is the earliest ride
// on the way, or (but cancelled) whose pickup hasn't passed, or that ended
// in the last hour (so the pay-and-rate or no-show card can show).
func (s *Service) NextRides(ctx context.Context, riderID uuid.UUID) ([]RideView, error) {
	now := s.Clock.Now()
	rows, err := s.DB.Query(ctx, `select distinct on (r.series_id) r.id
		from rides r join series se on se.id = r.series_id
		where r.rider_id = $1 and se.status in ('active', 'paused')
		  and (r.status = 'on_the_way'
		       or (r.status <> 'cancelled' and r.pickup_at > $2)
		       or (r.status in ('completed', 'rider_no_show', 'failed') and r.pickup_at > $3
		           and coalesce(r.completed_at, r.no_show_at, r.failed_at) > $4))
		order by r.series_id, r.pickup_at, r.id`, riderID, now, now.Add(-6*time.Hour), now.Add(-time.Hour))
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	out := []RideView{}
	for _, id := range ids {
		r, err := readRide(ctx, s.DB, id)
		if err != nil {
			return nil, err
		}
		v, err := s.rideView(ctx, s.DB, r, true)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b RideView) int { return a.PickupAt.Compare(b.PickupAt) })
	return out, nil
}

// RiderRides lists the rider's rides from [from] for [days] days (the
// Schedule screen; paying and rating later).
func (s *Service) RiderRides(ctx context.Context, riderID uuid.UUID, from *time.Time, days int) ([]RideView, error) {
	now := s.Clock.Now()
	start := s.today(now)
	if from != nil {
		start = *from
	}
	if days <= 0 {
		days = 14
	}
	days = min(days, 31)
	rows, err := s.DB.Query(ctx, `select id from rides where rider_id = $1 and local_date between $2 and $3 order by pickup_at, id`,
		riderID, start, scheduling.AddDays(start, days-1))
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	out := []RideView{}
	for _, id := range ids {
		r, err := readRide(ctx, s.DB, id)
		if err != nil {
			return nil, err
		}
		v, err := s.rideView(ctx, s.DB, r, true)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
