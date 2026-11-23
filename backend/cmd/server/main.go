// ride-api: the API server. Cloud Scheduler (or the worker) calls
// POST /internal/sweep every minute for the timed work.
//
// Env: DATABASE_URL, PORT, APP_ENV, OTP_SECRET, FIXED_OTP (dev), SWEEP_TOKEN,
// CORS_ORIGINS, ENABLE_DEV_ROUTES (dev), RUN_SWEEPER_IN_API (local runs),
// FCM_SERVICE_ACCOUNT_JSON, MSG91_*, OLA_MAPS_API_KEY, HEARTBEAT_URL.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"commute-backend/internal/clock"
	"commute-backend/internal/config"
	"commute-backend/internal/httpapi"
	"commute-backend/internal/seed"
	"commute-backend/internal/store"
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
	c, err := config.Load(os.Getenv) // fails before touching the database if unsafe
	if err != nil {
		return err
	}
	pool, err := store.Open(ctx, "")
	if err != nil {
		return err
	}
	defer pool.Close()
	if applied, err := store.Migrate(ctx, pool); err != nil {
		return err
	} else if len(applied) > 0 {
		log.Info("migrated", "applied", applied)
	}
	if err := seed.Config(ctx, pool); err != nil {
		return err
	}
	// Dev: the simulator can move this clock forward to close offers at once.
	var clk clock.Clock = clock.Real{}
	if c.EnableDevRoutes {
		clk = &clock.Offset{}
	}
	app, err := config.Build(ctx, c, os.Getenv, pool, clk, log)
	if err != nil {
		return err
	}
	if c.RunSweeperInAPI {
		go app.Sweeper.Loop(ctx, c.SweepEvery)
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	srv := &http.Server{Addr: "0.0.0.0:" + port, Handler: httpapi.LogRequests(log, app.API.Handler()), ReadHeaderTimeout: 10 * time.Second}
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
