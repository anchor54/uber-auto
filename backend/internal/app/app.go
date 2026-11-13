// Package app is the HTTP API, its services and the worker jobs.
package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"commute-backend/internal/domain"
)

type Config struct {
	// Every rider is in this city until multi-city sign-up exists.
	City string
	// Rider local time zone offset, for "tomorrow" and start dates. IST = 330.
	UTCOffsetMinutes int
	// Fixed OTP for demo and tests. Never set in production.
	FixedOTP string
	// Accept commutes on the driver's behalf the moment the rider confirms.
	// For demos without a driver; off by default and refused in production.
	AutoAcceptCommutes bool
	// How long a driver has to accept a new commute (D2).
	DriverResponseHours float64
	// Registers the /dev/sim routes that act as drivers. Never in production.
	EnableDriverSim bool
	// Key for hashing sign-in codes. Must be set (and secret) in production.
	OTPSecret string
	// Seconds between two codes for the same phone.
	OTPResendSeconds int
	// Codes one phone can ask for in an hour.
	OTPMaxSendsPerHour int
	// Web origins allowed to call the API from a browser (the web build).
	CORSOrigins []string
	// How long a proposed driver is held while the rider confirms.
	ProposalHoldMinutes int
}

var DefaultConfig = Config{
	City:                "blr",
	UTCOffsetMinutes:    330,
	ProposalHoldMinutes: 15,
	DriverResponseHours: 12,
	OTPSecret:           "dev-only-otp-secret",
	OTPResendSeconds:    30,
	OTPMaxSendsPerHour:  5,
	CORSOrigins:         []string{},
}

// IsProduction is true when APP_ENV (or NODE_ENV, as the Node backend used)
// is "production".
func IsProduction(getenv func(string) string) bool {
	return getenv("APP_ENV") == "production" || getenv("NODE_ENV") == "production"
}

// ConfigFromEnv reads the config from the environment. In production the
// demo switches are refused: a fixed code would let anyone sign in, and
// auto-accept or the simulator would confirm drivers who never agreed.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	c := DefaultConfig
	c.FixedOTP = getenv("FIXED_OTP")
	c.AutoAcceptCommutes = getenv("AUTO_ACCEPT_COMMUTES") == "true"
	c.EnableDriverSim = getenv("ENABLE_DRIVER_SIM") == "true"
	if v := getenv("DRIVER_RESPONSE_HOURS"); v != "" {
		h, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return c, fmt.Errorf("DRIVER_RESPONSE_HOURS: %w", err)
		}
		c.DriverResponseHours = h
	}
	if v := getenv("OTP_SECRET"); v != "" {
		c.OTPSecret = v
	}
	for _, o := range strings.Split(getenv("CORS_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			c.CORSOrigins = append(c.CORSOrigins, o)
		}
	}
	if IsProduction(getenv) {
		var unsafe []string
		if c.FixedOTP != "" {
			unsafe = append(unsafe, "FIXED_OTP")
		}
		if c.AutoAcceptCommutes {
			unsafe = append(unsafe, "AUTO_ACCEPT_COMMUTES")
		}
		if c.EnableDriverSim {
			unsafe = append(unsafe, "ENABLE_DRIVER_SIM")
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

// App is everything a request, job or simulated driver needs.
type App struct {
	DB     *pgxpool.Pool
	Config Config
	Clock  func() time.Time
	// Dev simulator only: moves Clock to [now]. Nil when the clock is fixed.
	SetClock  func(now time.Time)
	OTPSender OTPSender
	// Place search and trip estimates; the demo city when nil.
	Maps MapsProvider
	// The map the app draws (Ola Maps' style); none in the demo city.
	MapStyle MapStyle
	Log      *slog.Logger
}

func (a *App) maps() MapsProvider {
	if a.Maps == nil {
		a.Maps = DemoMaps(a.DB)
	}
	return a.Maps
}

func (a *App) now() time.Time { return a.Clock() }

func (a *App) log() *slog.Logger {
	if a.Log == nil {
		return slog.Default()
	}
	return a.Log
}

// RealClock is the system time, to the millisecond like the app's clocks.
func RealClock() time.Time { return time.Now().Truncate(time.Millisecond) }

// ---------------------------------------------------------------- errors

type badRequest struct{ message string }

func (e *badRequest) Error() string { return e.message }

func apiErr(status int, code, message string) error { return domain.NewApiError(status, code, message) }

func writeError(a *App, w http.ResponseWriter, r *http.Request, err error) {
	var ve *domain.ValidationError
	var ae *domain.ApiError
	var br *badRequest
	switch {
	case errors.As(err, &ve):
		writeJSON(w, 400, map[string]any{"error": "invalid_request", "message": ve.Error(), "issues": ve.Issues})
	case errors.As(err, &ae):
		writeJSON(w, ae.Status, map[string]any{"error": ae.Code, "message": ae.Message})
	case errors.As(err, &br):
		writeJSON(w, 400, map[string]any{"error": "bad_request", "message": br.message})
	default:
		a.log().Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		writeJSON(w, 500, map[string]any{"error": "internal", "message": "Something went wrong"})
	}
}

// ---------------------------------------------------------------- JSON

func writeJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		http.Error(w, `{"error":"internal","message":"Something went wrong"}`, 500)
		return
	}
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

const maxBody = 1 << 20

// decode reads the JSON body into [v]. An empty body leaves [v] as it is, so
// required fields (pointers) stay nil and the caller's checks report them.
func decode(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return &badRequest{"Could not read the request body"}
	}
	if len(body) > maxBody {
		return apiErr(413, "body_too_large", "Request body is too large")
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, v); err != nil {
		var te *json.UnmarshalTypeError
		if errors.As(err, &te) {
			var c domain.Check
			path := []any{}
			for _, p := range strings.Split(te.Field, ".") {
				if p != "" {
					path = append(path, p)
				}
			}
			c.Fail(fmt.Sprintf("Expected %s, received %s", te.Type.String(), te.Value), path...)
			return c.Err()
		}
		return &badRequest{"Body is not valid JSON"}
	}
	return nil
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// pathID is the :id path parameter, which must be a UUID.
func pathID(r *http.Request) (string, error) {
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		var c domain.Check
		c.Fail("Invalid uuid", "id")
		return "", c.Err()
	}
	return id, nil
}

// iso is a time as JavaScript's toISOString writes it.
func iso(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func isoPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := iso(*t)
	return &s
}

func str(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func noRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// inTx runs [f] in a transaction, rolled back if it returns an error.
func (a *App) inTx(ctx context.Context, f func(tx pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, a.DB, f)
}

// ---------------------------------------------------------------- routing

type handler func(w http.ResponseWriter, r *http.Request) error

// Handler is the whole API.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	handle := func(pattern string, h handler) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBody+1)
			if err := h(w, r); err != nil {
				writeError(a, w, r, err)
			}
		})
	}
	handle("GET /health", func(w http.ResponseWriter, r *http.Request) error {
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	})
	a.authRoutes(handle)
	a.bookingRoutes(handle)
	a.commuteRoutes(handle)
	a.driverRequestRoutes(handle)
	a.rideRoutes(handle)
	a.mapStyleRoutes(handle)
	if a.Config.EnableDriverSim {
		a.devSimRoutes(handle)
	}
	allowed := a.Config.CORSOrigins
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The web build calls the API from another origin (GitHub Pages).
		if origin := r.Header.Get("origin"); origin != "" && slices.Contains(allowed, origin) {
			w.Header().Set("access-control-allow-origin", origin)
			w.Header().Set("vary", "origin")
			if r.Method == http.MethodOptions {
				w.Header().Set("access-control-allow-methods", "GET, POST, PUT, OPTIONS")
				w.Header().Set("access-control-allow-headers", "authorization, content-type")
				w.Header().Set("access-control-max-age", "600")
				w.WriteHeader(204)
				return
			}
		}
		if _, pattern := mux.Handler(r); pattern == "" {
			writeJSON(w, 404, map[string]any{"error": "not_found", "message": fmt.Sprintf("Route %s:%s not found", r.Method, r.URL.Path)})
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// LogRequests logs each request's method, path, status and duration.
func LogRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		log.Info("request", "method", r.Method, "path", r.URL.Path, "status", sw.status, "ms", time.Since(start).Milliseconds())
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
