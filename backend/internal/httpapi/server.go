// Package httpapi is ride-api's HTTP layer: sign-in, the series endpoints
// (R1), the driver's offer endpoints (D2), rides.week, the map proxy and
// /internal/sweep. Each request is one Postgres transaction in the offers
// service.
package httpapi

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"commute-backend/internal/clock"
	"commute-backend/internal/drivers"
	"commute-backend/internal/offers"
	"commute-backend/internal/scheduling"
	"commute-backend/internal/sweeper"
)

// Server is the whole API.
type Server struct {
	DB      *pgxpool.Pool
	Clock   clock.Clock
	Offers  *offers.Service
	Drivers *drivers.Service
	Sweeper *sweeper.Sweeper
	City    string
	// Serves /storage/ (signed upload and read URLs) when files are kept
	// by ride-api itself (storage.Local); nil with an outside store.
	Storage http.Handler

	// Fixed sign-in code for dev and tests. Never set in production.
	FixedOTP           string
	OTPSecret          string
	OTPResendSeconds   int
	OTPMaxSendsPerHour int
	OTPSender          OTPSender

	Maps     MapsProvider
	MapStyle MapStyle

	// Web origins allowed to call the API from a browser.
	CORSOrigins []string
	// When set, POST /internal/sweep needs "Authorization: Bearer <SweepToken>".
	// (On Cloud Run, the OIDC token is checked by the platform.)
	SweepToken string
	// Registers POST /dev/clock/advance (the simulator's "close offers now").
	EnableDevRoutes bool
	DevClock        *clock.Offset
	Log             *slog.Logger
}

func (s *Server) log() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

func (s *Server) now() time.Time { return s.Clock.Now() }

func (s *Server) maps() MapsProvider {
	if s.Maps == nil {
		s.Maps = DemoMaps()
	}
	return s.Maps
}

// ---------------------------------------------------------------- errors

// apiErr is an error with an HTTP status and a code.
func apiErr(status int, code, message string) error {
	return &offers.Error{Status: status, Code: code, Message: message}
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var oe *offers.Error
	if errors.As(err, &oe) {
		body := map[string]any{}
		for k, v := range oe.Extra {
			body[k] = v
		}
		body["code"], body["message"], body["state"] = oe.Code, oe.Message, oe.State
		writeJSON(w, oe.Status, body)
		return
	}
	s.log().Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeJSON(w, 500, map[string]any{"code": "internal", "message": "Something went wrong", "state": nil})
}

// ---------------------------------------------------------------- JSON

func writeJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		http.Error(w, `{"code":"internal","message":"Something went wrong"}`, 500)
		return
	}
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

const maxBody = 1 << 20

// decode reads the JSON body into [v]; an empty body leaves [v] as it is.
func decode(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return apiErr(400, "bad_request", "Could not read the request body")
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
			return apiErr(400, "invalid_request", fmt.Sprintf("%s: expected %s, got %s", te.Field, te.Type, te.Value))
		}
		return apiErr(400, "bad_request", "Body is not valid JSON")
	}
	return nil
}

func pathID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return id, apiErr(404, "not_found", "No such id")
	}
	return id, nil
}

func noRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// ---------------------------------------------------------------- routing

type handler func(w http.ResponseWriter, r *http.Request) error

// Handler is the whole API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	handle := func(pattern string, h handler) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBody+1)
			if err := h(w, r); err != nil {
				s.writeError(w, r, err)
			}
		})
	}
	handle("GET /health", func(w http.ResponseWriter, r *http.Request) error {
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	})
	s.authRoutes(handle)
	s.seriesRoutes(handle)
	s.offerRoutes(handle)
	if s.Drivers != nil {
		s.driverRoutes(handle)
		s.adminRoutes(handle)
	}
	if s.Storage != nil {
		// Not through handle: uploads are up to 5 MB.
		mux.Handle("PUT /storage/", s.Storage)
		mux.Handle("GET /storage/", s.Storage)
	}
	s.mapStyleRoutes(handle)
	handle("GET /v1/places", func(w http.ResponseWriter, r *http.Request) error {
		if _, _, err := s.requireUser(r); err != nil {
			return err
		}
		places, err := s.maps().SearchPlaces(r.Context(), r.URL.Query().Get("q"), s.cityArea())
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"places": places})
		return nil
	})
	handle("POST /internal/sweep", func(w http.ResponseWriter, r *http.Request) error {
		if s.SweepToken != "" {
			token, _ := strings.CutPrefix(r.Header.Get("authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(token), []byte(s.SweepToken)) != 1 {
				return apiErr(401, "unauthorized", "Bad sweep token")
			}
		}
		if s.Sweeper == nil {
			return apiErr(404, "not_found", "This service doesn't sweep")
		}
		counts, err := s.Sweeper.RunOnce(r.Context())
		if err != nil {
			return err
		}
		writeJSON(w, 200, counts)
		return nil
	})
	if s.EnableDevRoutes && s.DevClock != nil {
		// The simulator's "close offers now": move the clock past every window, then sweep.
		handle("POST /dev/clock/advance", func(w http.ResponseWriter, r *http.Request) error {
			var body struct {
				Minutes float64 `json:"minutes"`
			}
			if err := decode(r, &body); err != nil {
				return err
			}
			if body.Minutes <= 0 || body.Minutes > 24*60 {
				return apiErr(400, "invalid_request", "minutes must be between 0 and 1440")
			}
			s.DevClock.Advance(time.Duration(body.Minutes * float64(time.Minute)))
			writeJSON(w, 200, map[string]any{"now": s.now()})
			return nil
		})
	}
	allowed := s.CORSOrigins
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("origin"); origin != "" && slices.Contains(allowed, origin) {
			w.Header().Set("access-control-allow-origin", origin)
			w.Header().Set("vary", "origin")
			if r.Method == http.MethodOptions {
				w.Header().Set("access-control-allow-methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("access-control-allow-headers", "authorization, content-type")
				w.Header().Set("access-control-max-age", "600")
				w.WriteHeader(204)
				return
			}
		}
		if _, pattern := mux.Handler(r); pattern == "" {
			writeJSON(w, 404, map[string]any{"code": "not_found", "message": fmt.Sprintf("Route %s %s not found", r.Method, r.URL.Path), "state": nil})
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------- series (R1)

func (s *Server) seriesRoutes(handle func(string, handler)) {
	handle("POST /v1/series/preview", func(w http.ResponseWriter, r *http.Request) error {
		if _, err := s.requireRider(r); err != nil {
			return err
		}
		var req offers.SeriesRequest
		if err := decode(r, &req); err != nil {
			return err
		}
		v, err := s.Offers.Preview(r.Context(), req)
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
		return nil
	})
	handle("POST /v1/series", func(w http.ResponseWriter, r *http.Request) error {
		rider, err := s.requireRider(r)
		if err != nil {
			return err
		}
		var req offers.SeriesRequest
		if err := decode(r, &req); err != nil {
			return err
		}
		v, err := s.Offers.Create(r.Context(), rider, req)
		if err != nil {
			return err
		}
		writeJSON(w, 201, v)
		return nil
	})
	handle("GET /v1/series", func(w http.ResponseWriter, r *http.Request) error {
		rider, err := s.requireRider(r)
		if err != nil {
			return err
		}
		list, err := s.Offers.List(r.Context(), rider)
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"series": list})
		return nil
	})
	handle("GET /v1/series/{id}", func(w http.ResponseWriter, r *http.Request) error {
		rider, err := s.requireRider(r)
		if err != nil {
			return err
		}
		id, err := pathID(r)
		if err != nil {
			return err
		}
		v, err := s.Offers.Get(r.Context(), rider, id)
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
		return nil
	})
	handle("POST /v1/series/{id}/cancel-request", func(w http.ResponseWriter, r *http.Request) error {
		rider, err := s.requireRider(r)
		if err != nil {
			return err
		}
		id, err := pathID(r)
		if err != nil {
			return err
		}
		if err := s.Offers.CancelRequest(r.Context(), rider, id); err != nil {
			return err
		}
		w.WriteHeader(204)
		return nil
	})
	handle("POST /v1/series/{id}/retry", func(w http.ResponseWriter, r *http.Request) error {
		rider, err := s.requireRider(r)
		if err != nil {
			return err
		}
		id, err := pathID(r)
		if err != nil {
			return err
		}
		var req offers.SeriesRequest
		if err := decode(r, &req); err != nil {
			return err
		}
		v, err := s.Offers.Retry(r.Context(), rider, id, req)
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
		return nil
	})
	handle("GET /v1/ride-types", func(w http.ResponseWriter, r *http.Request) error {
		if _, _, err := s.requireUser(r); err != nil {
			return err
		}
		flow := r.URL.Query().Get("flow")
		if flow == "" {
			flow = offers.FlowRecurring
		}
		rows, err := s.DB.Query(r.Context(), `select c.ride_type, c.label, c.description, c.seats
			from ride_type_config c where $1 = any (c.flows) and $2 = any (c.cities) order by c.sort, c.ride_type`, flow, s.City)
		if err != nil {
			return err
		}
		type rideType struct {
			RideType    string `json:"ride_type"`
			Label       string `json:"label"`
			Description string `json:"description"`
			Seats       int    `json:"seats"`
		}
		list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (rideType, error) {
			var t rideType
			err := row.Scan(&t.RideType, &t.Label, &t.Description, &t.Seats)
			return t, err
		})
		if err != nil {
			return err
		}
		if list == nil {
			list = []rideType{}
		}
		writeJSON(w, 200, map[string]any{"city": s.City, "flow": flow, "ride_types": list})
		return nil
	})
}

// ---------------------------------------------------------------- offers (D2)

func (s *Server) offerRoutes(handle func(string, handler)) {
	handle("GET /v1/offers", func(w http.ResponseWriter, r *http.Request) error {
		driver, err := s.requireDriver(r)
		if err != nil {
			return err
		}
		list, err := s.Offers.ListOffers(r.Context(), driver)
		if err != nil {
			return err
		}
		writeJSON(w, 200, list)
		return nil
	})
	handle("GET /v1/offers/{id}", func(w http.ResponseWriter, r *http.Request) error {
		driver, err := s.requireDriver(r)
		if err != nil {
			return err
		}
		id, err := pathID(r)
		if err != nil {
			return err
		}
		v, err := s.Offers.GetOffer(r.Context(), driver, id)
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
		return nil
	})
	action := func(f func(*offers.Service) func(ctx contextT, driver, id uuid.UUID) error) handler {
		return func(w http.ResponseWriter, r *http.Request) error {
			driver, err := s.requireDriver(r)
			if err != nil {
				return err
			}
			id, err := pathID(r)
			if err != nil {
				return err
			}
			if err := f(s.Offers)(r.Context(), driver, id); err != nil {
				return err
			}
			w.WriteHeader(204)
			return nil
		}
	}
	handle("POST /v1/offers/{id}/mark-interested", action(func(o *offers.Service) func(contextT, uuid.UUID, uuid.UUID) error {
		return o.MarkInterested
	}))
	handle("POST /v1/offers/{id}/withdraw-interest", action(func(o *offers.Service) func(contextT, uuid.UUID, uuid.UUID) error {
		return o.WithdrawInterest
	}))
	handle("POST /v1/offers/{id}/dismiss", action(func(o *offers.Service) func(contextT, uuid.UUID, uuid.UUID) error {
		return o.Dismiss
	}))
	handle("GET /v1/rides/week", func(w http.ResponseWriter, r *http.Request) error {
		driver, err := s.requireDriver(r)
		if err != nil {
			return err
		}
		var start *time.Time
		if v := r.URL.Query().Get("start"); v != "" {
			d, err := scheduling.ParseDate(v)
			if err != nil {
				return apiErr(400, "invalid_request", "start must be YYYY-MM-DD")
			}
			start = &d
		}
		week, err := s.Offers.Week(r.Context(), driver, start)
		if err != nil {
			return err
		}
		writeJSON(w, 200, week)
		return nil
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
