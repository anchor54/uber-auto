package offers

// One-ride offers (kind 'ride'): when a ride needs a driver (its driver gave
// it up, missed the On my way check, or a slot has none at generation) it is
// offered to every eligible driver of its vehicle type, except the driver
// who had it, for a 5-minute window. At the close the interested drivers
// are tried in shuffled order, each under a lock (driver, then ride, then
// offer), first one without a clash wins. R4 adds what the rider sees
// meanwhile; this is the minimal pick that reuses the series pick.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"commute-backend/internal/lifecycle"
	"commute-backend/internal/scheduling"
	"commute-backend/internal/travel"
)

// openRideOffer opens round [round] of a one-ride offer for a needs_driver
// ride (locked by the caller). Recipients: live, paid drivers of the ride's
// vehicle type and city, minus the ride's previous driver, minus drivers
// who said "Not for me" to an earlier round of this ride. With nobody to
// offer it to, or no time left before pickup, the ride fails (no_driver).
func (s *Service) openRideOffer(ctx context.Context, tx pgx.Tx, r rideRow, c rideCtx, round int, now time.Time) error {
	window := s.cfg().RideWindow
	if !now.Add(window).Before(r.PickupAt) && round > 1 {
		return s.failRide(ctx, tx, r, c, "no_driver", now)
	}
	exclude := uuid.Nil
	if r.PreviousDriverID != nil {
		exclude = *r.PreviousDriverID
	}
	rows, err := tx.Query(ctx, `select d.user_id from drivers d
		where d.status = 'live' and d.subscription_until >= $1 and d.vehicle_type = $2 and d.city = $3 and d.user_id <> $4
		  and not exists (select 1 from offer_recipients rr join offers o on o.id = rr.offer_id
		                  where o.ride_id = $5 and o.kind = 'ride' and rr.driver_id = d.user_id
		                    and (rr.dismissed_at is not null or rr.result in ('clashed', 'withdrew')))
		order by d.name, d.user_id`, s.today(now), c.Series.VehicleType, c.Series.City, exclude, r.ID)
	if err != nil {
		return err
	}
	recipients, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	if len(recipients) == 0 {
		return s.failRide(ctx, tx, r, c, "no_driver", now)
	}
	expires := now.Add(window)
	var offerID uuid.UUID
	err = tx.QueryRow(ctx, `insert into offers (kind, series_id, slot_id, ride_id, round, status, opened_at, expires_at, created_at)
		values ('ride', $1, $2, $3, $4, 'open', $5, $6, $5) returning id`, r.SeriesID, r.SlotID, r.ID, round, now, expires).Scan(&offerID)
	if err != nil {
		return err
	}
	tz := c.Series.TimeZone
	line := fmt.Sprintf("%s %s at %s · %s → %s · ₹%d", r.LocalDate.Format("Mon"), r.LocalDate.Format("2 Jan"),
		s.localClock(r.PickupAt, tz), label(c.From.Label, "Pickup"), label(c.To.Label, "Drop-off"), r.Fare)
	data := rideData(r)
	data["offer_id"] = offerID.String()
	title := "One ride"
	if round > 1 {
		title = "Still open"
	}
	for _, d := range recipients {
		if _, err := tx.Exec(ctx, `insert into offer_recipients (offer_id, driver_id, notified_at, created_at) values ($1, $2, $3, $3)`,
			offerID, d, now); err != nil {
			return err
		}
		if err := s.push(ctx, tx, now, d, "offer_ride", title, line, data); err != nil {
			return err
		}
	}
	return event(ctx, tx, now, nil, "offer", offerID, "offer.opened", map[string]any{
		"kind": "ride", "ride_id": r.ID, "round": round, "recipients": len(recipients), "expires_at": expires,
	})
}

// failRide ends a ride nobody could take: failed with [reason]; the rider
// is told to make their own way. missed_by_outage also alerts the admins
// (the caller, which may batch the alert). Any live offer is closed.
func (s *Service) failRide(ctx context.Context, tx pgx.Tx, r rideRow, c rideCtx, reason string, now time.Time) error {
	tag, err := tx.Exec(ctx, `update rides set status = 'failed', fail_reason = $2, failed_at = $3, driver_id = null,
			version = version + 1, updated_at = $3
		where id = $1 and status in ('assigned', 'needs_driver')`, r.ID, reason, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if err := rideEvent(ctx, tx, now, nil, r, "ride.failed", lifecycle.Failed, map[string]any{"reason": reason, "driver_id": r.DriverID}); err != nil {
		return err
	}
	if err := s.closeRideOffers(ctx, tx, r.ID, "no_fit", now); err != nil {
		return err
	}
	return s.push(ctx, tx, now, r.RiderID, "ride_failed", "No driver for this ride",
		fmt.Sprintf("We couldn't find a driver for your %s ride on %s. Please make your own way.",
			s.localClock(r.PickupAt, c.Series.TimeZone), dayWord(r.LocalDate)), rideData(r))
}

// closeRideOffers closes a ride's open or closing offers.
func (s *Service) closeRideOffers(ctx context.Context, tx pgx.Tx, rideID uuid.UUID, outcome string, now time.Time) error {
	rows, err := tx.Query(ctx, `update offers set status = 'closed', outcome = $2, closed_at = $3
		where ride_id = $1 and kind = 'ride' and status in ('open', 'closing') returning id`, rideID, outcome, now)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := event(ctx, tx, now, nil, "offer", id, "offer.closed", map[string]any{"outcome": outcome, "ride_id": rideID}); err != nil {
			return err
		}
	}
	return nil
}

// pickRide is the pick on a closing one-ride offer.
func (s *Service) pickRide(ctx context.Context, o offerRow) (bool, error) {
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
		res, err := s.tryRideDriver(ctx, o, c)
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
	return false, s.finishRideNoFit(ctx, o)
}

// tryRideDriver is one attempt: lock the driver, then the ride, then the
// offer; re-check; check eligibility and clashes; assign.
func (s *Service) tryRideDriver(ctx context.Context, o offerRow, cand Candidate) (attempt, error) {
	now := s.Clock.Now()
	var res attempt
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		d, err := lockDriver(ctx, tx, cand.DriverID)
		if err != nil {
			return err
		}
		r, err := lockRide(ctx, tx, *o.RideID)
		if err != nil {
			return err
		}
		off, err := lockOffer(ctx, tx, o.ID)
		if err != nil {
			return err
		}
		if off.Status != "closing" {
			res = stop
			return nil
		}
		if r.Status != lifecycle.NeedsDriver {
			res = stop
			return s.closeRideOffers(ctx, tx, r.ID, outcomeFor(r.Status), now)
		}
		if !now.Before(r.PickupAt) {
			// Too late to hand it to anyone: finishRideNoFit fails it.
			res = tryNext
			return nil
		}
		c, err := s.rideContext(ctx, tx, r)
		if err != nil {
			return err
		}
		if !d.eligibleFor(c.Series.VehicleType, c.Series.City, s.today(now)) {
			res = tryNext
			return s.recordResult(ctx, tx, o.ID, d.ID, "ineligible", now)
		}
		clash, why, err := s.rideClash(ctx, tx, d, r, c)
		if err != nil {
			return err
		}
		if clash {
			res = tryNext
			s.log().Info("ride pick: clash", "offer", o.ID, "driver", d.Name, "why", why)
			return s.recordResult(ctx, tx, o.ID, d.ID, "clashed", now)
		}
		if err := s.assignRide(ctx, tx, off, r, c, d, now); err != nil {
			return err
		}
		res = assigned
		return nil
	})
	if errors.Is(err, errRideOverlap) {
		s.log().Warn("ride pick: overlap constraint refused the ride", "offer", o.ID, "driver", cand.DriverID)
		return tryNext, pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
			return s.recordResult(ctx, tx, o.ID, cand.DriverID, "clashed", now)
		})
	}
	return res, err
}

func outcomeFor(status string) string {
	switch status {
	case lifecycle.Assigned, lifecycle.OnTheWay, lifecycle.Completed, lifecycle.RiderNoShow:
		return "assigned"
	case lifecycle.Skipped, lifecycle.Cancelled:
		return "cancelled"
	}
	return "no_fit"
}

// rideClash checks one ride against the driver's day off, rides and slots:
// each previous drop-off → next pickup must leave the travel time plus 10
// minutes.
func (s *Service) rideClash(ctx context.Context, tx pgx.Tx, d driverRow, r rideRow, c rideCtx) (bool, string, error) {
	if d.DayOff != nil && *d.DayOff == scheduling.ISOWeekday(r.LocalDate) {
		return true, "day off", nil
	}
	cand := scheduling.Trip{Pickup: r.PickupAt, Drop: r.DropAt, From: c.From.point(), To: c.To.point(), Label: "ride " + r.ID.String()}
	existing, err := s.driverTrips(ctx, tx, d.ID, uuid.Nil, r.PickupAt.Add(-24*time.Hour), r.PickupAt.Add(24*time.Hour))
	if err != nil {
		return false, "", err
	}
	speed, err := s.speed(ctx, tx, d.VehicleType)
	if err != nil {
		return false, "", err
	}
	tf := func(a, b travel.Point) int { return travel.MinutesBetween(a, b, speed) }
	if a, b, ok := scheduling.FirstClash(existing, []scheduling.Trip{cand}, tf); ok {
		return true, a.Label + " vs " + b.Label, nil
	}
	return false, "", nil
}

// assignRide gives the ride to the driver and tells everyone.
func (s *Service) assignRide(ctx context.Context, tx pgx.Tx, off offerRow, r rideRow, c rideCtx, d driverRow, now time.Time) error {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return err
	}
	_, err = sp.Exec(ctx, `update rides set status = 'assigned', driver_id = $2, reminder_sent_at = null, version = version + 1, updated_at = $3
		where id = $1 and status = 'needs_driver'`, r.ID, d.ID, now)
	if isExclusionViolation(err) {
		sp.Rollback(ctx)
		return errRideOverlap
	}
	if err != nil {
		sp.Rollback(ctx)
		return err
	}
	if err := sp.Commit(ctx); err != nil {
		return err
	}
	if err := rideEvent(ctx, tx, now, nil, r, "ride.assigned", lifecycle.Assigned, map[string]any{"driver_id": d.ID, "offer_id": off.ID}); err != nil {
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
	if err := event(ctx, tx, now, nil, "offer", off.ID, "offer.closed", map[string]any{"outcome": "assigned", "driver_id": d.ID, "ride_id": r.ID}); err != nil {
		return err
	}
	tz := c.Series.TimeZone
	at := s.localClock(r.PickupAt, tz)
	data := rideData(r)
	data["offer_id"] = off.ID.String()
	if err := s.push(ctx, tx, now, r.RiderID, "ride_driver_found", "New driver",
		fmt.Sprintf("%s will pick you up at %s on %s.", label(FirstName(d.Name), "A driver"), at, dayWord(r.LocalDate)), data); err != nil {
		return err
	}
	if err := s.push(ctx, tx, now, d.ID, "offer_won", "You got it",
		fmt.Sprintf("One ride: %s %s at %s, %s, %s → %s.", r.LocalDate.Format("Mon"), r.LocalDate.Format("2 Jan"), at,
			c.riderFirst(), label(c.From.Label, "Pickup"), label(c.To.Label, "Drop-off")), data); err != nil {
		return err
	}
	return s.tellLosers(ctx, tx, off.ID, d.ID, now, data)
}

// finishRideNoFit closes a round nobody fitted: the next round opens while
// there is time before pickup (up to MaxRounds), else the ride fails.
func (s *Service) finishRideNoFit(ctx context.Context, o offerRow) error {
	now := s.Clock.Now()
	var outage bool
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		r, err := lockRide(ctx, tx, *o.RideID)
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
		if r.Status != lifecycle.NeedsDriver {
			return s.closeRideOffers(ctx, tx, r.ID, outcomeFor(r.Status), now)
		}
		if _, err := tx.Exec(ctx, "update offers set status = 'closed', outcome = 'no_fit', closed_at = $2 where id = $1", off.ID, now); err != nil {
			return err
		}
		if err := event(ctx, tx, now, nil, "offer", off.ID, "offer.closed", map[string]any{"outcome": "no_fit", "round": off.Round, "ride_id": r.ID}); err != nil {
			return err
		}
		data := rideData(r)
		data["offer_id"] = off.ID.String()
		if err := s.tellLosers(ctx, tx, off.ID, uuid.Nil, now, data); err != nil {
			return err
		}
		c, err := s.rideContext(ctx, tx, r)
		if err != nil {
			return err
		}
		if !now.Before(r.PickupAt) {
			// The round was due to close before pickup: an outage made it late.
			reason := "no_driver"
			if off.ExpiresAt.Before(r.PickupAt) && now.Sub(off.ExpiresAt) > time.Minute*2 {
				reason, outage = "missed_by_outage", true
			}
			if err := s.failRide(ctx, tx, r, c, reason, now); err != nil {
				return err
			}
			if outage {
				return s.pushAdmins(ctx, tx, now, "admin_rides_missed", "Rides missed", "1 ride missed by outage", map[string]string{"ride_ids": r.ID.String()})
			}
			return nil
		}
		if off.Round < s.cfg().MaxRounds {
			return s.openRideOffer(ctx, tx, r, c, off.Round+1, now)
		}
		return s.failRide(ctx, tx, r, c, "no_driver", now)
	})
	return err
}
