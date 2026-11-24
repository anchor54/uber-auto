# Integration test harness contract

Fixed by the orchestrator so the test-writer and implementer subagents can work in parallel.
The implementer owns `internal/testkit`; test writers only use it. Extend it (add methods) when a
new feature needs more; never change an existing signature without updating every test.

Tests live in `backend/test/integration/<feature>_test.go`, package `integration`, one test per
spec scenario, named after the spec id (e.g. `TestR1_I1_Preview`).

```go
package testkit

// Start runs the real ride-api handlers in-process on a random port, against a fresh
// Postgres schema (all migrations applied) on DATABASE_URL (default
// postgres://app:app@localhost:5432/commute_test). Cleans up on t.Cleanup.
func Start(t *testing.T, opts ...Option) *Env

type Option func(*config)
func WithSeed(name string) Option      // "spec" (R1+D2, default), "d1", "r2" (R2+D3: Priya's and Ankit's active series with Ravi, admin, no rides), "empty"
func WithShuffle(order ...string) Option // fixed shuffle: driver first names in pick order

type Env struct {
    BaseURL string
    DB      *pgxpool.Pool          // read the DB to assert results
    Clock   *clock.Fake            // shared by API and sweeper; starts Thu 19 Nov 2026 10:00 IST
    Push    *PushRecorder          // every notification sent
}

func (e *Env) SignIn(t *testing.T, name string) *Client   // seeded user by first name ("Priya", "Ravi"); fixed OTP
func (e *Env) SignUp(t *testing.T, phone string) *Client  // new user via OTP flow
func (e *Env) Sweep(t *testing.T)                          // sweeper.RunOnce(ctx) once
func (e *Env) Advance(t *testing.T, d time.Duration)       // move the clock, no sweep
func (e *Env) UserID(name string) uuid.UUID

type Client struct{ Token string /* ... */ }
func (c *Client) Do(t *testing.T, method, path string, body any) *Resp // JSON in, JSON out
type Resp struct { Status int; Body []byte }
func (r *Resp) JSON(t *testing.T, v any)
func (r *Resp) Map(t *testing.T) map[string]any

type PushRecorder struct{}
func (p *PushRecorder) To(name string) []Message   // messages for a seeded user, in order
func (p *PushRecorder) Reset()
type Message struct { UserID uuid.UUID; Title, Body string; Data map[string]string }
```

Endpoint paths, request and response JSON follow the spec's Endpoints tables exactly. Where the
spec names an operation (e.g. `series.preview`) without a path, use `POST /v1/series/preview`,
`POST /v1/series`, `GET /v1/series/:id`, `POST /v1/series/:id/cancel-request`, etc. (REST, kebab-case),
and the implementer documents the final list in `backend/API.md`.
