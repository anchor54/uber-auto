// Package testkit runs the real ride-api in-process for integration tests:
// a fresh Postgres schema per test (all migrations applied, seeded), the
// handlers on a random port, a fake clock shared by the API and the sweeper,
// a push recorder and a fixed shuffle. See backend/TEST_CONTRACT.md.
package testkit

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"commute-backend/internal/clock"
	"commute-backend/internal/httpapi"
	"commute-backend/internal/notify"
	"commute-backend/internal/offers"
	"commute-backend/internal/seed"
	"commute-backend/internal/store"
	"commute-backend/internal/sweeper"
)

// DefaultDatabaseURL is used when DATABASE_URL is not set.
const DefaultDatabaseURL = "postgres://app:app@localhost:5432/commute_test"

// FixedOTP is the sign-in code in tests.
const FixedOTP = "123456"

// Option changes how Start sets up.
type Option func(*config)

type config struct {
	seed    string
	shuffle []string
}

// WithSeed picks the seed: "spec" (default; the doc's Integration tests
// seed) or "empty" (config only: ride types and fare rules, no users).
func WithSeed(name string) Option { return func(c *config) { c.seed = name } }

// WithShuffle fixes the pick order: driver first names, first tried first.
// Interested drivers not named go after them, by name.
func WithShuffle(order ...string) Option { return func(c *config) { c.shuffle = order } }

// Env is a running API.
type Env struct {
	BaseURL string
	DB      *pgxpool.Pool
	Clock   *clock.Fake
	Push    *PushRecorder
	// The services behind the API, for tests that need them directly.
	Offers  *offers.Service
	Sweeper *sweeper.Sweeper

	mu    sync.Mutex
	users map[string]uuid.UUID // by first name
	names map[uuid.UUID]string
}

// Start runs the API against a fresh schema; everything is cleaned up when
// the test ends.
func Start(t *testing.T, opts ...Option) *Env {
	t.Helper()
	cfg := config{seed: "spec"}
	for _, o := range opts {
		o(&cfg)
	}
	ctx := context.Background()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = DefaultDatabaseURL
	}
	schema := "t_" + randomHex(8)
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("testkit: connect: %v", err)
	}
	// btree_gist lives in public, shared by every test schema.
	if _, err := admin.Exec(ctx, "select pg_advisory_lock(7200312002)"); err != nil {
		t.Fatal(err)
	}
	_, err = admin.Exec(ctx, "create extension if not exists btree_gist schema public")
	admin.Exec(ctx, "select pg_advisory_unlock(7200312002)")
	if err != nil {
		admin.Close()
		t.Fatalf("testkit: btree_gist: %v", err)
	}
	if _, err := admin.Exec(ctx, "create schema "+schema); err != nil {
		admin.Close()
		t.Fatalf("testkit: create schema: %v", err)
	}
	pcfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	pcfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pcfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		admin.Exec(context.Background(), "drop schema if exists "+schema+" cascade")
		admin.Close()
	})
	if _, err := store.Migrate(ctx, pool); err != nil {
		t.Fatalf("testkit: migrate: %v", err)
	}
	if err := seed.Config(ctx, pool); err != nil {
		t.Fatalf("testkit: seed config: %v", err)
	}

	env := &Env{DB: pool, Clock: clock.NewFake(seed.SpecNow), Push: &PushRecorder{}, users: map[string]uuid.UUID{}, names: map[uuid.UUID]string{}}
	env.Push.env = env
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("TESTKIT_LOG") != "" {
		quiet = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	var shuffler offers.Shuffler = offers.NewRandomShuffler()
	if cfg.shuffle != nil {
		shuffler = &fixedShuffle{order: cfg.shuffle}
	}
	env.Offers = &offers.Service{DB: pool, Clock: env.Clock, Shuffler: shuffler, Config: offers.DefaultConfig, Log: quiet}
	env.Sweeper = &sweeper.Sweeper{DB: pool, Clock: env.Clock, Offers: env.Offers,
		Outbox: &notify.Outbox{DB: pool, Clock: env.Clock, Pusher: env.Push, Log: quiet}, Log: quiet}

	switch cfg.seed {
	case "spec":
		ids, err := seed.Spec(ctx, pool, env.Offers)
		if err != nil {
			t.Fatalf("testkit: spec seed: %v", err)
		}
		for name, id := range ids {
			env.users[name] = id
			env.names[id] = name
		}
	case "empty", "":
	default:
		t.Fatalf("testkit: unknown seed %q", cfg.seed)
	}

	api := &httpapi.Server{
		DB: pool, Clock: env.Clock, Offers: env.Offers, Sweeper: env.Sweeper, City: seed.City,
		FixedOTP: FixedOTP, OTPSecret: "testkit-otp-secret", OTPResendSeconds: 0, OTPMaxSendsPerHour: 1000,
		OTPSender: httpapi.OTPSenderFunc(func(context.Context, string, string) error { return nil }),
		Maps:      httpapi.DemoMaps(), Log: quiet,
	}
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	env.BaseURL = srv.URL
	return env
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// UserID is a seeded (or signed-up) user's id by first name; uuid.Nil if unknown.
func (e *Env) UserID(name string) uuid.UUID {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.users[name]
}

// Sweep runs the sweeper once (close_offers, generate_rides, send_outbox).
func (e *Env) Sweep(t *testing.T) {
	t.Helper()
	if _, err := e.Sweeper.RunOnce(context.Background()); err != nil {
		t.Errorf("sweep: %v", err)
	}
}

// Advance moves the shared clock; nothing runs.
func (e *Env) Advance(t *testing.T, d time.Duration) {
	t.Helper()
	e.Clock.Advance(d)
}

// SignIn signs in a seeded user by first name with the fixed code.
func (e *Env) SignIn(t *testing.T, name string) *Client {
	t.Helper()
	id := e.UserID(name)
	if id == uuid.Nil {
		t.Fatalf("testkit: no seeded user %q", name)
	}
	var phone string
	if err := e.DB.QueryRow(context.Background(), "select phone from users where id = $1", id).Scan(&phone); err != nil {
		t.Fatal(err)
	}
	return e.signIn(t, phone)
}

// SignUp signs up a new rider with [phone] through the OTP flow.
func (e *Env) SignUp(t *testing.T, phone string) *Client {
	t.Helper()
	return e.signIn(t, phone)
}

func (e *Env) signIn(t *testing.T, phone string) *Client {
	t.Helper()
	c := &Client{base: e.BaseURL}
	if r := c.Do(t, "POST", "/v1/auth/otp", map[string]string{"phone": phone}); r.Status != 204 {
		t.Fatalf("testkit: otp for %s: %d %s", phone, r.Status, r.Body)
	}
	r := c.Do(t, "POST", "/v1/auth/verify", map[string]string{"phone": phone, "code": FixedOTP})
	if r.Status != 200 {
		t.Fatalf("testkit: verify %s: %d %s", phone, r.Status, r.Body)
	}
	var body struct {
		Token string `json:"token"`
		User  struct {
			ID   uuid.UUID `json:"id"`
			Name string    `json:"name"`
		} `json:"user"`
	}
	r.JSON(t, &body)
	c.Token = body.Token
	c.UserID = body.User.ID
	e.mu.Lock()
	if _, ok := e.names[body.User.ID]; !ok {
		e.names[body.User.ID] = phone
		e.users[phone] = body.User.ID
	}
	e.mu.Unlock()
	return c
}

// Client calls the API as one signed-in user.
type Client struct {
	Token  string
	UserID uuid.UUID
	base   string
}

// Resp is a response.
type Resp struct {
	Status int
	Body   []byte
}

// Do sends [body] as JSON (none when nil) and returns the response.
func (c *Client) Do(t *testing.T, method, path string, body any) *Resp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("authorization", "Bearer "+c.Token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return &Resp{Status: res.StatusCode, Body: b}
}

// JSON decodes the body into [v].
func (r *Resp) JSON(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("decode %s: %v", r.Body, err)
	}
}

// Map decodes the body as an object.
func (r *Resp) Map(t *testing.T) map[string]any {
	t.Helper()
	m := map[string]any{}
	r.JSON(t, &m)
	return m
}

// ---------------------------------------------------------------- pushes

// Message is one push as sent.
type Message struct {
	UserID uuid.UUID
	Title  string
	Body   string
	Data   map[string]string
}

// PushRecorder records every push the sweep sends.
type PushRecorder struct {
	mu   sync.Mutex
	msgs []Message
	env  *Env
}

// Push implements notify.Pusher.
func (p *PushRecorder) Push(_ context.Context, _ []string, m notify.Message) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.msgs = append(p.msgs, Message{UserID: m.UserID, Title: m.Title, Body: m.Body, Data: m.Data})
	return nil, nil
}

// To is the messages sent to a user (first name, or phone for a signed-up
// user), in order.
func (p *PushRecorder) To(name string) []Message {
	id := p.env.UserID(name)
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []Message
	for _, m := range p.msgs {
		if m.UserID == id {
			out = append(out, m)
		}
	}
	return out
}

// All is every message sent, in order.
func (p *PushRecorder) All() []Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.msgs)
}

// Reset forgets the messages so far.
func (p *PushRecorder) Reset() {
	p.mu.Lock()
	p.msgs = nil
	p.mu.Unlock()
}

// ---------------------------------------------------------------- shuffle

type fixedShuffle struct{ order []string }

func (f *fixedShuffle) Shuffle(c []offers.Candidate) {
	rank := func(name string) int {
		first := offers.FirstName(name)
		for i, o := range f.order {
			if strings.EqualFold(o, first) {
				return i
			}
		}
		return len(f.order)
	}
	slices.SortStableFunc(c, func(a, b offers.Candidate) int {
		if ra, rb := rank(a.Name), rank(b.Name); ra != rb {
			return ra - rb
		}
		return strings.Compare(a.Name, b.Name)
	})
}
