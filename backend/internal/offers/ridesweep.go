package offers

// The sweeper's ride tasks (R2 + D3). Each derives what is due from
// pickup_at offsets and the ride's marker columns (reminder_sent_at, the
// status itself), so a missed run is caught up by the next; each has a
// too-late limit. No deadlines table.

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"commute-backend/internal/lifecycle"
	"commute-backend/internal/scheduling"
)

// RideCounts is what one run's ride tasks did.
type RideCounts struct {
	Reminders     int `json:"evening_reminder"`
	RemindersLate int `json:"evening_reminder_too_late"`
	HandedOver    int `json:"on_my_way_check"`
	MissedOutage  int `json:"missed_by_outage"`
	Unfilled      int `json:"failed_no_driver"`
	StaleClosed   int `json:"close_stale_rides"`
}

// EveningReminders is evening_reminder: from 20:00 local the day before, one
// push per driver listing tomorrow's assigned rides; reminder_sent_at is set
// on each. From 23:00 the push is skipped but the markers are still set.
func (s *Service) EveningReminders(ctx context.Context) (sent, late int, err error) {
	now := s.Clock.Now()
	rows, err := s.DB.Query(ctx, `select r.id, r.driver_id, r.local_date, coalesce(se.time_zone, '')
		from rides r left join series se on se.id = r.series_id
		where r.status = 'assigned' and r.reminder_sent_at is null and r.driver_id is not null
		  and r.pickup_at <= $1
		order by r.driver_id, r.pickup_at`, now.Add(30*time.Hour))
	if err != nil {
		return 0, 0, err
	}
	type key struct {
		driver uuid.UUID
		date   string
	}
	groups := map[key][]uuid.UUID{}
	tooLate := map[key]bool{}
	var keys []key
	for rows.Next() {
		var id, driver uuid.UUID
		var date time.Time
		var tz string
		if err := rows.Scan(&id, &driver, &date, &tz); err != nil {
			rows.Close()
			return 0, 0, err
		}
		loc := s.loc(tz)
		if now.Before(lifecycle.ReminderDueAt(date, loc)) {
			continue
		}
		k := key{driver, scheduling.FormatDate(date)}
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], id)
		tooLate[k] = !now.Before(lifecycle.ReminderTooLateAt(date, loc))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	for _, k := range keys {
		err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `update rides set reminder_sent_at = $2
				where id = any($1) and reminder_sent_at is null and status = 'assigned' and driver_id = $3
				returning id`, groups[k], now, k.driver)
			if err != nil {
				return err
			}
			ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
			if err != nil || len(ids) == 0 {
				return err
			}
			if tooLate[k] {
				late++
				return nil
			}
			var rides []rideRow
			for _, id := range ids {
				r, err := readRide(ctx, tx, id)
				if err != nil {
					return err
				}
				rides = append(rides, r)
			}
			sort.Slice(rides, func(i, j int) bool { return rides[i].PickupAt.Before(rides[j].PickupAt) })
			first := rides[0]
			c, err := s.rideContext(ctx, tx, first)
			if err != nil {
				return err
			}
			word := "rides"
			if len(rides) == 1 {
				word = "ride"
			}
			body := fmt.Sprintf("Tomorrow: %d %s. First at %s, %s, %s → %s.", len(rides), word,
				s.localClock(first.PickupAt, c.Series.TimeZone), c.riderFirst(), label(c.From.Label, "Pickup"), label(c.To.Label, "Drop-off"))
			ordered := make([]uuid.UUID, len(rides))
			for i, r := range rides {
				ordered[i] = r.ID
			}
			sent++
			return s.push(ctx, tx, now, k.driver, "driver_reminder", "Tomorrow's rides", body,
				map[string]string{"ride_ids": joinIDs(ordered), "date": k.date})
		})
		if err != nil {
			return sent, late, err
		}
	}
	return sent, late, nil
}

// OnMyWayChecks is on_my_way_check: an assigned ride whose driver hasn't
// tapped On my way by pickup − 15 min goes to needs_driver, the rider is
// told and a one-ride offer opens. Too late (at or after pickup, after an
// outage): failed, missed_by_outage, no offer, one admin alert per run, not
// counted against the driver.
func (s *Service) OnMyWayChecks(ctx context.Context) (handed, missed int, err error) {
	now := s.Clock.Now()
	rows, err := s.DB.Query(ctx, `select id, driver_id from rides
		where status = 'assigned' and pickup_at <= $1 order by pickup_at, id`, now.Add(lifecycle.CheckBefore))
	if err != nil {
		return 0, 0, err
	}
	type due struct{ ride, driver uuid.UUID }
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.ride, &d.driver); err != nil {
			rows.Close()
			return 0, 0, err
		}
		list = append(list, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	var missedIDs []uuid.UUID
	for _, d := range list {
		err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
			// Driver first, then the ride; re-check after the locks.
			if _, err := lockDriver(ctx, tx, d.driver); err != nil {
				return err
			}
			r, err := lockRide(ctx, tx, d.ride)
			if err != nil {
				return err
			}
			if r.Status != lifecycle.Assigned || r.DriverID == nil || *r.DriverID != d.driver ||
				now.Before(lifecycle.CheckAt(r.PickupAt)) {
				return nil
			}
			c, err := s.rideContext(ctx, tx, r)
			if err != nil {
				return err
			}
			if lifecycle.CheckIsTooLate(r.PickupAt, now) {
				if err := s.failRide(ctx, tx, r, c, "missed_by_outage", now); err != nil {
					return err
				}
				missedIDs = append(missedIDs, r.ID)
				return nil
			}
			if err := changeStatus(ctx, tx, r, lifecycle.NeedsDriver, now,
				"driver_id = null, previous_driver_id = $5", d.driver); err != nil {
				return err
			}
			if err := rideEvent(ctx, tx, now, nil, r, "ride.handed_over", lifecycle.NeedsDriver,
				map[string]any{"driver_id": d.driver, "reason": "no_on_my_way"}); err != nil {
				return err
			}
			if err := s.push(ctx, tx, now, r.RiderID, "ride_needs_driver", "Finding another driver",
				fmt.Sprintf("%s hasn't started yet. We're finding you another driver.", c.driverFirst()), rideData(r)); err != nil {
				return err
			}
			handed++
			r.Status, r.DriverID, r.PreviousDriverID = lifecycle.NeedsDriver, nil, &d.driver
			return s.openRideOffer(ctx, tx, r, c, 1, now)
		})
		if err != nil {
			return handed, len(missedIDs), err
		}
	}
	if len(missedIDs) > 0 {
		err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
			word := "rides"
			if len(missedIDs) == 1 {
				word = "ride"
			}
			return s.pushAdmins(ctx, tx, now, "admin_rides_missed", "Rides missed",
				fmt.Sprintf("%d %s missed by outage", len(missedIDs), word), map[string]string{"ride_ids": joinIDs(missedIDs)})
		})
		if err != nil {
			return handed, len(missedIDs), err
		}
	}
	return handed, len(missedIDs), nil
}

// FailUnfilled is the too-late limit of a ride that needs a driver: at
// pickup with no live offer still being picked, it fails (no_driver).
func (s *Service) FailUnfilled(ctx context.Context) (int, error) {
	now := s.Clock.Now()
	rows, err := s.DB.Query(ctx, `select r.id from rides r
		where r.status = 'needs_driver' and r.pickup_at <= $1
		  and not exists (select 1 from offers o where o.ride_id = r.id and o.kind = 'ride' and o.status in ('open', 'closing'))
		order by r.pickup_at`, now)
	if err != nil {
		return 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
			r, err := lockRide(ctx, tx, id)
			if err != nil || r.Status != lifecycle.NeedsDriver {
				return err
			}
			c, err := s.rideContext(ctx, tx, r)
			if err != nil {
				return err
			}
			n++
			return s.failRide(ctx, tx, r, c, "no_driver", now)
		})
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// CloseStaleRides is close_stale_rides: a ride still on the way 4 hours
// after pickup is completed (close_reason auto_closed) and the admins told.
func (s *Service) CloseStaleRides(ctx context.Context) (int, error) {
	now := s.Clock.Now()
	rows, err := s.DB.Query(ctx, `select id, driver_id from rides where status = 'on_the_way' and pickup_at <= $1 order by pickup_at`,
		now.Add(-lifecycle.StaleAfter))
	if err != nil {
		return 0, err
	}
	type due struct{ ride, driver uuid.UUID }
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.ride, &d.driver); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, d := range list {
		err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
			if _, err := lockDriver(ctx, tx, d.driver); err != nil {
				return err
			}
			r, err := lockRide(ctx, tx, d.ride)
			if err != nil {
				return err
			}
			if r.Status != lifecycle.OnTheWay || now.Before(lifecycle.StaleAt(r.PickupAt)) {
				return nil
			}
			if err := changeStatus(ctx, tx, r, lifecycle.Completed, now, "completed_at = $4, close_reason = 'auto_closed'"); err != nil {
				return err
			}
			if err := rideEvent(ctx, tx, now, nil, r, "ride.completed", lifecycle.Completed, map[string]any{"reason": "auto_closed"}); err != nil {
				return err
			}
			n++
			return s.pushAdmins(ctx, tx, now, "admin_ride_auto_closed", "Ride closed",
				fmt.Sprintf("Ride %s closed automatically", r.ID), rideData(r))
		})
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// RunRideTasks runs the ride tasks in order: the On my way check (and its
// outage limit), unfilled rides, reminders, stale rides.
func (s *Service) RunRideTasks(ctx context.Context) (RideCounts, error) {
	var c RideCounts
	var firstErr error
	note := func(step string, err error) {
		if err != nil {
			s.log().Error("ride task failed", "step", step, "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	var err error
	c.HandedOver, c.MissedOutage, err = s.OnMyWayChecks(ctx)
	note("on_my_way_check", err)
	c.Unfilled, err = s.FailUnfilled(ctx)
	note("fail_unfilled", err)
	c.Reminders, c.RemindersLate, err = s.EveningReminders(ctx)
	note("evening_reminder", err)
	c.StaleClosed, err = s.CloseStaleRides(ctx)
	note("close_stale_rides", err)
	return c, firstErr
}

// ---------------------------------------------------------------- the run

// Run is rides.schedule (D3.1 Today): the driver's rides from [from] for
// [days] days (max 14) in pickup order; only rides the driver holds.
type Run struct {
	From          string     `json:"from"`
	To            string     `json:"to"`
	Days          int        `json:"days"`
	PlanPaidTill  *string    `json:"plan_paid_till"`
	RideCount     int        `json:"ride_count"`
	RegularRiders int        `json:"regular_riders"`
	Rides         []RideView `json:"rides"`
}

// DriverRun is rides.schedule.
func (s *Service) DriverRun(ctx context.Context, driverID uuid.UUID, from *time.Time, days int) (Run, error) {
	now := s.Clock.Now()
	start := s.today(now)
	if from != nil {
		start = *from
	}
	if days <= 0 {
		days = 1
	}
	days = min(days, scheduling.HorizonDays)
	end := scheduling.AddDays(start, days-1)
	run := Run{From: scheduling.FormatDate(start), To: scheduling.FormatDate(end), Days: days, Rides: []RideView{}}
	var until *time.Time
	if err := s.DB.QueryRow(ctx, "select subscription_until from drivers where user_id = $1", driverID).Scan(&until); err != nil {
		if noRows(err) {
			return run, errf(403, "not_a_driver", "This needs a driver account.")
		}
		return run, err
	}
	if until != nil {
		run.PlanPaidTill = ptr(scheduling.FormatDate(*until))
	}
	rows, err := s.DB.Query(ctx, `select id from rides
		where driver_id = $1 and local_date between $2 and $3 and status <> 'cancelled'
		order by pickup_at, id`, driverID, start, end)
	if err != nil {
		return run, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return run, err
	}
	riders := map[uuid.UUID]bool{}
	for _, id := range ids {
		r, err := readRide(ctx, s.DB, id)
		if err != nil {
			return run, err
		}
		v, err := s.rideView(ctx, s.DB, r, false)
		if err != nil {
			return run, err
		}
		run.Rides = append(run.Rides, v)
		riders[r.RiderID] = true
	}
	run.RideCount, run.RegularRiders = len(run.Rides), len(riders)
	return run, nil
}
