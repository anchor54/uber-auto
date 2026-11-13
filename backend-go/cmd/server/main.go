// The API server.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"commute-backend/internal/app"
	"commute-backend/internal/db"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	config, err := app.ConfigFromEnv(os.Getenv) // Fails before touching the database if unsafe.
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, "")
	if err != nil {
		return err
	}
	defer pool.Close()
	providers, err := app.ProvidersFromEnv(os.Getenv, pool, log)
	if err != nil {
		return err
	}
	if applied, err := db.Migrate(ctx, pool); err != nil {
		return err
	} else if len(applied) > 0 {
		log.Info("migrated", "applied", applied)
	}

	// The dev simulator can move this clock (POST /dev/sim/clock) to replay a
	// ride at any time of day. Without the simulator it is real time.
	var offset atomic.Int64
	a := &app.App{
		DB:        pool,
		Config:    config,
		Clock:     func() time.Time { return app.RealClock().Add(time.Duration(offset.Load())) },
		OTPSender: providers.OTPSender,
		Maps:      providers.Maps,
		MapStyle:  providers.MapStyle,
		Log:       log,
	}
	if config.EnableDriverSim {
		// Measured against the same millisecond clock, so the next reading is [now] exactly.
		a.SetClock = func(now time.Time) { offset.Store(int64(now.Sub(app.RealClock()))) }
	}

	// Production runs the worker as its own process (cmd/worker). For local
	// and end-to-end runs it can share this process, and so this clock.
	if os.Getenv("RUN_JOBS_IN_API") == "true" {
		if app.IsProduction(os.Getenv) {
			return errors.New("run the worker as its own process in production")
		}
		every, err := workerEvery()
		if err != nil {
			return err
		}
		go a.StartJobLoop(ctx, app.LogPushSender(log), every, log)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	srv := &http.Server{
		Addr:              "0.0.0.0:" + port,
		Handler:           app.LogRequests(log, a.Handler()),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	log.Info("listening", "addr", srv.Addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func workerEvery() (time.Duration, error) {
	ms := 30_000
	if v := os.Getenv("WORKER_EVERY_MS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return 0, errors.New("WORKER_EVERY_MS must be a positive number of milliseconds")
		}
		ms = n
	}
	return time.Duration(ms) * time.Millisecond, nil
}
