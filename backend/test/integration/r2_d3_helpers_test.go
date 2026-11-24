package integration

// Helpers for the R2+D3 "The Daily Ride" integration tests (r2_d3_*_test.go).
//
// ============================================================================
// CONTRACT FOR THE IMPLEMENTER (paths and JSON chosen by the R2+D3 test writer)
// ============================================================================
//
// HLD overrides of the spec (marked `// HLD:` in the tests):
//   - ride states: assigned, needs_driver, on_the_way, completed, rider_no_show,
//     skipped, failed, cancelled. No confirmed state.
//   - NO PIN STEP for now: ride.start needs no PIN (no 422 wrong_pin); the
//     tests send an empty body. They never assert a PIN value.
//   - the 20:00 message is a reminder only (no confirm tap).
//   - on_my_way_check (pickup - 15 min) is the first automatic check; every
//     timed check has a too-late limit -> failed with fail_reason + admin push.
//   - the sweeper derives due work from pickup_at offsets plus marker columns
//     (reminder_sent_at, ...).
//
// Seed: testkit.WithSeed("r2") = seed.Config plus the spec's R2 seed:
//   - rider Priya (Priya Sharma) with ACTIVE series S-17: auto, Koramangala
//     (12.9352, 77.6245) -> Whitefield (12.9698, 77.7178), Mon/Wed/Fri 08:30
//     outbound, pickup_at timing, Ravi on every slot, travel 51 min,
//     suggested_fare from the fare rule (197), start date on or before 2 Nov;
//   - rider Ankit with an ACTIVE series driven by Ravi every weekday (Mon-Fri)
//     at 09:50 from Whitefield (to Indiranagar), as in the spec seed;
//   - live, paid auto drivers Ravi, Suresh, Anil, Meena (as seed.Spec; Meena
//     WITHOUT Kavya's Wednesday series, Kavya may be omitted); bike driver
//     Vijay may stay;
//   - the admin "Admin" (seed.Admin), so Push.To("Admin") works;
//   - NO rides rows: rides come only from the sweeper's generate_rides. The
//     tests Set the clock themselves (to Mon 16 Nov 2026 02:00 IST or later),
//     so nothing in the seed may depend on the clock being Thu 19 Nov.
//
// Sweeper (Env.Sweep = RunOnce) also runs: generate_rides (today .. today+14,
// today included), evening_reminder, on_my_way_check, close_stale_rides,
// send_outbox (as before). Admin alerts are pushes to every admin.
//
// Paths (rider session for /v1/rides/..., driver session for /v1/driver/...;
// the wrong role gets 403; another driver's ride 403 or 404):
//   GET  /v1/rides/next                          rides.next
//        -> 200 {"rides": [ <ride>, ... ]} one per active series; each ride has
//           "id", "status", "pickup_at", and "driver": {"name", ...} (or null)
//   GET  /v1/rides/{id}/live                     ride.live
//        -> 200 {"ride_id", "status", "position": null | {"lat","lng",
//           "accuracy_m","recorded_at","age_seconds"}, "eta_minutes": null|int}
//           position (and eta) only while on_the_way; 403 another rider's ride
//           (or a driver), 404 unknown ride
//   POST /v1/rides/{id}/running-late             ride.running_late -> 204;
//        409 already_sent, 409 wrong_status (not on_the_way)
//   POST /v1/rides/{id}/paid   {"mark": "upi"|"cash"}           ride.rider_paid
//        -> 200 the ride; 409 not_completed
//   POST /v1/rides/{id}/rate   {"stars":1..5,"tags":[...],"note"} ride.rate
//        -> 200 the ride; 409 not_completed, 409 already_rated
//   GET  /v1/driver/run?from=YYYY-MM-DD&days=N   rides.schedule (days max 14)
//        -> 200 {"rides": [ <ride with rider name, route, times, status>, ... ]}
//           in pickup order; only rides whose driver_id is the caller
//   POST /v1/driver/rides/{id}/on-my-way          ride.on_my_way -> 200 the ride
//        (on_the_way); 409 too_early, 409 ride_reassigned, 409 wrong_status
//   POST /v1/driver/rides/{id}/location {"lat","lng","accuracy"} -> 204;
//        409 not_on_the_way
//   POST /v1/driver/rides/{id}/arrived            -> 200 the ride; 409 wrong_status
//   POST /v1/driver/rides/{id}/start   {}         -> 200 the ride with picked_up_at;
//        409 wrong_status
//   POST /v1/driver/rides/{id}/end                ride.complete -> 200 (completed);
//        409 not_started, 409 wrong_status
//   POST /v1/driver/rides/{id}/no-show            -> 200 (rider_no_show);
//        409 too_soon with "opens_at" (UTC RFC 3339; top level or in "state"),
//        409 wrong_status
//   POST /v1/driver/rides/{id}/payment {"mark": "upi"|"cash"|"not_paid"}
//        ride.driver_paid -> 200; 409 not_completed, 409 too_late (after local midnight)
//   POST /v1/driver/rides/{id}/flag    {"tags": [...]}  ride.flag_rider -> 200;
//        409 not_completed
//   POST /v1/driver/rides/{id}/cant-make-it {"reason": "vehicle"}  ride.give_up
//        -> 200 the ride (needs_driver); 409 wrong_status, 409 too_late
// "the ride" = a ride object (top level, or wrapped as {"ride": {...}}) with at
// least "id" and "status". Every 409 carries the ride as it is now in "state".
//
// DB (spec Table changes): rides.on_the_way_at, arrived_at, picked_up_at,
// completed_at, late_notice_at (timestamptz), give_up_reason, fail_reason
// ('no_driver' | 'missed_by_outage'), driver_paid_mark, rider_paid_mark,
// rider_stars, rider_tags, rider_note, driver_flags, reminder_sent_at;
// driver_locations(driver_id, lat, lng, accuracy_m, recorded_at).
// One-ride offers: offers.kind = 'ride', ride_id set, round 1, 5-minute window,
// recipients in offer_recipients. Stale close writes an events row for the
// ride whose type or data mentions "auto_closed".
//
// Pushes (substrings checked):
//   Ravi  reminder  "Tomorrow: 2 rides" + "First at 08:30, Priya, Koramangala → Whitefield";
//                   data references the ride ids
//   Priya on my way "Ravi is on his way"; arrived "Ravi has arrived at Koramangala";
//         completed "You've arrived" + "₹197"; no-show "waited 10 minutes";
//         check "hasn't started yet"; give up "can't make your 08:30 ride"
//   Ravi  running late "Priya is running about 5 minutes late"
//   Admin "missed by outage", "closed automatically"
// ============================================================================

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"commute-backend/internal/testkit"
)

// ---------------------------------------------------------------- paths ----

const (
	pathRidesNext = "/v1/rides/next"
	pathDrvRun    = "/v1/driver/run"
)

func pathRideLive(id string) string        { return "/v1/rides/" + id + "/live" }
func pathRideRunningLate(id string) string { return "/v1/rides/" + id + "/running-late" }
func pathRideRiderPaid(id string) string   { return "/v1/rides/" + id + "/paid" }
func pathRideRate(id string) string        { return "/v1/rides/" + id + "/rate" }

func pathDrvRunFrom(from string, days int) string {
	return fmt.Sprintf("%s?from=%s&days=%d", pathDrvRun, from, days)
}
func pathDrvRide(id, action string) string { return "/v1/driver/rides/" + id + "/" + action }

// Driver ride actions (the last path segment).
const (
	actOnMyWay  = "on-my-way"
	actLocation = "location"
	actArrived  = "arrived"
	actStart    = "start"
	actEnd      = "end"
	actNoShow   = "no-show"
	actPayment  = "payment"
	actFlag     = "flag"
	actGiveUp   = "cant-make-it"
)

// ---------------------------------------------------------------- pushes ---

const (
	pushR2OnTheWay    = "Ravi is on his way"
	pushR2Arrived     = "Ravi has arrived at Koramangala"
	pushR2Completed   = "You've arrived"
	pushR2NoShow      = "waited 10 minutes"
	pushR2NotStarted  = "hasn't started yet"
	pushR2GaveUp      = "can't make your 08:30 ride"
	pushR2RunningLate = "Priya is running about 5 minutes late"
	pushR2Outage      = "missed by outage"
	pushR2AutoClosed  = "closed automatically"
	pushR2Tomorrow    = "Tomorrow:"
)

// ---------------------------------------------------------------- world ----

type r2world struct{ *world }

func startR2(t *testing.T, opts ...testkit.Option) *r2world {
	t.Helper()
	w := &r2world{start(t, append([]testkit.Option{testkit.WithSeed("r2")}, opts...)...)}
	if n := w.count(t, `SELECT count(*) FROM rides`); n != 0 {
		t.Fatalf("seed r2 must not insert rides (generate_rides makes them); found %d", n)
	}
	return w
}

// at is a local (IST) time in Nov 2026; day > 30 rolls into December.
func at(day, hh, mm int) time.Time { return time.Date(2026, 11, day, hh, mm, 0, 0, ist) }

// setAt moves the shared clock (no sweep).
func (w *r2world) setAt(day, hh, mm int) { w.Clock.Set(at(day, hh, mm)) }

// prepare generates rides as of [day] 02:00 IST (no pickup has passed yet, so
// no check fires), then forgets any pushes.
func (w *r2world) prepare(t *testing.T, day int) {
	t.Helper()
	w.setAt(day, 2, 0)
	w.Sweep(t)
	w.Push.Reset()
}

// rideOn returns the id of [rider]'s first ride on [date] (YYYY-MM-DD).
func (w *r2world) rideOn(t *testing.T, rider, date string) string {
	t.Helper()
	var id string
	if err := w.DB.QueryRow(ctx(), `SELECT id::text FROM rides WHERE rider_id = $1 AND local_date = $2::date
		ORDER BY pickup_at LIMIT 1`, w.uid(rider), date).Scan(&id); err != nil {
		t.Fatalf("no ride for %s on %s: %v", rider, date, err)
	}
	return id
}

// r102 prepares Wednesday 18 Nov and returns Priya's 08:30 ride (R-102).
func (w *r2world) r102(t *testing.T) string {
	t.Helper()
	w.prepare(t, 18)
	return w.rideOn(t, "Priya", "2026-11-18")
}

// ---------------------------------------------------------------- DB rows --

// r2Ride is a rides row as JSON (row_to_json), so new columns are read by name.
type r2Ride map[string]any

func (w *r2world) ride(t *testing.T, id string) r2Ride {
	t.Helper()
	var js string
	w.one(t, `SELECT row_to_json(r)::text FROM rides r WHERE id = $1`, []any{id}, &js)
	m := r2Ride{}
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func (r r2Ride) str(k string) string {
	switch v := r[k].(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

func (r r2Ride) null(k string) bool { return r[k] == nil }

// time reads a timestamptz column; fails when null or absent.
func (r r2Ride) time(t *testing.T, k string) time.Time {
	t.Helper()
	s, ok := r[k].(string)
	if !ok {
		t.Fatalf("rides.%s is %#v, want a timestamp", k, r[k])
	}
	tm, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		// Postgres may print "+05:30" or "+00"; normalise.
		if tm, err = time.Parse("2006-01-02T15:04:05.999999999-07", s); err != nil {
			t.Fatalf("rides.%s: bad time %q", k, s)
		}
	}
	return tm
}

func (w *r2world) expectRideStatus(t *testing.T, id, want string) r2Ride {
	t.Helper()
	r := w.ride(t, id)
	if got := r.str("status"); got != want {
		t.Errorf("ride %s status %q, want %q (row: %v)", id, got, want, r)
	}
	return r
}

func (w *r2world) expectRideTime(t *testing.T, r r2Ride, col string, want time.Time) {
	t.Helper()
	if got := r.time(t, col); !got.Equal(want) {
		t.Errorf("rides.%s = %s, want %s", col, got.In(ist), want.In(ist))
	}
}

// rideOffers returns the one-ride offers of a ride, oldest first.
func (w *r2world) rideOffers(t *testing.T, rideID string) []offerRow {
	t.Helper()
	rows, err := w.DB.Query(ctx(), `
		SELECT id::text, kind::text, status::text, coalesce(outcome::text, ''),
		       round, opened_at, expires_at
		  FROM offers WHERE ride_id = $1
		 ORDER BY opened_at, round`, rideID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []offerRow
	for rows.Next() {
		var o offerRow
		if err := rows.Scan(&o.ID, &o.Kind, &o.Status, &o.Outcome, &o.Round, &o.OpenedAt, &o.ExpiresAt); err != nil {
			t.Fatal(err)
		}
		out = append(out, o)
	}
	return out
}

// ---------------------------------------------------------------- HTTP -----

// drv calls a driver ride action as [name].
func (w *r2world) drv(t *testing.T, name, rideID, action string, body any) *testkit.Resp {
	t.Helper()
	if body == nil && action != actLocation {
		body = map[string]any{}
	}
	return w.as(t, name).Do(t, http.MethodPost, pathDrvRide(rideID, action), body)
}

// drvOK calls a driver action that must succeed and returns the ride object.
func (w *r2world) drvOK(t *testing.T, name, rideID, action string, body any) map[string]any {
	t.Helper()
	r := w.drv(t, name, rideID, action, body)
	expectStatus(t, r, http.StatusOK, http.StatusNoContent)
	if r.Status == http.StatusNoContent || len(r.Body) == 0 {
		return nil
	}
	return rideObjOf(t, r)
}

func locBody(lat, lng float64) map[string]any {
	return map[string]any{"lat": lat, "lng": lng, "accuracy": 12}
}

func (w *r2world) live(t *testing.T, rider, rideID string) (*testkit.Resp, map[string]any) {
	t.Helper()
	r := w.as(t, rider).Do(t, http.MethodGet, pathRideLive(rideID), nil)
	if r.Status != http.StatusOK {
		return r, nil
	}
	return r, r.Map(t)
}

// rideObjOf reads the ride object from a response: {"ride": {...}} or top level.
func rideObjOf(t *testing.T, r *testkit.Resp) map[string]any {
	t.Helper()
	m := r.Map(t)
	if x, ok := m["ride"].(map[string]any); ok {
		return x
	}
	return m
}

func strOf(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	s, _ := m[k].(string)
	return s
}

// position reads ride.live's driver position; nil when absent or null.
func position(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	p, _ := m["position"].(map[string]any)
	return p
}

// toCompleted runs R-102 through On my way 08:09, arrived 08:29, start 08:31,
// end 09:22 (the spec's happy path, no locations).
func (w *r2world) toCompleted(t *testing.T, id string) {
	t.Helper()
	w.setAt(18, 8, 9)
	w.drvOK(t, "Ravi", id, actOnMyWay, nil)
	w.setAt(18, 8, 29)
	w.drvOK(t, "Ravi", id, actArrived, nil)
	w.setAt(18, 8, 31)
	w.drvOK(t, "Ravi", id, actStart, nil) // HLD: no PIN step
	w.setAt(18, 9, 22)
	w.drvOK(t, "Ravi", id, actEnd, nil)
}

// expectOpensAt checks a 409 too_soon body carries "opens_at" (top level or
// in state) equal to [want].
func expectOpensAt(t *testing.T, m map[string]any, want time.Time) {
	t.Helper()
	v, ok := m["opens_at"]
	if !ok {
		if st, _ := m["state"].(map[string]any); st != nil {
			v, ok = st["opens_at"]
		}
	}
	s, _ := v.(string)
	if !ok || s == "" {
		t.Errorf("too_soon has no opens_at: %v", m)
		return
	}
	got, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Errorf("opens_at %q: %v", s, err)
		return
	}
	if !got.Equal(want) {
		t.Errorf("opens_at %s, want %s", got.In(ist), want.In(ist))
	}
}

// pushTextsTo lists a user's pushes as "title | body".
func (w *r2world) pushTextsTo(name string) []string {
	var out []string
	for _, m := range w.Push.To(name) {
		out = append(out, m.Title+" | "+m.Body)
	}
	return out
}

func (w *r2world) expectSomePush(t *testing.T, name, substr string) {
	t.Helper()
	if w.pushes(name, substr) == 0 {
		t.Errorf("%s: no push containing %q; got %q", name, substr, w.pushTextsTo(name))
	}
}

// fare is R-102's fare snapshot as "₹197".
func (w *r2world) fareText(t *testing.T, id string) string {
	t.Helper()
	return "₹" + strings.TrimSuffix(w.ride(t, id).str("fare_snapshot"), ".0")
}
