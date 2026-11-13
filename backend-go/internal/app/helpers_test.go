package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"commute-backend/internal/db"
	"commute-backend/internal/domain"
)

// The integration tests run against real Postgres: DATABASE_URL, or
// postgres://app:app@localhost:5432/commute_test. Every test starts from a
// freshly seeded demo city, so they run one at a time.

const demoOTP = "123456"

// Wednesday 21 Oct 2026, 9:00 AM IST: the same moment the app's tests use.
var testNow = time.Date(2026, 10, 21, 3, 30, 0, 0, time.UTC)

var shared *pgxpool.Pool

func TestMain(m *testing.M) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://app:app@localhost:5432/commute_test"
	}
	pool, err := db.Open(context.Background(), url)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	shared = pool
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

type sentCode struct{ phone, code string }

type testApp struct {
	t         *testing.T
	app       *App
	h         http.Handler
	now       time.Time
	sentCodes []sentCode
}

// startTestApp seeds the demo city and builds the API with a test clock.
func startTestApp(t *testing.T, configure ...func(*Config)) *testApp {
	t.Helper()
	ctx := context.Background()
	if err := db.SeedDemoData(ctx, shared); err != nil {
		t.Fatal(err)
	}
	ta := &testApp{t: t, now: testNow}
	config := DefaultConfig
	config.FixedOTP = demoOTP
	// Tests sign the same phone in many times; the send limits have their own tests.
	config.OTPResendSeconds = 0
	config.OTPMaxSendsPerHour = 1000
	for _, f := range configure {
		f(&config)
	}
	ta.app = &App{
		DB:       shared,
		Config:   config,
		Clock:    func() time.Time { return ta.now },
		SetClock: func(now time.Time) { ta.now = now },
		OTPSender: OTPSenderFunc(func(_ context.Context, phone, code string) error {
			ta.sentCodes = append(ta.sentCodes, sentCode{phone, code})
			return nil
		}),
	}
	ta.h = ta.app.Handler()
	return ta
}

func (ta *testApp) later(d time.Duration) { ta.now = ta.now.Add(d) }

// ---- requests ----

type resp struct {
	t      *testing.T
	status int
	header http.Header
	body   []byte
}

func (ta *testApp) do(method, url string, body any, headers map[string]string) *resp {
	ta.t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			ta.t.Fatal(err)
		}
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, url, reader)
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if h, ok := headers["host"]; ok {
		req.Host = h
	}
	rec := httptest.NewRecorder()
	ta.h.ServeHTTP(rec, req)
	return &resp{ta.t, rec.Code, rec.Header(), rec.Body.Bytes()}
}

func bearer(token string) map[string]string {
	if token == "" {
		return nil
	}
	return map[string]string{"authorization": "Bearer " + token}
}

func (ta *testApp) post(url string, body any, token string) *resp {
	ta.t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	return ta.do("POST", url, body, bearer(token))
}

func (ta *testApp) put(url string, body any, token string) *resp {
	ta.t.Helper()
	return ta.do("PUT", url, body, bearer(token))
}

func (ta *testApp) get(url, token string) *resp {
	ta.t.Helper()
	return ta.do("GET", url, nil, bearer(token))
}

// signIn signs a rider in with the fixed code and returns their token.
func (ta *testApp) signIn(phone string) string {
	ta.t.Helper()
	if phone == "" {
		phone = "9886054321"
	}
	ta.post("/v1/auth/otp", map[string]string{"phone": phone}, "")
	return ta.post("/v1/auth/verify", map[string]string{"phone": phone, "code": demoOTP}, "").str("token")
}

// json decodes the body.
func (r *resp) json() any {
	r.t.Helper()
	var v any
	if err := json.Unmarshal(r.body, &v); err != nil {
		r.t.Fatalf("not JSON (%d): %s", r.status, r.body)
	}
	return v
}

// at follows a dotted path ("proposals.0.driver.name") through the body.
func (r *resp) at(path string) any {
	r.t.Helper()
	return pathIn(r.json(), path)
}

func pathIn(v any, path string) any {
	if path == "" {
		return v
	}
	for _, k := range strings.Split(path, ".") {
		switch c := v.(type) {
		case map[string]any:
			v = c[k]
		case []any:
			i, err := strconv.Atoi(k)
			if err != nil || i >= len(c) {
				return nil
			}
			v = c[i]
		default:
			return nil
		}
	}
	return v
}

func (r *resp) str(path string) string {
	r.t.Helper()
	s, _ := r.at(path).(string)
	return s
}

func (r *resp) num(path string) float64 {
	r.t.Helper()
	n, _ := r.at(path).(float64)
	return n
}

func (r *resp) list(path string) []any {
	r.t.Helper()
	l, _ := r.at(path).([]any)
	return l
}

func (r *resp) errCode() string { return r.str("error") }

// pluck is field [key] of every item in a JSON list.
func pluck(list []any, key string) []any {
	out := []any{}
	for _, item := range list {
		out = append(out, pathIn(item, key))
	}
	return out
}

// ---- assertions ----

func eq(t *testing.T, got, want any, what ...any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v\n got: %#v\nwant: %#v", fmt.Sprint(what...), got, want)
	}
}

func status(t *testing.T, r *resp, want int) {
	t.Helper()
	if r.status != want {
		t.Fatalf("status %d, want %d: %s", r.status, want, r.body)
	}
}

// matches checks that every key in [want] has that value in the JSON object [got].
func matches(t *testing.T, got any, want map[string]any) {
	t.Helper()
	for k, w := range want {
		g := pathIn(got, k)
		if sub, ok := w.(map[string]any); ok {
			matches(t, g, sub)
			continue
		}
		if !reflect.DeepEqual(g, normalise(w)) {
			t.Fatalf("%s: got %#v, want %#v", k, g, w)
		}
	}
}

// normalise turns Go values into what JSON decoding gives (numbers as float64).
func normalise(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	json.Unmarshal(b, &out)
	return out
}

// ---- data ----

func place(id string) domain.Place { return db.DemoPlace(id) }

func commuteRequest(overrides map[string]any) map[string]any {
	r := map[string]any{
		"pickup":      place("home"),
		"drop":        place("etv"),
		"repeat":      "every_weekday",
		"weekdays":    []int{1, 2, 3, 4, 5},
		"timing":      "pickup_at",
		"time":        520,
		"returnTrip":  true,
		"returnTime":  1110,
		"rideType":    "cab_sedan",
		"preferences": []string{"ac_on"},
	}
	for k, v := range overrides {
		r[k] = v
	}
	return r
}

func (ta *testApp) exec(sql string, args ...any) {
	ta.t.Helper()
	if _, err := shared.Exec(context.Background(), sql, args...); err != nil {
		ta.t.Fatal(err)
	}
}

func (ta *testApp) count(sql string, args ...any) int {
	ta.t.Helper()
	var n int
	if err := shared.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		ta.t.Fatal(err)
	}
	return n
}

func (ta *testApp) strings(sql string, args ...any) []string {
	ta.t.Helper()
	rows, err := shared.Query(context.Background(), sql, args...)
	if err != nil {
		ta.t.Fatal(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			ta.t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func (ta *testApp) pin(rideID string) string {
	ta.t.Helper()
	return ta.strings("select pin from rides where id = $1", rideID)[0]
}

// ---- fake HTTP for outside services ----

type fakeCall struct {
	req  *http.Request
	body string
}

type fakeHTTP struct {
	calls []fakeCall
	reply func(r *http.Request) (int, any, error)
}

func (f *fakeHTTP) RoundTrip(r *http.Request) (*http.Response, error) {
	var body string
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
	}
	f.calls = append(f.calls, fakeCall{r, body})
	code, v, err := f.reply(r)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(v)
	return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(bytes.NewReader(raw)), Request: r}, nil
}

func (f *fakeHTTP) client() *http.Client { return &http.Client{Transport: f} }

// fakeMaps answers estimates with [estimate] and records where they start.
type fakeMaps struct {
	asked    []domain.LatLng
	estimate func(from, to domain.LatLng) domain.TripEstimate
}

func (f *fakeMaps) Name() string { return "fake" }

func (f *fakeMaps) SearchPlaces(context.Context, string, CityArea) ([]domain.Place, error) {
	return []domain.Place{}, nil
}

func (f *fakeMaps) EstimateTrip(_ context.Context, from, to domain.LatLng) (domain.TripEstimate, error) {
	f.asked = append(f.asked, from)
	return f.estimate(from, to), nil
}
