package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"commute-backend/internal/domain"
)

// The worker's jobs (cmd/worker runs them every 30 seconds). Each one is
// safe to run twice or from two workers at once: inserts are unique,
// updates check the state they expect, and sends claim rows with
// FOR UPDATE SKIP LOCKED.

const (
	nightlyAtMinute             = 30 // 00:30 local
	legIsOnBeforeMinutes        = 30
	paymentReminderAfterMinutes = 120
)

// CreateRidesNightly creates rides 14 days ahead, once a night after 00:30 local.
func (a *App) CreateRidesNightly(ctx context.Context) (int, error) {
	now := a.now()
	local := now.Add(time.Duration(a.Config.UTCOffsetMinutes) * time.Minute).UTC()
	if local.Hour()*60+local.Minute() < nightlyAtMinute {
		return 0, nil
	}
	today := domain.LocalDateString(now, a.Config.UTCOffsetMinutes)
	// Claim tonight's run; a second worker gets no row back and skips.
	tag, err := a.DB.Exec(ctx, `insert into job_runs (name, last_run_on) values ('create_rides', $1::text::date)
		on conflict (name) do update set last_run_on = excluded.last_run_on where job_runs.last_run_on < excluded.last_run_on`, today)
	if err != nil || tag.RowsAffected() == 0 {
		return 0, err
	}
	return a.CreateRides(ctx, "")
}

// QueueLegIsOn queues "Leg is on" for scheduled rides 30 minutes from pickup.
func (a *App) QueueLegIsOn(ctx context.Context) (int, error) {
	now := a.now()
	rides, err := queryRides(ctx, a.DB, `select `+rideColumns+` from rides r where r.state = 'scheduled' and r.pickup_at > $1 and r.pickup_at <= $2
		and not exists (select 1 from notifications n where n.kind = 'leg_is_on' and n.ride_id = r.id)`,
		now, now.Add(legIsOnBeforeMinutes*time.Minute))
	if err != nil {
		return 0, err
	}
	for i := range rides {
		r := &rides[i]
		payload, err := a.notifyPayload(ctx, a.DB, r)
		if err != nil {
			return i, err
		}
		if err := queueNotification(ctx, a.DB, notification{To: Recipient{"rider", r.RiderID}, Kind: "leg_is_on", RideID: r.ID, SendAfter: now, Payload: payload}); err != nil {
			return i, err
		}
	}
	return len(rides), nil
}

// FlagLateDrivers flags late drivers. ETA comes from the driver's latest
// position; a driver who hasn't sent one is late once the window has been
// over for 5 minutes.
func (a *App) FlagLateDrivers(ctx context.Context) (int, error) {
	now := a.now()
	rows, err := a.DB.Query(ctx, `select `+rideColumns+`, l.lat, l.lng from rides r left join driver_locations l on l.driver_id = r.driver_id
		where r.state in ('scheduled', 'on_the_way') and r.late_flagged_at is null and r.pickup_at <= $1`, now.Add(90*time.Minute))
	if err != nil {
		return 0, err
	}
	type withLoc struct {
		ride     RideRow
		lat, lng *float64
	}
	var list []withLoc
	for rows.Next() {
		var w withLoc
		if err := rows.Scan(append(w.ride.dest(), &w.lat, &w.lng)...); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, w)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	flagged := 0
	for i := range list {
		w := &list[i]
		eta := 0
		if w.lat != nil && w.lng != nil {
			eta = domain.EtaMinutes(domain.HaversineKm(*w.lat, *w.lng, w.ride.Pickup.Lat, w.ride.Pickup.Lng))
		}
		late, err := a.flagIfLate(ctx, &w.ride, eta)
		if err != nil {
			return flagged, err
		}
		if late {
			flagged++
		}
	}
	return flagged, nil
}

// QueuePaymentReminders reminds, two hours after drop, whichever side hasn't
// marked payment. Disputes go to ops instead.
func (a *App) QueuePaymentReminders(ctx context.Context) (int, error) {
	now := a.now()
	rides, err := queryRides(ctx, a.DB, `select `+rideColumns+` from rides r where r.state = 'completed' and r.ended_at <= $1 and r.ended_at > $2`,
		now.Add(-paymentReminderAfterMinutes*time.Minute), now.Add(-7*24*time.Hour))
	if err != nil {
		return 0, err
	}
	queued := 0
	for _, r := range rides {
		status := domain.PaymentStatus(str(r.RiderPayment), str(r.DriverPayment))
		var to []Recipient
		switch status {
		case "unmarked":
			to = []Recipient{{"rider", r.RiderID}, {"driver", r.DriverID}}
		case "paid_by_rider":
			to = []Recipient{{"driver", r.DriverID}}
		case "unpaid":
			to = []Recipient{{"rider", r.RiderID}}
		}
		for _, rec := range to {
			if err := queueNotification(ctx, a.DB, notification{To: rec, Kind: "payment_reminder", RideID: r.ID, SendAfter: now,
				Payload: map[string]any{"status": status}}); err != nil {
				return queued, err
			}
			queued++
		}
	}
	return queued, nil
}

// CloseStaleRides closes rides nothing has happened to for 3 hours.
func (a *App) CloseStaleRides(ctx context.Context) (int, error) {
	cutoff := a.now().Add(-domain.StaleAfterHours * time.Hour)
	rows, err := a.DB.Query(ctx, `select id from rides where state in ('on_the_way', 'arrived', 'in_ride')
		and greatest(departed_at, arrived_at, started_at) < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	closed := 0
	for _, id := range ids {
		// A ride that moved on since we looked (e.g. the driver ended it) is left alone.
		if _, err := a.ApplyMove(ctx, id, "close_stale", Actor{Type: "system"}, MoveInput{}); err == nil {
			closed++
		}
	}
	return closed, rows.Err()
}

// JobCounts is what each job did in one run.
type JobCounts struct {
	RidesCreated     int `json:"ridesCreated"`
	LegIsOn          int `json:"legIsOn"`
	LateFlagged      int `json:"lateFlagged"`
	PaymentReminders int `json:"paymentReminders"`
	StaleClosed      int `json:"staleClosed"`
	Sent             int `json:"sent"`
	TrackPruned      int `json:"trackPruned"`
}

// TrackKeptFor is how long ride_track points are kept, to settle disputes.
const TrackKeptFor = 30 * 24 * time.Hour

// PruneRideTrack deletes ride_track points older than 30 days, once a day.
func (a *App) PruneRideTrack(ctx context.Context) (int, error) {
	now := a.now()
	today := domain.LocalDateString(now, a.Config.UTCOffsetMinutes)
	// Claim today's run; a second worker gets no row back and skips.
	tag, err := a.DB.Exec(ctx, `insert into job_runs (name, last_run_on) values ('prune_track', $1::text::date)
		on conflict (name) do update set last_run_on = excluded.last_run_on where job_runs.last_run_on < excluded.last_run_on`, today)
	if err != nil || tag.RowsAffected() == 0 {
		return 0, err
	}
	tag, err = a.DB.Exec(ctx, "delete from ride_track where at < $1", now.Add(-TrackKeptFor))
	return int(tag.RowsAffected()), err
}

func (c JobCounts) any() bool { return c != JobCounts{} }

// RunJobs runs every job once.
func (a *App) RunJobs(ctx context.Context, sender PushSender) (JobCounts, error) {
	var c JobCounts
	var err error
	steps := []struct {
		n *int
		f func(context.Context) (int, error)
	}{
		{&c.RidesCreated, a.CreateRidesNightly},
		{&c.LegIsOn, a.QueueLegIsOn},
		{&c.LateFlagged, a.FlagLateDrivers},
		{&c.PaymentReminders, a.QueuePaymentReminders},
		{&c.StaleClosed, a.CloseStaleRides},
		{&c.Sent, func(ctx context.Context) (int, error) { return a.SendDueNotifications(ctx, sender) }},
		{&c.TrackPruned, a.PruneRideTrack},
	}
	for _, s := range steps {
		if *s.n, err = s.f(ctx); err != nil {
			return c, err
		}
	}
	return c, nil
}

// StartJobLoop runs RunJobs every [every] until [ctx] is done. Returns when
// the loop has stopped.
func (a *App) StartJobLoop(ctx context.Context, sender PushSender, every time.Duration, log *slog.Logger) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		did, err := a.RunJobs(ctx, sender)
		if err != nil && ctx.Err() == nil {
			log.Error("[jobs] run failed", "err", err)
		} else if did.any() {
			b, _ := json.Marshal(did)
			log.Info("[jobs] " + string(b))
		}
		timer.Reset(every)
	}
}
