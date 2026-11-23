// Package offers is the series request and the driver pick (R1 and D2): a
// rider's series is offered to every eligible driver in rounds; drivers mark
// interest; when a round closes, interested drivers are tried in random order
// under a lock until one has no clash.
package offers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"commute-backend/internal/clock"
	"commute-backend/internal/scheduling"
)

// Config is the service's settings.
type Config struct {
	// Every rider is in this city until multi-city sign-up exists.
	City string
	// The zone weekdays and local times are read in.
	TimeZone string
	// How long a series or slot round stays open.
	SeriesWindow time.Duration
	// How long a one-ride round stays open.
	RideWindow time.Duration
	// Rounds before a series is no_driver_found.
	MaxRounds int
	// A closing offer older than this (a crashed sweep) is picked up again.
	ClosingRetryAfter time.Duration
}

// DefaultConfig is production's settings.
var DefaultConfig = Config{
	City:              "blr",
	TimeZone:          scheduling.DefaultTimeZone,
	SeriesWindow:      15 * time.Minute,
	RideWindow:        5 * time.Minute,
	MaxRounds:         3,
	ClosingRetryAfter: 5 * time.Minute,
}

// FlowRecurring is the ride_type_config flow for series.
const FlowRecurring = "recurring"

// Candidate is an interested driver at the pick.
type Candidate struct {
	DriverID uuid.UUID
	Name     string
}

// Shuffler orders the interested drivers for the pick. Tests inject a fixed order.
type Shuffler interface {
	Shuffle(c []Candidate)
}

// RandomShuffler is a uniformly random order.
type RandomShuffler struct {
	mu sync.Mutex
	r  *rand.Rand
}

func NewRandomShuffler() *RandomShuffler {
	return &RandomShuffler{r: rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))}
}

func (s *RandomShuffler) Shuffle(c []Candidate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.r.Shuffle(len(c), func(i, j int) { c[i], c[j] = c[j], c[i] })
}

// Service holds what the series and offer operations need.
type Service struct {
	DB       *pgxpool.Pool
	Clock    clock.Clock
	Shuffler Shuffler
	Config   Config
	Log      *slog.Logger

	locMu sync.Mutex
	locs  map[string]*time.Location
}

func (s *Service) log() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

func (s *Service) cfg() Config {
	c := s.Config
	d := DefaultConfig
	if c.City == "" {
		c.City = d.City
	}
	if c.TimeZone == "" {
		c.TimeZone = d.TimeZone
	}
	if c.SeriesWindow == 0 {
		c.SeriesWindow = d.SeriesWindow
	}
	if c.RideWindow == 0 {
		c.RideWindow = d.RideWindow
	}
	if c.MaxRounds == 0 {
		c.MaxRounds = d.MaxRounds
	}
	if c.ClosingRetryAfter == 0 {
		c.ClosingRetryAfter = d.ClosingRetryAfter
	}
	return c
}

func (s *Service) shuffler() Shuffler {
	if s.Shuffler == nil {
		s.Shuffler = NewRandomShuffler()
	}
	return s.Shuffler
}

func (s *Service) loc(name string) *time.Location {
	if name == "" {
		name = s.cfg().TimeZone
	}
	s.locMu.Lock()
	defer s.locMu.Unlock()
	if s.locs == nil {
		s.locs = map[string]*time.Location{}
	}
	if l, ok := s.locs[name]; ok {
		return l
	}
	l, err := time.LoadLocation(name)
	if err != nil {
		// IST, fixed: Asia/Kolkata has had no DST since 1945.
		l = time.FixedZone("IST", 330*60)
	}
	s.locs[name] = l
	return l
}

// today is the local date in the service's zone.
func (s *Service) today(now time.Time) time.Time { return scheduling.DateOf(now, s.loc("")) }

// ---------------------------------------------------------------- errors

// Error is an API error: { "code", "message", "state" }, where state is the
// object as it is now, so the app can redraw without another call.
type Error struct {
	Status  int
	Code    string
	Message string
	State   any
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func errf(status int, code, format string, args ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

var (
	errNotFound = func(what string) *Error { return errf(404, "not_found", "No such %s", what) }
)

// ---------------------------------------------------------------- helpers

func noRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// isExclusionViolation is the no_driver_overlap constraint refusing a ride.
func isExclusionViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23P01"
}

type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// event appends to the events table: who changed what, old and new status.
func event(ctx context.Context, q querier, at time.Time, actor *uuid.UUID, entity string, id uuid.UUID, typ string, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `insert into events (at, actor_id, entity, entity_id, type, data, created_at) values ($1, $2, $3, $4, $5, $6, $1)`,
		at, actor, entity, id, typ, raw)
	return err
}

// FirstName is the first word of a name.
func FirstName(name string) string {
	name = strings.TrimSpace(name)
	if i := strings.IndexByte(name, ' '); i > 0 {
		return name[:i]
	}
	return name
}

// nameAndInitial is "Priya S." for "Priya Sharma".
func nameAndInitial(name string) string {
	parts := strings.Fields(name)
	switch len(parts) {
	case 0:
		return "Rider"
	case 1:
		return parts[0]
	}
	return parts[0] + " " + string([]rune(parts[len(parts)-1])[0]) + "."
}

func ptr[T any](v T) *T { return &v }
