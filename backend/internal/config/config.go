// Package config reads the environment and wires ride-api together: the
// API server and the sweeper share one clock and one offers service.
package config

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"commute-backend/internal/clock"
	"commute-backend/internal/drivers"
	"commute-backend/internal/httpapi"
	"commute-backend/internal/notify"
	"commute-backend/internal/offers"
	"commute-backend/internal/seed"
	"commute-backend/internal/storage"
	"commute-backend/internal/sweeper"
)

// Config is everything read from the environment.
type Config struct {
	Production bool
	City       string
	// Fixed sign-in code (dev builds use 123456). Refused in production.
	FixedOTP string
	// Key for hashing sign-in codes; 32+ characters in production.
	OTPSecret string
	// Web origins allowed to call the API from a browser.
	CORSOrigins []string
	// Bearer token POST /internal/sweep needs, when set.
	SweepToken string
	// Pinged after each sweep (Healthchecks.io).
	HeartbeatURL string
	// /dev routes (the simulator moves the clock). Refused in production.
	EnableDevRoutes bool
	// Run the sweeper inside the API process every SweepEvery (local runs).
	RunSweeperInAPI bool
	SweepEvery      time.Duration
}

// Load reads the config. In production, demo switches are refused.
func Load(getenv func(string) string) (Config, error) {
	c := Config{
		Production:      getenv("APP_ENV") == "production",
		City:            seed.City,
		FixedOTP:        getenv("FIXED_OTP"),
		OTPSecret:       getenv("OTP_SECRET"),
		SweepToken:      getenv("SWEEP_TOKEN"),
		HeartbeatURL:    getenv("HEARTBEAT_URL"),
		EnableDevRoutes: getenv("ENABLE_DEV_ROUTES") == "true",
		RunSweeperInAPI: getenv("RUN_SWEEPER_IN_API") == "true",
		SweepEvery:      time.Minute,
	}
	if c.OTPSecret == "" {
		c.OTPSecret = "dev-only-otp-secret"
	}
	for _, o := range strings.Split(getenv("CORS_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			c.CORSOrigins = append(c.CORSOrigins, o)
		}
	}
	if v := getenv("SWEEP_EVERY_MS"); v != "" {
		ms, err := strconv.Atoi(v)
		if err != nil || ms <= 0 {
			return c, errors.New("SWEEP_EVERY_MS must be a positive number of milliseconds")
		}
		c.SweepEvery = time.Duration(ms) * time.Millisecond
	}
	if c.Production {
		var unsafe []string
		if c.FixedOTP != "" {
			unsafe = append(unsafe, "FIXED_OTP")
		}
		if c.EnableDevRoutes {
			unsafe = append(unsafe, "ENABLE_DEV_ROUTES")
		}
		if len(unsafe) > 0 {
			return c, fmt.Errorf("refusing to start in production with %s set", strings.Join(unsafe, ", "))
		}
		if len(getenv("OTP_SECRET")) < 32 {
			return c, errors.New("refusing to start in production without OTP_SECRET (32+ random characters)")
		}
	}
	return c, nil
}

// App is the assembled service.
type App struct {
	API     *httpapi.Server
	Sweeper *sweeper.Sweeper
	Offers  *offers.Service
}

// Build wires the API and the sweeper on [pool] with [clk]. Outside
// services (SMS, maps, FCM) come from the environment.
func Build(ctx context.Context, c Config, getenv func(string) string, pool *pgxpool.Pool, clk clock.Clock, log *slog.Logger) (*App, error) {
	pusher, err := notify.PusherFromEnv(getenv, log)
	if err != nil {
		return nil, err
	}
	otp := httpapi.OTPSender(httpapi.ConsoleOTPSender)
	if key, tmpl := getenv("MSG91_AUTH_KEY"), getenv("MSG91_OTP_TEMPLATE_ID"); key != "" && tmpl != "" {
		otp = &httpapi.MSG91{AuthKey: key, TemplateID: tmpl, Log: log}
	} else if c.Production && c.FixedOTP == "" {
		log.Warn("no SMS provider: sign-in codes are only logged")
	}
	maps := httpapi.DemoMaps()
	var style httpapi.MapStyle
	if key := getenv("OLA_MAPS_API_KEY"); key != "" && getenv("MAPS_PROVIDER") != "demo" {
		maps = &httpapi.OlaMaps{APIKey: key}
		mb := 64.0
		if v := getenv("MAP_TILE_CACHE_MB"); v != "" {
			if mb, err = strconv.ParseFloat(v, 64); err != nil {
				return nil, errors.New("MAP_TILE_CACHE_MB must be a number")
			}
		}
		style = httpapi.NewOlaMapStyle(key, httpapi.OlaMapStyleOptions{CacheMB: &mb})
	}
	svc := &offers.Service{DB: pool, Clock: clk, Shuffler: offers.NewRandomShuffler(), Config: offers.DefaultConfig, Log: log}
	sw := &sweeper.Sweeper{DB: pool, Clock: clk, Offers: svc,
		Outbox: &notify.Outbox{DB: pool, Clock: clk, Pusher: pusher, Log: log}, HeartbeatURL: c.HeartbeatURL, Log: log}
	api := &httpapi.Server{
		DB: pool, Clock: clk, Offers: svc, Sweeper: sw, City: c.City,
		FixedOTP: c.FixedOTP, OTPSecret: c.OTPSecret, OTPResendSeconds: 30, OTPMaxSendsPerHour: 5, OTPSender: otp,
		Maps: maps, MapStyle: style, CORSOrigins: c.CORSOrigins, SweepToken: c.SweepToken,
		EnableDevRoutes: c.EnableDevRoutes, Log: log,
	}
	// Driver documents: kept by ride-api (in memory, or under STORAGE_DIR)
	// behind signed URLs on PUBLIC_URL. Supabase Storage would sit behind
	// the same storage.Storage interface.
	secret := getenv("STORAGE_SECRET")
	if secret == "" {
		if c.Production {
			return nil, errors.New("refusing to start in production without STORAGE_SECRET")
		}
		secret = "dev-only-storage-secret"
	}
	publicURL := getenv("PUBLIC_URL")
	if publicURL == "" {
		port := getenv("PORT")
		if port == "" {
			port = "8080"
		}
		publicURL = "http://localhost:" + port
	}
	files := &storage.Local{BaseURL: publicURL, Secret: secret, Dir: getenv("STORAGE_DIR"), Clock: clk}
	api.Storage = files
	api.Drivers = &drivers.Service{DB: pool, Clock: clk, Storage: files, RequireFile: true, Log: log}
	if off, ok := clk.(*clock.Offset); ok {
		api.DevClock = off
	}
	return &App{API: api, Sweeper: sw, Offers: svc}, nil
}
