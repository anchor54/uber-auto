package offers

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"commute-backend/internal/scheduling"
)

// rideWindow is the dates rides exist for: from the start date (or today
// once the series has started) up to today + 14, and at least the first 14
// days counted from the start date (R1's win creates those). Ruling: end =
// max(start + 13, today + 14).
func rideWindow(start, today time.Time) (from, to time.Time) {
	from = start
	if today.After(from) {
		from = today
	}
	to = scheduling.AddDays(today, scheduling.HorizonDays)
	if first := scheduling.AddDays(start, scheduling.HorizonDays-1); first.After(to) {
		to = first
	}
	return from, to
}

// RideDatesFor lists the dates a slot on [weekday] gets rides for: the pure
// rule behind generateRides.
func RideDatesFor(weekday int, start, today time.Time) []time.Time {
	from, to := rideWindow(start, today)
	return scheduling.RideDates(weekday, from, to)
}

// generateRides creates the missing rides of an active series for its ride
// window: assigned when the slot has a driver, else needs_driver. Rides
// whose pickup has passed are not created. With [strict], a ride refused by
// the overlap constraint is an error (errRideOverlap, a clash at the pick);
// otherwise it is created as needs_driver.
func (s *Service) generateRides(ctx context.Context, tx pgx.Tx, seriesID uuid.UUID, now time.Time, strict bool) (int, error) {
	se, err := readSeries(ctx, tx, seriesID)
	if err != nil {
		return 0, err
	}
	if se.Status != "active" {
		return 0, nil
	}
	slots, err := readSlots(ctx, tx, seriesID)
	if err != nil {
		return 0, err
	}
	loc := s.loc(se.TimeZone)
	made := 0
	for _, sl := range slots {
		if !sl.Active {
			continue
		}
		for _, date := range RideDatesFor(sl.Weekday, se.StartDate, scheduling.DateOf(now, loc)) {
			pickup := scheduling.LocalInstant(date, sl.Pickup, loc)
			if !pickup.After(now) {
				continue
			}
			drop := pickup.Add(time.Duration(se.TravelMinutes) * time.Minute)
			status := "needs_driver"
			if sl.DriverID != nil {
				status = "assigned"
			}
			id, err := insertRide(ctx, tx, se, sl, date, pickup, drop, status, sl.DriverID, now)
			if isExclusionViolation(err) {
				if strict {
					return made, errRideOverlap
				}
				s.log().Warn("generate_rides: overlap; ride needs a driver", "slot", sl.ID, "date", scheduling.FormatDate(date))
				status = "needs_driver"
				id, err = insertRide(ctx, tx, se, sl, date, pickup, drop, status, nil, now)
			}
			if err != nil {
				return made, err
			}
			if id == uuid.Nil {
				continue
			}
			made++
			if status == "needs_driver" {
				// A ride without a driver opens its one-ride offer in the same transaction.
				r, err := lockRide(ctx, tx, id)
				if err != nil {
					return made, err
				}
				c, err := s.rideContext(ctx, tx, r)
				if err != nil {
					return made, err
				}
				if err := s.openRideOffer(ctx, tx, r, c, 1, now); err != nil {
					return made, err
				}
			}
		}
	}
	return made, nil
}

// insertRide inserts one ride in a savepoint, so a refused insert leaves the
// transaction usable. Returns uuid.Nil when the ride already exists.
func insertRide(ctx context.Context, tx pgx.Tx, se seriesRow, sl slotRow, date, pickup, drop time.Time, status string, driver *uuid.UUID, now time.Time) (uuid.UUID, error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err = sp.QueryRow(ctx, `insert into rides (series_id, slot_id, rider_id, driver_id, local_date, pickup_at, drop_at, busy_until,
			status, fare_snapshot, created_at, updated_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)
		on conflict (slot_id, local_date) do nothing returning id`,
		se.ID, sl.ID, se.RiderID, driver, date, pickup, drop, drop.Add(scheduling.Buffer), status, se.SuggestedFare, now).Scan(&id)
	if noRows(err) {
		return uuid.Nil, sp.Commit(ctx)
	}
	if err != nil {
		sp.Rollback(ctx)
		return uuid.Nil, err
	}
	if err := event(ctx, sp, now, nil, "ride", id, "ride.created", map[string]any{"status": status, "series_id": se.ID}); err != nil {
		sp.Rollback(ctx)
		return uuid.Nil, err
	}
	return id, sp.Commit(ctx)
}

// GenerateDueRides is the sweep's generate_rides step: every active series
// keeps its rides created 14 days ahead, one transaction per series.
func (s *Service) GenerateDueRides(ctx context.Context) (int, error) {
	rows, err := s.DB.Query(ctx, "select id from series where status = 'active' order by id")
	if err != nil {
		return 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, err
	}
	total := 0
	for _, id := range ids {
		now := s.Clock.Now()
		err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
			n, err := s.generateRides(ctx, tx, id, now, false)
			total += n
			return err
		})
		if err != nil {
			s.log().Error("generate_rides", "series", id, "err", err)
		}
	}
	return total, nil
}

// ActivateWithDriver assigns every slot of a series to a driver directly
// and creates its rides, with no offer (seed data and the simulator).
func (s *Service) ActivateWithDriver(ctx context.Context, seriesID, driverID uuid.UUID) error {
	now := s.Clock.Now()
	return pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := lockDriver(ctx, tx, driverID); err != nil {
			return err
		}
		se, err := lockSeries(ctx, tx, seriesID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "update offers set status = 'closed', outcome = 'assigned', closed_at = $2 where series_id = $1 and status <> 'closed'", seriesID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "update series_slots set driver_id = $2 where series_id = $1 and active", seriesID, driverID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "update series set status = 'active', version = version + 1, updated_at = $2 where id = $1", seriesID, now); err != nil {
			return err
		}
		if err := event(ctx, tx, now, nil, "series", seriesID, "series.assigned",
			map[string]any{"old_status": se.Status, "new_status": "active", "driver_id": driverID, "direct": true}); err != nil {
			return err
		}
		_, err = s.generateRides(ctx, tx, seriesID, now, true)
		return err
	})
}
