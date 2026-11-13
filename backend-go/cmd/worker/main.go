// The worker: everything that runs on a clock, one loop every 30 seconds.
// In production it is its own process; the API never runs these jobs there.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"commute-backend/internal/app"
	"commute-backend/internal/db"
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

	every := 30 * time.Second
	if v := os.Getenv("WORKER_EVERY_MS"); v != "" {
		ms, err := strconv.Atoi(v)
		if err != nil || ms <= 0 {
			return errors.New("WORKER_EVERY_MS must be a positive number of milliseconds")
		}
		every = time.Duration(ms) * time.Millisecond
	}
	config, err := app.ConfigFromEnv(os.Getenv)
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, "")
	if err != nil {
		return err
	}
	defer pool.Close()
	if _, err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	// Real time only. When the dev simulator moves the API's clock, run the
	// jobs inside the API instead (RUN_JOBS_IN_API=true) so both share it.
	a := &app.App{DB: pool, Config: config, Clock: app.RealClock, OTPSender: app.ConsoleOTPSender, Log: log}
	log.Info("[worker] running jobs", "every", every.String())
	a.StartJobLoop(ctx, app.LogPushSender(log), every, log)
	return nil
}
