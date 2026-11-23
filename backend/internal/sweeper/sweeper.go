// Package sweeper is the timed work: one RunOnce per minute (Cloud Scheduler
// calls POST /internal/sweep). Each step selects what is due by comparing
// with now, so a missed run is caught up by the next.
package sweeper

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"commute-backend/internal/clock"
	"commute-backend/internal/notify"
	"commute-backend/internal/offers"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Sweeper runs the steps in order.
type Sweeper struct {
	DB     *pgxpool.Pool
	Clock  clock.Clock
	Offers *offers.Service
	Outbox *notify.Outbox
	// Optional: pinged after each run (Healthchecks.io); no ping for 5
	// minutes raises an alert there.
	HeartbeatURL string
	Log          *slog.Logger
}

// Counts is what one run did, per step.
type Counts struct {
	CloseOffers   offers.CloseCounts `json:"close_offers"`
	GenerateRides int                `json:"generate_rides"`
	SendOutbox    int                `json:"send_outbox"`
}

func (s *Sweeper) log() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

// RunOnce runs close_offers, generate_rides and send_outbox once, and
// records the run in sweeper_runs.
func (s *Sweeper) RunOnce(ctx context.Context) (Counts, error) {
	var c Counts
	started := s.Clock.Now()
	var runID uuid.UUID
	if err := s.DB.QueryRow(ctx, "insert into sweeper_runs (started_at, created_at) values ($1, $1) returning id", started).Scan(&runID); err != nil {
		return c, err
	}
	var firstErr error
	note := func(step string, err error) {
		if err != nil {
			s.log().Error("sweep step failed", "step", step, "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	var err error
	c.CloseOffers, err = s.Offers.CloseDueOffers(ctx)
	note("close_offers", err)
	c.GenerateRides, err = s.Offers.GenerateDueRides(ctx)
	note("generate_rides", err)
	c.SendOutbox, err = s.Outbox.SendDue(ctx)
	note("send_outbox", err)

	counts, _ := json.Marshal(c)
	var errText *string
	if firstErr != nil {
		e := firstErr.Error()
		errText = &e
	}
	if _, err := s.DB.Exec(ctx, "update sweeper_runs set finished_at = $2, counts = $3, error = $4 where id = $1",
		runID, s.Clock.Now(), counts, errText); err != nil && firstErr == nil {
		firstErr = err
	}
	if s.HeartbeatURL != "" && firstErr == nil {
		go s.ping()
	}
	return c, firstErr
}

func (s *Sweeper) ping() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.HeartbeatURL, nil)
	if err != nil {
		return
	}
	if res, err := http.DefaultClient.Do(req); err == nil {
		res.Body.Close()
	}
}

// Loop runs RunOnce every [every] until ctx ends (the worker process).
func (s *Sweeper) Loop(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if _, err := s.RunOnce(ctx); err != nil {
			s.log().Error("sweep failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
