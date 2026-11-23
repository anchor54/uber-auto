// The sweeper as its own process: RunOnce every minute (SWEEP_EVERY_MS).
// On Cloud Run, Cloud Scheduler calls POST /internal/sweep instead; on
// Render this worker plays Cloud Scheduler's part.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"commute-backend/internal/clock"
	"commute-backend/internal/config"
	"commute-backend/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("worker stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	pool, err := store.Open(ctx, "")
	if err != nil {
		return err
	}
	defer pool.Close()
	if _, err := store.Migrate(ctx, pool); err != nil {
		return err
	}
	app, err := config.Build(ctx, c, os.Getenv, pool, clock.Real{}, log)
	if err != nil {
		return err
	}
	log.Info("sweeping", "every", c.SweepEvery.String())
	app.Sweeper.Loop(ctx, c.SweepEvery)
	return nil
}
