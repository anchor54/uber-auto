package integration

// Helpers for the D1 "Driver Registration, Verification and Subscription"
// integration tests (d1_*_test.go).
//
// ============================================================================
// CONTRACT FOR THE IMPLEMENTER (paths and JSON chosen by the D1 test writer)
// ============================================================================
//
// Seed: testkit.WithSeed("d1") = seed.Config (ride types, fare rules) plus
//   - one `plans` row per vehicle type / cab class with a monthly_price
//     (auto: cab_class NULL), and app_config cities = Bengaluru ("blr");
//   - rider "Priya" (Priya Sharma, a riders row), signed in with w.as("Priya");
//   - admin "Admin" (users.role = 'admin'), signed in with w.as("Admin") through
//     the normal POST /v1/auth/otp + /v1/auth/verify (a known number signs in
//     with its role, so the admin's session has role admin);
//   - no drivers. The clock starts at seed.SpecNow; the tests Set it to
//     Sat 21 Nov 2026 10:00 IST themselves.
//   Push.To("Admin") / Push.To("Priya") must work (names registered in Env).
//
// Driver ids in admin paths are the driver's user id (drivers.user_id).
// Weekdays are ISO (1 = Mon .. 7 = Sun), dates YYYY-MM-DD (IST local date).
//
// Sign-up (auth.start / auth.verify, driver app):
//   POST /v1/driver/auth/otp     {"phone"}                 -> 204
//   POST /v1/driver/auth/verify  {"phone","code"}          -> 200
//        {"token", "user": {"id", ...}, "driver_exists": bool}
//        A NEW number now gets a driver session (no drivers row yet,
//        driver_exists false). 401 wrong_code on a bad code.
// Driver:
//   POST /v1/driver/signup        {"name","city":"blr","terms_accepted":true}
//        -> 201 (or 200) the driver: {"id","status":"signing_up","reference":"DRV-12345",...}
//        409 phone_in_use_as_rider, 409 already_registered
//   GET  /v1/driver               driver.get: {"status","reference","reject_reason",
//        "items":[{"kind","state","reject_reason"}...], "vehicle":{...},
//        "day_off", "plan", "latest_payment":{"id","status","reject_reason",...}}
//        ("status" top level, or inside a "driver" object)
//   POST /v1/driver/upload-url    {"kind","content_type":"image/jpeg"}
//        -> 200 {"upload_url","storage_path"}; storage_path is exactly
//        "driver-docs/{driver_id}/{kind}". When upload_url is http(s) the tests
//        PUT the JPEG bytes to it and expect 2xx (testkit's in-memory storage
//        should serve it on the test server). 409 locked.
//   PUT  /v1/driver/documents/{kind}  {"storage_path"}   (driver.item_uploaded,
//        also used for vehicle_front / vehicle_back / vehicle_interior)
//        -> 200 the item {"kind","state":"uploaded"}; 403 not_your_path, 409 locked
//   PUT  /v1/driver/upi           {"upi_id"} -> 200; 422 bad_upi, 409 locked
//   PUT  /v1/driver/vehicle       {"vehicle_type" (alias "type"),"cab_class","make_model",
//        "year","plate","colour","ac","cng_or_electric"} -> 200; 409 plate_in_use, 409 locked
//   PUT  /v1/driver/day-off       {"day_off": 1..7 | null} -> 200; 409 locked
//   POST /v1/driver/submit        -> 200 driver (submitted);
//        409 incomplete with "missing":[kinds...] (also accepted inside "state"),
//        409 wrong_status
//   GET  /v1/driver/plan          plans.get -> 200 {"plan":{"vehicle_type","cab_class",
//        "monthly_price"}, "amount_due", "period_start", "period_end", ...}; 409 not_approved
//   POST /v1/driver/payments      {"period_start"} driver.claim_payment
//        -> 200/201 payment (claimed); 409 already_claimed, 409 not_approved
// Admin (role admin; anyone else 403):
//   GET  /v1/admin/pending        {"drivers":[{"id",...}], "payments":[{"id",...}]} oldest first
//   GET  /v1/admin/drivers/{id}   admin.show_driver: {"items":[{"kind","state","url",...}],...}; 404
//   POST /v1/admin/drivers/{id}/review-item {"item","state":"verified"|"rejected","reason"}
//        -> 200 the item; 409 wrong_status
//   POST /v1/admin/drivers/{id}/approve  -> 200 driver; 409 items_not_verified, 409 wrong_status
//   POST /v1/admin/drivers/{id}/reject   {"reason"} -> 200 driver; 409 wrong_status
//   POST /v1/admin/payments/{id}/confirm -> 200 {payment, driver}; 409 wrong_status
//   POST /v1/admin/payments/{id}/reject  {"reason"} -> 200; 409 wrong_status
//
// Review items (admin "item" names): the nine driver_items kinds
// (profile_photo, id_proof, licence, rc, insurance, permit, vehicle_front,
// vehicle_back, vehicle_interior) plus "upi" and "vehicle" (vehicles.review_state).
//
// DB (spec Tables): drivers(user_id, status, reject_reason, reference, upi_id,
// day_off, subscription_until), vehicles(driver_id, type, plate, review_state),
// driver_items(driver_id, kind, storage_path, state, reject_reason),
// plans(vehicle_type, cab_class, monthly_price),
// subscription_payments(id, driver_id, period_start, period_end, amount, status,
// reject_reason, claimed_at). Events as in R1+D2: entity_id = driver user id or
// payment id, data {"old_status","new_status"} on every status change.
// ============================================================================

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"commute-backend/internal/testkit"
)

// ---------------------------------------------------------------- paths ----

const (
	pathDriverOTP      = "/v1/driver/auth/otp"
	pathDriverVerify   = "/v1/driver/auth/verify"
	pathDriverSignup   = "/v1/driver/signup"
	pathDriver         = "/v1/driver"
	pathDriverUpload   = "/v1/driver/upload-url"
	pathDriverUPI      = "/v1/driver/upi"
	pathDriverVehicle  = "/v1/driver/vehicle"
	pathDriverDayOff   = "/v1/driver/day-off"
	pathDriverSubmit   = "/v1/driver/submit"
	pathDriverPlan     = "/v1/driver/plan"
	pathDriverPayments = "/v1/driver/payments"
	pathAdminPending   = "/v1/admin/pending"
)

func pathDriverDocument(kind string) string { return "/v1/driver/documents/" + kind }
func pathAdminDriver(id string) string      { return "/v1/admin/drivers/" + id }
func pathAdminReviewItem(id string) string  { return "/v1/admin/drivers/" + id + "/review-item" }
func pathAdminApprove(id string) string     { return "/v1/admin/drivers/" + id + "/approve" }
func pathAdminReject(id string) string      { return "/v1/admin/drivers/" + id + "/reject" }
func pathAdminConfirmPay(id string) string  { return "/v1/admin/payments/" + id + "/confirm" }
func pathAdminRejectPay(id string) string   { return "/v1/admin/payments/" + id + "/reject" }

// ---------------------------------------------------------------- items ----

// docKinds are the six file documents; with the UPI ID they are "the seven".
var docKinds = []string{"profile_photo", "id_proof", "licence", "rc", "insurance", "permit"}

// photoKinds are the three vehicle photos.
var photoKinds = []string{"vehicle_front", "vehicle_back", "vehicle_interior"}

// fileKinds are every driver_items kind.
var fileKinds = append(append([]string{}, docKinds...), photoKinds...)

// reviewItems is everything the admin verifies before approve.
var reviewItems = append(append([]string{}, fileKinds...), "upi", "vehicle")

// d1Now is D1-I1's clock: Sat 21 Nov 2026 10:00 IST.
var d1Now = time.Date(2026, 11, 21, 10, 0, 0, 0, ist)

// Push texts from the D1 Notifications table.
const (
	pushFixItems     = "A few things need fixing"
	pushApproved     = "You're approved"
	pushAppRejected  = "We can't approve your application"
	pushLive         = "You're live"
	pushPayRejected  = "We couldn't find your payment"
	pushAdminWaiting = "is waiting for review"
	pushAdminPaid    = "says they've paid"
)

var referenceRE = regexp.MustCompile(`^DRV-\d{5}$`)

// ---------------------------------------------------------------- client ---

// api calls ride-api with a bearer token. It never calls t.Fatal from
// inside raw(), so it is safe in goroutines.
type api struct {
	base  string
	token string
}

func (c *api) raw(method, path string, body any) (*testkit.Resp, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("authorization", "Bearer "+c.token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	return &testkit.Resp{Status: res.StatusCode, Body: b}, nil
}

func (c *api) Do(t *testing.T, method, path string, body any) *testkit.Resp {
	t.Helper()
	r, err := c.raw(method, path, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return r
}

// driver is a driver signed up through the driver app's auth flow.
type driver struct {
	*api
	Name, First, Phone, ID string
	Plate                  string
}

// d1world is a world on the "d1" seed with the drivers made by the test.
type d1world struct {
	*world
	mu      sync.Mutex
	drivers map[string]*driver // by first name
	phoneN  int
	plateN  int
}

// startD1 starts the API on the d1 seed with the clock at Sat 21 Nov 10:00 IST.
func startD1(t *testing.T) *d1world {
	t.Helper()
	w := &d1world{world: start(t, testkit.WithSeed("d1")), drivers: map[string]*driver{}}
	w.Clock.Set(d1Now)
	return w
}

func (w *d1world) setClock(at time.Time) { w.Clock.Set(at) }

// adminAPI / riderAPI wrap a testkit client's token in an api.
func (w *d1world) adminAPI(t *testing.T) *api {
	t.Helper()
	return &api{base: w.BaseURL, token: w.as(t, "Admin").Token}
}

func (w *d1world) asAPI(t *testing.T, name string) *api {
	t.Helper()
	return &api{base: w.BaseURL, token: w.as(t, name).Token}
}

// driverAuth runs the driver app's OTP flow for phone and returns the
// verify response (status 200 expected).
func (w *d1world) driverAuth(t *testing.T, phone string) (*api, map[string]any) {
	t.Helper()
	c := &api{base: w.BaseURL}
	expectStatus(t, c.Do(t, http.MethodPost, pathDriverOTP, map[string]string{"phone": phone}), http.StatusNoContent)
	r := c.Do(t, http.MethodPost, pathDriverVerify, map[string]string{"phone": phone, "code": testkit.FixedOTP})
	expectStatus(t, r, http.StatusOK)
	m := r.Map(t)
	tok, _ := m["token"].(string)
	if tok == "" {
		t.Fatalf("driver verify: no token in %s", r.Body)
	}
	c.token = tok
	return c, m
}

func userIDOf(t *testing.T, m map[string]any) string {
	t.Helper()
	if u, ok := m["user"].(map[string]any); ok {
		if id, _ := u["id"].(string); id != "" {
			return id
		}
	}
	if id, _ := m["user_id"].(string); id != "" {
		return id
	}
	t.Fatalf("no user id in %v", m)
	return ""
}

// signUpDriver signs up a new number in the driver app and registers it
// (driver.register) with the given full name. Status: signing_up.
func (w *d1world) signUpDriver(t *testing.T, fullName string) *driver {
	t.Helper()
	w.mu.Lock()
	w.phoneN++
	w.plateN++
	phone := fmt.Sprintf("98000%05d", 200+w.phoneN)
	plate := fmt.Sprintf("KA 05 AB %04d", 1233+w.plateN)
	w.mu.Unlock()
	c, m := w.driverAuth(t, phone)
	d := &driver{api: c, Name: fullName, First: strings.Fields(fullName)[0], Phone: phone, ID: userIDOf(t, m), Plate: plate}
	r := d.Do(t, http.MethodPost, pathDriverSignup, map[string]any{"name": fullName, "city": "blr", "terms_accepted": true})
	expectStatus(t, r, http.StatusOK, http.StatusCreated)
	w.mu.Lock()
	w.drivers[d.First] = d
	w.mu.Unlock()
	return d
}

// --------------------------------------------------------- driver steps ----

// jpeg is a tiny JPEG-looking payload (the fake storage doesn't decode it).
var jpeg = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0xFF, 0xD9}

// uploadURL asks for a signed upload URL; returns (upload_url, storage_path).
func (d *driver) uploadURL(t *testing.T, kind string) (string, string) {
	t.Helper()
	r := d.Do(t, http.MethodPost, pathDriverUpload, map[string]string{"kind": kind, "content_type": "image/jpeg"})
	expectStatus(t, r, http.StatusOK, http.StatusCreated)
	m := r.Map(t)
	u, _ := m["upload_url"].(string)
	p, _ := m["storage_path"].(string)
	if u == "" || p == "" {
		t.Fatalf("upload-url %s: want upload_url and storage_path, got %s", kind, r.Body)
	}
	return u, p
}

// putFile uploads the bytes to a signed URL when it is an http(s) URL.
func putFile(t *testing.T, url string) {
	t.Helper()
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return
	}
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(jpeg))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("content-type", "image/jpeg")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", url, err)
	}
	res.Body.Close()
	if res.StatusCode/100 != 2 {
		t.Fatalf("PUT %s: status %d", url, res.StatusCode)
	}
}

// upload runs upload_url -> PUT -> item_uploaded for one kind.
func (d *driver) upload(t *testing.T, kind string) {
	t.Helper()
	u, p := d.uploadURL(t, kind)
	putFile(t, u)
	r := d.Do(t, http.MethodPut, pathDriverDocument(kind), map[string]string{"storage_path": p})
	expectStatus(t, r, http.StatusOK)
}

func (d *driver) setUPI(t *testing.T) {
	t.Helper()
	r := d.Do(t, http.MethodPut, pathDriverUPI, map[string]string{"upi_id": strings.ToLower(d.First) + "@okaxis"})
	expectStatus(t, r, http.StatusOK)
}

type vehicleReq struct {
	VehicleType   string  `json:"vehicle_type"`
	Type          string  `json:"type"`
	CabClass      *string `json:"cab_class"`
	MakeModel     string  `json:"make_model"`
	Year          int     `json:"year"`
	Plate         string  `json:"plate"`
	Colour        string  `json:"colour"`
	AC            bool    `json:"ac"`
	CNGOrElectric bool    `json:"cng_or_electric"`
}

func autoVehicle(plate string) vehicleReq {
	return vehicleReq{VehicleType: "auto", Type: "auto", MakeModel: "Bajaj RE", Year: 2021,
		Plate: plate, Colour: "Green", CNGOrElectric: true}
}

func (d *driver) setVehicle(t *testing.T) {
	t.Helper()
	expectStatus(t, d.Do(t, http.MethodPut, pathDriverVehicle, autoVehicle(d.Plate)), http.StatusOK)
}

// setDayOff: weekday 1..7, or 0 for "No fixed day off" (sent as null).
func (d *driver) setDayOff(t *testing.T, weekday int) {
	t.Helper()
	expectStatus(t, d.Do(t, http.MethodPut, pathDriverDayOff, dayOffBody(weekday)), http.StatusOK)
}

func dayOffBody(weekday int) map[string]any {
	if weekday == 0 {
		return map[string]any{"day_off": nil}
	}
	return map[string]any{"day_off": weekday}
}

// fillAll adds the six documents, the UPI ID, the vehicle, its three photos
// and the day off.
func (d *driver) fillAll(t *testing.T, dayOff int) {
	t.Helper()
	for _, k := range docKinds {
		d.upload(t, k)
	}
	d.setUPI(t)
	d.setVehicle(t)
	for _, k := range photoKinds {
		d.upload(t, k)
	}
	d.setDayOff(t, dayOff)
}

func (d *driver) submit(t *testing.T) {
	t.Helper()
	expectStatus(t, d.Do(t, http.MethodPost, pathDriverSubmit, nil), http.StatusOK)
}

func (d *driver) get(t *testing.T) map[string]any {
	t.Helper()
	r := d.Do(t, http.MethodGet, pathDriver, nil)
	expectStatus(t, r, http.StatusOK)
	return r.Map(t)
}

// plan calls plans.get and returns (monthly price, amount due, period start, period end).
func (d *driver) plan(t *testing.T) (float64, float64, string, string) {
	t.Helper()
	r := d.Do(t, http.MethodGet, pathDriverPlan, nil)
	expectStatus(t, r, http.StatusOK)
	body := anyJSON(t, r)
	get := func(k string) any {
		v, ok := findKey(body, k)
		if !ok {
			t.Fatalf("plans.get: no %s in %s", k, r.Body)
		}
		return v
	}
	ps, _ := get("period_start").(string)
	pe, _ := get("period_end").(string)
	return num(t, get("monthly_price"), "monthly_price"), num(t, get("amount_due"), "amount_due"), dateOnly(ps), dateOnly(pe)
}

func dateOnly(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

// claim calls driver.claim_payment for periodStart.
func (d *driver) claim(t *testing.T, periodStart string) *testkit.Resp {
	t.Helper()
	return d.Do(t, http.MethodPost, pathDriverPayments, map[string]string{"period_start": periodStart})
}

// --------------------------------------------------------- admin steps -----

func (w *d1world) review(t *testing.T, d *driver, item, state, reason string) *testkit.Resp {
	t.Helper()
	body := map[string]string{"item": item, "state": state}
	if reason != "" {
		body["reason"] = reason
	}
	return w.adminAPI(t).Do(t, http.MethodPost, pathAdminReviewItem(d.ID), body)
}

func (w *d1world) verify(t *testing.T, d *driver, items ...string) {
	t.Helper()
	for _, it := range items {
		expectStatus(t, w.review(t, d, it, "verified", ""), http.StatusOK)
	}
}

func (w *d1world) approve(t *testing.T, d *driver) *testkit.Resp {
	t.Helper()
	return w.adminAPI(t).Do(t, http.MethodPost, pathAdminApprove(d.ID), nil)
}

func (w *d1world) confirmPayment(t *testing.T, paymentID string) *testkit.Resp {
	t.Helper()
	return w.adminAPI(t).Do(t, http.MethodPost, pathAdminConfirmPay(paymentID), nil)
}

// --------------------------------------------------------- whole flows -----

// submitted: signed up, everything added, submitted.
func (w *d1world) submittedDriver(t *testing.T, fullName string, dayOff int) *driver {
	t.Helper()
	d := w.signUpDriver(t, fullName)
	d.fillAll(t, dayOff)
	d.submit(t)
	return d
}

// approvedDriver: submitted, every item verified, approved.
func (w *d1world) approvedDriver(t *testing.T, fullName string, dayOff int) *driver {
	t.Helper()
	d := w.submittedDriver(t, fullName, dayOff)
	w.verify(t, d, reviewItems...)
	expectStatus(t, w.approve(t, d), http.StatusOK)
	return d
}

// claimed: approved and claimed the first payment; returns the payment id.
func (w *d1world) claimedDriver(t *testing.T, fullName string, dayOff int) (*driver, string) {
	t.Helper()
	d := w.approvedDriver(t, fullName, dayOff)
	_, _, ps, _ := d.plan(t)
	expectStatus(t, d.claim(t, ps), http.StatusOK, http.StatusCreated)
	return d, w.latestPayment(t, d).ID
}

// liveDriver runs the whole D1 flow to live and paid.
func (w *d1world) liveDriver(t *testing.T, fullName string, dayOff int) *driver {
	t.Helper()
	d, pay := w.claimedDriver(t, fullName, dayOff)
	expectStatus(t, w.confirmPayment(t, pay), http.StatusOK)
	if st := w.driverStatus(t, d); st != "live" {
		t.Fatalf("%s is %s after confirm, want live", d.Name, st)
	}
	return d
}

// --------------------------------------------------------------- DB --------

func (w *d1world) driverStatus(t *testing.T, d *driver) string {
	t.Helper()
	var s string
	w.one(t, `SELECT status::text FROM drivers WHERE user_id = $1`, []any{d.ID}, &s)
	return s
}

func (w *d1world) subscriptionUntil(t *testing.T, d *driver) string {
	t.Helper()
	var s *string
	w.one(t, `SELECT subscription_until::text FROM drivers WHERE user_id = $1`, []any{d.ID}, &s)
	if s == nil {
		return ""
	}
	return *s
}

// itemState is driver_items.state for a kind, "" when there is no row.
func (w *d1world) itemState(t *testing.T, d *driver, kind string) string {
	t.Helper()
	var s string
	err := w.DB.QueryRow(ctx(), `SELECT state::text FROM driver_items WHERE driver_id = $1 AND kind::text = $2`,
		d.ID, kind).Scan(&s)
	if err != nil {
		return ""
	}
	return s
}

func (w *d1world) itemPath(t *testing.T, d *driver, kind string) string {
	t.Helper()
	var s *string
	err := w.DB.QueryRow(ctx(), `SELECT storage_path FROM driver_items WHERE driver_id = $1 AND kind::text = $2`,
		d.ID, kind).Scan(&s)
	if err != nil || s == nil {
		return ""
	}
	return *s
}

type paymentRow struct {
	ID, Status, PeriodStart, PeriodEnd, RejectReason string
	Amount                                           float64
}

func (w *d1world) payments(t *testing.T, d *driver) []paymentRow {
	t.Helper()
	rows, err := w.DB.Query(ctx(), `
		SELECT id::text, status::text, period_start::text, period_end::text,
		       coalesce(reject_reason, ''), amount::float8
		  FROM subscription_payments WHERE driver_id = $1 ORDER BY claimed_at, created_at`, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []paymentRow
	for rows.Next() {
		var p paymentRow
		if err := rows.Scan(&p.ID, &p.Status, &p.PeriodStart, &p.PeriodEnd, &p.RejectReason, &p.Amount); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func (w *d1world) latestPayment(t *testing.T, d *driver) paymentRow {
	t.Helper()
	ps := w.payments(t, d)
	if len(ps) == 0 {
		t.Fatalf("%s has no subscription_payments row", d.Name)
	}
	return ps[len(ps)-1]
}

// monthlyPrice reads plans.monthly_price for a vehicle type (no cab class).
func (w *d1world) monthlyPrice(t *testing.T, vehicleType string) float64 {
	t.Helper()
	var p float64
	w.one(t, `SELECT monthly_price::float8 FROM plans WHERE vehicle_type::text = $1 AND cab_class IS NULL`,
		[]any{vehicleType}, &p)
	return p
}

// prorated: monthly price x days left (today included) / days in month, rounded up.
func prorated(price float64, daysLeft, daysInMonth int) float64 {
	return math.Ceil(price * float64(daysLeft) / float64(daysInMonth))
}

// statusEvents counts events rows for an entity whose data.new_status is status.
func (w *d1world) statusEvents(t *testing.T, entityID, status string) int {
	t.Helper()
	return w.count(t, `SELECT count(*) FROM events WHERE entity_id = $1 AND data->>'new_status' = $2`, entityID, status)
}

// --------------------------------------------------------------- pushes ----

// pushesToID counts recorded pushes to a user id containing substr.
func (w *d1world) pushesToID(id, substr string) int {
	n := 0
	for _, m := range w.Push.All() {
		if m.UserID.String() == id && strings.Contains(m.Title+" "+m.Body, substr) {
			n++
		}
	}
	return n
}

func (w *d1world) pushTexts(id string) []string {
	var out []string
	for _, m := range w.Push.All() {
		if m.UserID.String() == id {
			out = append(out, m.Title+" | "+m.Body)
		}
	}
	return out
}

func (w *d1world) expectDriverPushes(t *testing.T, d *driver, substr string, want int) {
	t.Helper()
	if got := w.pushesToID(d.ID, substr); got != want {
		t.Errorf("%s: %d pushes containing %q, want %d; all: %q", d.Name, got, substr, want, w.pushTexts(d.ID))
	}
}

// --------------------------------------------------------- offers hand-off -

// recipientIDs is the set of driver ids that received an offer.
func (w *d1world) recipientIDs(t *testing.T, offerID string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for id := range w.recipients(t, offerID) {
		out[id] = true
	}
	return out
}

// expectOfferTo checks an offer's recipients are exactly these drivers.
func (w *d1world) expectOfferTo(t *testing.T, offerID string, ds ...*driver) {
	t.Helper()
	got := w.recipientIDs(t, offerID)
	want := map[string]bool{}
	for _, d := range ds {
		want[d.ID] = true
		if !got[d.ID] {
			t.Errorf("offer %s: %s is not a recipient", offerID, d.Name)
		}
	}
	for id := range got {
		if !want[id] {
			t.Errorf("offer %s: unexpected recipient %s (%s)", offerID, id, w.driverName(t, id))
		}
	}
}

// everRecipient reports whether a driver received any offer at all.
func (w *d1world) everRecipient(t *testing.T, d *driver) bool {
	t.Helper()
	return w.count(t, `SELECT count(*) FROM offer_recipients WHERE driver_id = $1`, d.ID) > 0
}

// --------------------------------------------------------- JSON helpers ----

// statusOf reads the driver's status from a driver response: top-level
// "status" or inside "driver".
func statusOf(m map[string]any) string {
	if dm, ok := m["driver"].(map[string]any); ok {
		if s, _ := dm["status"].(string); s != "" {
			return s
		}
	}
	s, _ := m["status"].(string)
	return s
}

// strField reads a string field top-level or inside "driver".
func strField(m map[string]any, key string) string {
	if dm, ok := m["driver"].(map[string]any); ok {
		if s, _ := dm[key].(string); s != "" {
			return s
		}
	}
	s, _ := m[key].(string)
	return s
}

// findBy returns the first object anywhere in v with v[key] == val.
func findBy(v any, key, val string) map[string]any {
	switch x := v.(type) {
	case map[string]any:
		if s, _ := x[key].(string); s == val {
			return x
		}
		for _, e := range x {
			if o := findBy(e, key, val); o != nil {
				return o
			}
		}
	case []any:
		for _, e := range x {
			if o := findBy(e, key, val); o != nil {
				return o
			}
		}
	}
	return nil
}

// stringsIn collects every string anywhere in v (for "missing" lists).
func stringsIn(v any) []string {
	var out []string
	switch x := v.(type) {
	case string:
		out = append(out, x)
	case map[string]any:
		for _, e := range x {
			out = append(out, stringsIn(e)...)
		}
	case []any:
		for _, e := range x {
			out = append(out, stringsIn(e)...)
		}
	}
	return out
}

// missingList reads the "missing" list of a 409 incomplete (top level or in state).
func missingList(t *testing.T, r *testkit.Resp) []string {
	t.Helper()
	v, ok := findKey(anyJSON(t, r), "missing")
	if !ok {
		t.Fatalf("409 incomplete without a missing list: %s", r.Body)
	}
	return stringsIn(v)
}

func contains(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

// idList reads the ids of a list under key in an admin.pending response.
func idList(m map[string]any, key string) []string {
	var out []string
	list, _ := m[key].([]any)
	for _, e := range list {
		o, _ := e.(map[string]any)
		if o == nil {
			continue
		}
		for _, k := range []string{"id", "driver_id", "user_id"} {
			if s, _ := o[k].(string); s != "" {
				out = append(out, s)
				break
			}
		}
	}
	return out
}
