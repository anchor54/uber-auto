package integration

// Shared helpers for the R1+D2 integration tests. Tests talk to the real
// ride-api over HTTP (testkit.Client) and read Postgres directly (env.DB).
//
// Request/response shapes: the spec names fields but not their JSON layout, so
// every request body is built here, in one place, and responses are read with
// findKey/findObj (recursive lookups) so a wrapper object ({"series": {...}})
// does not break the tests.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"commute-backend/internal/testkit"
)

// ---------------------------------------------------------------- time ----

var ist = time.FixedZone("IST", 5*3600+30*60)

// t0 is the fake clock's start: Thu 19 Nov 2026 10:00 IST.
var t0 = time.Date(2026, 11, 19, 10, 0, 0, 0, ist)

const window = 15 * time.Minute

func istDate(y int, m time.Month, d int) string {
	return time.Date(y, m, d, 0, 0, 0, 0, ist).Format("2006-01-02")
}

// ---------------------------------------------------------------- places --

type place struct {
	Lat   float64 `json:"lat"`
	Lng   float64 `json:"lng"`
	Label string  `json:"label"`
}

// SPEC-AMBIGUITY: the spec gives no coordinates for Koramangala and
// Whitefield, only "Travel 51 min" (road = straight line x 1.4 at 18 km/h,
// rounded up). These points are 10.818 km apart (haversine, R = 6371 km):
// 15.145 km by road, 50.48 min -> 51. The seed's Whitefield-based rides
// (Ravi's 09:50 Ankit pickup) should use the same Whitefield point.
var (
	koramangala = place{Lat: 12.9352, Lng: 77.6245, Label: "Koramangala"}
	whitefield  = place{Lat: 12.9698, Lng: 77.7178, Label: "Whitefield"}
)

func straightKm(a, b place) float64 {
	const r = 6371.0
	p := math.Pi / 180
	dLat := (b.Lat - a.Lat) * p
	dLng := (b.Lng - a.Lng) * p
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(a.Lat*p)*math.Cos(b.Lat*p)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * r * math.Asin(math.Sqrt(h))
}

func roadKm(a, b place) float64 { return straightKm(a, b) * 1.4 }

// ---------------------------------------------------------------- requests -

// SPEC-AMBIGUITY: series.preview/create take "pickup, drop, weekdays, per-day
// times (pickup or arrive-by), return leg, ride type (+ usual payment)". We
// send them as a list of slots mirroring the series_slots columns
// (weekday 1=Mon..7=Sun ISO, direction outbound|return, timing
// pickup_at|arrive_by, time "HH:MM" local).
type slotReq struct {
	Weekday   int    `json:"weekday"`
	Direction string `json:"direction"`
	Timing    string `json:"timing"`
	Time      string `json:"time"`
}

type seriesReq struct {
	Pickup       place     `json:"pickup"`
	Drop         place     `json:"drop"`
	VehicleType  string    `json:"vehicle_type"`
	Preferences  []string  `json:"preferences"`
	Slots        []slotReq `json:"slots"`
	UsualPayment string    `json:"usual_payment,omitempty"`
}

// retryReq: series.retry's "optional new times or ride type".
type retryReq struct {
	Slots       []slotReq `json:"slots,omitempty"`
	VehicleType string    `json:"vehicle_type,omitempty"`
}

func slotsAt(hhmm string, weekdays ...int) []slotReq {
	var out []slotReq
	for _, d := range weekdays {
		out = append(out, slotReq{Weekday: d, Direction: "outbound", Timing: "pickup_at", Time: hhmm})
	}
	return out
}

// priyaReq is the spec's request: Koramangala -> Whitefield, auto,
// Mon/Wed/Fri 08:30, UPI.
func priyaReq() seriesReq {
	return seriesReq{
		Pickup:       koramangala,
		Drop:         whitefield,
		VehicleType:  "auto",
		Preferences:  []string{},
		Slots:        slotsAt("08:30", 1, 3, 5),
		UsualPayment: "upi",
	}
}

// ---------------------------------------------------------------- paths ----
// Operation names from the spec mapped with TEST_CONTRACT.md's REST convention.

const (
	pathPreview = "/v1/series/preview"
	pathSeries  = "/v1/series"
	pathOffers  = "/v1/offers"
)

func pathSeriesID(id string) string      { return "/v1/series/" + id }
func pathCancelRequest(id string) string { return "/v1/series/" + id + "/cancel-request" }
func pathRetry(id string) string         { return "/v1/series/" + id + "/retry" }
func pathOffer(id string) string         { return "/v1/offers/" + id }
func pathMarkInterested(id string) string {
	return "/v1/offers/" + id + "/mark-interested"
}
func pathWithdrawInterest(id string) string {
	return "/v1/offers/" + id + "/withdraw-interest"
}
func pathDismiss(id string) string { return "/v1/offers/" + id + "/dismiss" }

// SPEC-AMBIGUITY: rides.week takes "week start"; sent as ?start=YYYY-MM-DD.
func pathRidesWeek(start string) string { return "/v1/rides/week?start=" + start }

// ---------------------------------------------------------------- world ----

// world wraps an Env with cached signed-in clients.
type world struct {
	*testkit.Env
	mu      sync.Mutex
	clients map[string]*testkit.Client
}

func start(t *testing.T, opts ...testkit.Option) *world {
	t.Helper()
	return &world{Env: testkit.Start(t, opts...), clients: map[string]*testkit.Client{}}
}

func (w *world) as(t *testing.T, name string) *testkit.Client {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	if c, ok := w.clients[name]; ok {
		return c
	}
	c := w.SignIn(t, name)
	w.clients[name] = c
	return c
}

func (w *world) uid(name string) string { return w.UserID(name).String() }

// autoDrivers are the seeded live, paid auto drivers.
var autoDrivers = []string{"Ravi", "Meena", "Anil", "Suresh"}

// ---------------------------------------------------------------- HTTP -----

func expectStatus(t *testing.T, r *testkit.Resp, want ...int) {
	t.Helper()
	for _, w := range want {
		if r.Status == w {
			return
		}
	}
	t.Fatalf("status %d, want one of %v; body: %s", r.Status, want, r.Body)
}

// expectError checks status and the error body's code, and that the body
// carries the current "state" (spec: errors are {code, message, state}).
func expectError(t *testing.T, r *testkit.Resp, status int, code string) map[string]any {
	t.Helper()
	expectStatus(t, r, status)
	m := r.Map(t)
	if got, _ := m["code"].(string); got != code {
		t.Fatalf("error code %q, want %q; body: %s", got, code, r.Body)
	}
	if _, ok := m["message"]; !ok {
		t.Errorf("error body has no message: %s", r.Body)
	}
	return m
}

func expectErrorWithState(t *testing.T, r *testkit.Resp, status int, code string) map[string]any {
	t.Helper()
	m := expectError(t, r, status, code)
	if m["state"] == nil {
		t.Errorf("error body has no state: %s", r.Body)
	}
	return m
}

func anyJSON(t *testing.T, r *testkit.Resp) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(r.Body, &v); err != nil {
		t.Fatalf("bad JSON: %v; body: %s", err, r.Body)
	}
	return v
}

// findKey returns the first value under key anywhere in v (depth first).
func findKey(v any, key string) (any, bool) {
	switch x := v.(type) {
	case map[string]any:
		if val, ok := x[key]; ok {
			return val, true
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if val, ok := findKey(x[k], key); ok {
				return val, true
			}
		}
	case []any:
		for _, e := range x {
			if val, ok := findKey(e, key); ok {
				return val, true
			}
		}
	}
	return nil, false
}

// findObj returns the first object anywhere in v whose "id" equals id.
func findObj(v any, id string) map[string]any {
	switch x := v.(type) {
	case map[string]any:
		if s, _ := x["id"].(string); s == id {
			return x
		}
		for _, e := range x {
			if o := findObj(e, id); o != nil {
				return o
			}
		}
	case []any:
		for _, e := range x {
			if o := findObj(e, id); o != nil {
				return o
			}
		}
	}
	return nil
}

func num(t *testing.T, v any, what string) float64 {
	t.Helper()
	switch x := v.(type) {
	case float64:
		return x
	case string:
		var f float64
		if _, err := fmt.Sscan(x, &f); err == nil {
			return f
		}
	}
	t.Fatalf("%s: not a number: %#v", what, v)
	return 0
}

// seriesIDOf reads the series id from a series.create/retry/get response,
// either top-level {"id"} or wrapped {"series": {"id"}}.
func seriesIDOf(t *testing.T, r *testkit.Resp) string {
	t.Helper()
	m := r.Map(t)
	if s, ok := m["series"].(map[string]any); ok {
		if id, _ := s["id"].(string); id != "" {
			return id
		}
	}
	if id, _ := m["id"].(string); id != "" {
		return id
	}
	t.Fatalf("no series id in %s", r.Body)
	return ""
}

// seriesStatusOf reads series.status from a response, either top-level or
// wrapped in "series".
func seriesStatusOf(t *testing.T, r *testkit.Resp) string {
	t.Helper()
	m := r.Map(t)
	if s, ok := m["series"].(map[string]any); ok {
		if st, _ := s["status"].(string); st != "" {
			return st
		}
	}
	st, _ := m["status"].(string)
	return st
}

// createSeries posts series.create and returns the series id.
func (w *world) createSeries(t *testing.T, rider string, req seriesReq) (string, *testkit.Resp) {
	t.Helper()
	r := w.as(t, rider).Do(t, http.MethodPost, pathSeries, req)
	expectStatus(t, r, http.StatusOK, http.StatusCreated)
	return seriesIDOf(t, r), r
}

func (w *world) markInterested(t *testing.T, driver, offerID string) {
	t.Helper()
	r := w.as(t, driver).Do(t, http.MethodPost, pathMarkInterested(offerID), nil)
	expectStatus(t, r, http.StatusNoContent)
}

func (w *world) withdraw(t *testing.T, driver, offerID string) {
	t.Helper()
	r := w.as(t, driver).Do(t, http.MethodPost, pathWithdrawInterest(offerID), nil)
	expectStatus(t, r, http.StatusNoContent)
}

func (w *world) dismiss(t *testing.T, driver, offerID string) {
	t.Helper()
	r := w.as(t, driver).Do(t, http.MethodPost, pathDismiss(offerID), nil)
	expectStatus(t, r, http.StatusNoContent)
}

// closeWindow moves the clock to the current window's end and runs one sweep.
func (w *world) closeWindow(t *testing.T) {
	t.Helper()
	w.Advance(t, window)
	w.Sweep(t)
}

// ---------------------------------------------------------------- DB -------

func ctx() context.Context { return context.Background() }

func (w *world) one(t *testing.T, sql string, args []any, dest ...any) {
	t.Helper()
	if err := w.DB.QueryRow(ctx(), sql, args...).Scan(dest...); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
}

func (w *world) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	w.one(t, sql, args, &n)
	return n
}

func (w *world) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := w.DB.Exec(ctx(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

type seriesRow struct {
	Status, RiderID, StartDate, TimeZone, VehicleType, UsualPayment string
	TravelMinutes                                                   int
	SuggestedFare, DistanceKm                                       float64
	Version                                                         int
}

func (w *world) series(t *testing.T, id string) seriesRow {
	t.Helper()
	var s seriesRow
	w.one(t, `SELECT status::text, rider_id::text, start_date::text, time_zone,
	                 vehicle_type::text, coalesce(usual_payment::text, ''),
	                 travel_minutes, suggested_fare::float8, distance_km::float8, version
	            FROM series WHERE id = $1`, []any{id},
		&s.Status, &s.RiderID, &s.StartDate, &s.TimeZone, &s.VehicleType, &s.UsualPayment,
		&s.TravelMinutes, &s.SuggestedFare, &s.DistanceKm, &s.Version)
	return s
}

func (w *world) seriesStatus(t *testing.T, id string) string {
	t.Helper()
	var s string
	w.one(t, `SELECT status::text FROM series WHERE id = $1`, []any{id}, &s)
	return s
}

type slotRow struct {
	Weekday    int
	Direction  string
	PickupTime string // "HH:MM"
	DriverID   string // "" when null
	Active     bool
}

func (w *world) slots(t *testing.T, seriesID string) []slotRow {
	t.Helper()
	rows, err := w.DB.Query(ctx(), `
		SELECT weekday, direction::text, to_char(local_pickup_time, 'HH24:MI'),
		       coalesce(driver_id::text, ''), active
		  FROM series_slots WHERE series_id = $1
		 ORDER BY weekday, direction`, seriesID)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (slotRow, error) {
		var s slotRow
		err := r.Scan(&s.Weekday, &s.Direction, &s.PickupTime, &s.DriverID, &s.Active)
		return s, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

type offerRow struct {
	ID, Kind, Status, Outcome string
	Round                     int
	OpenedAt, ExpiresAt       time.Time
}

// offers returns every offer of a series, oldest first.
func (w *world) offers(t *testing.T, seriesID string) []offerRow {
	t.Helper()
	rows, err := w.DB.Query(ctx(), `
		SELECT id::text, kind::text, status::text, coalesce(outcome::text, ''),
		       round, opened_at, expires_at
		  FROM offers WHERE series_id = $1
		 ORDER BY opened_at, round`, seriesID)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (offerRow, error) {
		var o offerRow
		err := r.Scan(&o.ID, &o.Kind, &o.Status, &o.Outcome, &o.Round, &o.OpenedAt, &o.ExpiresAt)
		return o, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// openOffer returns the series' single open offer, failing if there isn't one.
func (w *world) openOffer(t *testing.T, seriesID string) offerRow {
	t.Helper()
	var open []offerRow
	for _, o := range w.offers(t, seriesID) {
		if o.Status == "open" {
			open = append(open, o)
		}
	}
	if len(open) != 1 {
		t.Fatalf("series %s: %d open offers, want 1: %+v", seriesID, len(open), w.offers(t, seriesID))
	}
	return open[0]
}

type recipRow struct {
	Interested, Withdrawn, Dismissed, Notified bool
	Result                                     string
}

// recipients maps driver user id -> recipient row for an offer.
func (w *world) recipients(t *testing.T, offerID string) map[string]recipRow {
	t.Helper()
	rows, err := w.DB.Query(ctx(), `
		SELECT driver_id::text, interested_at IS NOT NULL, withdrawn_at IS NOT NULL,
		       dismissed_at IS NOT NULL, notified_at IS NOT NULL, coalesce(result::text, '')
		  FROM offer_recipients WHERE offer_id = $1`, offerID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]recipRow{}
	for rows.Next() {
		var id string
		var r recipRow
		if err := rows.Scan(&id, &r.Interested, &r.Withdrawn, &r.Dismissed, &r.Notified, &r.Result); err != nil {
			t.Fatal(err)
		}
		out[id] = r
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// expectRecipients checks the offer's recipients are exactly these drivers.
func (w *world) expectRecipients(t *testing.T, offerID string, names ...string) {
	t.Helper()
	got := w.recipients(t, offerID)
	want := map[string]bool{}
	for _, n := range names {
		want[w.uid(n)] = true
		if _, ok := got[w.uid(n)]; !ok {
			t.Errorf("offer %s: %s is not a recipient", offerID, n)
		}
	}
	for id := range got {
		if !want[id] {
			t.Errorf("offer %s: unexpected recipient %s (%s)", offerID, id, w.driverName(t, id))
		}
	}
}

func (w *world) result(t *testing.T, offerID, driver string) string {
	t.Helper()
	r, ok := w.recipients(t, offerID)[w.uid(driver)]
	if !ok {
		t.Fatalf("offer %s: %s is not a recipient", offerID, driver)
	}
	return r.Result
}

func (w *world) driverName(t *testing.T, userID string) string {
	t.Helper()
	var n string
	if err := w.DB.QueryRow(ctx(), `SELECT name FROM drivers WHERE user_id = $1`, userID).Scan(&n); err != nil {
		return "?"
	}
	return n
}

// bikeDriver returns the first name of the seeded bike driver.
func (w *world) bikeDriver(t *testing.T) (firstName, userID string) {
	t.Helper()
	var name string
	w.one(t, `SELECT name, user_id::text FROM drivers WHERE vehicle_type::text = 'bike'
	           ORDER BY name LIMIT 1`, nil, &name, &userID)
	return strings.Fields(name)[0], userID
}

type rideRow struct {
	ID, LocalDate, Status, DriverID, SlotID string
	PickupAt, BusyUntil                     time.Time
}

func (w *world) rides(t *testing.T, seriesID string) []rideRow {
	t.Helper()
	rows, err := w.DB.Query(ctx(), `
		SELECT id::text, local_date::text, status::text, coalesce(driver_id::text, ''),
		       coalesce(slot_id::text, ''), pickup_at, busy_until
		  FROM rides WHERE series_id = $1 ORDER BY pickup_at`, seriesID)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (rideRow, error) {
		var x rideRow
		err := r.Scan(&x.ID, &x.LocalDate, &x.Status, &x.DriverID, &x.SlotID, &x.PickupAt, &x.BusyUntil)
		return x, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (w *world) eventsFor(t *testing.T, entityID string) int {
	t.Helper()
	return w.count(t, `SELECT count(*) FROM events WHERE entity_id = $1`, entityID)
}

// fareFor computes the suggested fare from fare_rules (base + per-km rate x
// road km) for a vehicle type. Columns are found by name (one containing
// "base", one containing "km") so the test doesn't depend on exact names.
func (w *world) fareFor(t *testing.T, vehicleType string, km float64) float64 {
	t.Helper()
	rows, err := w.DB.Query(ctx(), `SELECT row_to_json(f)::text FROM fare_rules f`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var js string
		if err := rows.Scan(&js); err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(js), &m); err != nil {
			t.Fatal(err)
		}
		match := false
		for _, v := range m {
			if s, ok := v.(string); ok && s == vehicleType {
				match = true
			}
		}
		if !match {
			continue
		}
		var base, perKm float64
		var haveBase, haveKm bool
		for k, v := range m {
			f, ok := v.(float64)
			if !ok {
				continue
			}
			lk := strings.ToLower(k)
			switch {
			case strings.Contains(lk, "base"):
				base, haveBase = f, true
			case strings.Contains(lk, "km"):
				perKm, haveKm = f, true
			}
		}
		if haveBase && haveKm {
			return base + perKm*km
		}
	}
	t.Fatalf("no fare_rules row for %s with base and per-km columns", vehicleType)
	return 0
}

// ---------------------------------------------------------------- pushes ---

func (w *world) pushes(name, substr string) int {
	n := 0
	for _, m := range w.Push.To(name) {
		if strings.Contains(m.Title+" "+m.Body, substr) {
			n++
		}
	}
	return n
}

func (w *world) expectPushes(t *testing.T, name, substr string, want int) {
	t.Helper()
	if got := w.pushes(name, substr); got != want {
		var all []string
		for _, m := range w.Push.To(name) {
			all = append(all, m.Title+" | "+m.Body)
		}
		t.Errorf("%s: %d pushes containing %q, want %d; all: %q", name, got, substr, want, all)
	}
}

// Push texts from the spec's Notifications table.
const (
	pushNewRequest = "New regular ride"
	pushStillOpen  = "Still open"
	pushRiderWin   = "Ravi will drive you"
	pushDriverWin  = "regular driver"
	pushNotPicked  = "went to another driver"
	pushWithdrew   = "rider withdrew"
	pushNoDriver   = "No driver yet"
)

// ---------------------------------------------------------------- flows ----

// assignRavi runs create -> Ravi interested -> window closes -> pick, and
// returns the active series id. Needs no shuffle beyond Ravi being the only
// interested driver.
func (w *world) assignRavi(t *testing.T) string {
	t.Helper()
	id, _ := w.createSeries(t, "Priya", priyaReq())
	off := w.openOffer(t, id)
	w.markInterested(t, "Ravi", off.ID)
	w.closeWindow(t)
	if st := w.seriesStatus(t, id); st != "active" {
		t.Fatalf("series %s is %s after the pick, want active", id, st)
	}
	return id
}

// pgCode returns the SQLSTATE of a Postgres error, or "".
func pgCode(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}
