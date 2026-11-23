package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"
)

// Helpers for flow D1: driver registration, verification and subscription.

const (
	adminPhone = "9999900000"
	d1City     = "blr"
)

// Sat 21 Nov 2026, 10:00 AM IST: the day Ravi signs up in the spec.
var d1Now = time.Date(2026, 11, 21, 4, 30, 0, 0, time.UTC)

// Every reviewable item, in order: the seven documents, the three vehicle
// photos and the vehicle details.
var (
	d1FileKinds = []string{"profile_photo", "id_proof", "licence", "rc", "insurance", "permit",
		"vehicle_front", "vehicle_back", "vehicle_interior"}
	d1ItemKinds = []string{"profile_photo", "id_proof", "licence", "rc", "insurance", "permit", "upi_id",
		"vehicle_front", "vehicle_back", "vehicle_interior"}
	d1Reviewables = append(append([]string{}, d1ItemKinds...), "vehicle")
)

// pushLog is a push sender that records every message by device token.
type pushLog struct {
	mu   sync.Mutex
	sent map[string][]PushMessage
}

func (p *pushLog) Send(_ context.Context, token string, m PushMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent[token] = append(p.sent[token], m)
	return nil
}

// to is what was sent to one device, as "title | body".
func (p *pushLog) to(token string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := []string{}
	for _, m := range p.sent[token] {
		out = append(out, m.Title+" | "+m.Body)
	}
	return out
}

type d1App struct {
	*testApp
	store  *MemStorage
	pushes *pushLog
}

// startD1 is the test app with one admin phone, the in-memory document
// store and a recording push sender, at Sat 21 Nov 10:00 IST.
func startD1(t *testing.T, configure ...func(*Config)) *d1App {
	t.Helper()
	configure = append([]func(*Config){func(c *Config) { c.AdminPhones = []string{adminPhone} }}, configure...)
	ta := startTestApp(t, configure...)
	ta.now = d1Now
	store := NewMemStorage("http://test", "secret")
	ta.app.Storage = store
	ta.h = ta.app.Handler()
	return &d1App{testApp: ta, store: store, pushes: &pushLog{sent: map[string][]PushMessage{}}}
}

// sendPushes runs the worker's send job once.
func (ta *d1App) sendPushes() {
	ta.t.Helper()
	if _, err := ta.app.SendDueNotifications(context.Background(), ta.pushes); err != nil {
		ta.t.Fatal(err)
	}
}

func d1Device(phone string) string { return "device-" + phone }

func (ta *d1App) registerDevice(phone, token string) {
	ta.t.Helper()
	status(ta.t, ta.put("/v1/devices", map[string]string{"pushToken": d1Device(phone), "platform": "android"}, token), 204)
}

// ---- people ----

type d1Driver struct {
	name, phone, token, id string
}

// signUp signs a new phone in on the driver app and registers it in
// Bengaluru, with a device for pushes.
func (ta *d1App) signUp(name, phone string) *d1Driver {
	ta.t.Helper()
	token := ta.driverSignIn(phone)
	res := ta.post("/v1/driver/register", map[string]any{"name": name, "city": d1City, "termsAccepted": true}, token)
	status(ta.t, res, 201)
	ta.registerDevice(phone, token)
	return &d1Driver{name: name, phone: phone, token: token, id: res.str("id")}
}

func (ta *d1App) adminSignIn() string {
	ta.t.Helper()
	status(ta.t, ta.post("/v1/admin/auth/otp", map[string]string{"phone": adminPhone}, ""), 204)
	res := ta.post("/v1/admin/auth/verify", map[string]string{"phone": adminPhone, "code": demoOTP}, "")
	status(ta.t, res, 200)
	token := res.str("token")
	ta.registerDevice(adminPhone, token)
	return token
}

func (ta *d1App) me(d *d1Driver) *resp {
	ta.t.Helper()
	res := ta.get("/v1/driver/me", d.token)
	status(ta.t, res, 200)
	return res
}

// itemState is the state of one item in a driverState ("missing" if not listed).
func d1ItemState(driver any, kind string) string {
	for _, item := range d1List(pathIn(driver, "items")) {
		if pathIn(item, "kind") == kind {
			s, _ := pathIn(item, "state").(string)
			return s
		}
	}
	return "missing"
}

func d1ItemOf(driver any, kind string) any {
	for _, it := range d1List(pathIn(driver, "items")) {
		if pathIn(it, "kind") == kind {
			return it
		}
	}
	return nil
}

func d1List(v any) []any {
	l, _ := v.([]any)
	return l
}

// ---- the driver's steps ----

// upload asks for an upload link, stores a file at its path as the phone
// would with the PUT, and reports the upload.
func (ta *d1App) upload(d *d1Driver, kind string) *resp {
	ta.t.Helper()
	u := ta.post("/v1/driver/items/"+kind+"/upload-url", map[string]string{"contentType": "image/jpeg"}, d.token)
	if u.status != 200 {
		return u
	}
	ta.store.Put(u.str("path"), []byte("jpeg bytes of "+kind))
	return ta.post("/v1/driver/items/"+kind+"/uploaded", map[string]string{"path": u.str("path")}, d.token)
}

func (ta *d1App) uploadOK(d *d1Driver, kind string) {
	ta.t.Helper()
	status(ta.t, ta.upload(d, kind), 200)
}

func (ta *d1App) setUPI(d *d1Driver, upi string) *resp {
	ta.t.Helper()
	return ta.put("/v1/driver/upi", map[string]string{"upiId": upi}, d.token)
}

// autoVehicle is Ravi's auto from the spec with [plate].
func autoVehicle(plate string, overrides map[string]any) map[string]any {
	v := map[string]any{"type": "auto", "cabClass": nil, "makeModel": "Bajaj RE", "year": 2022, "plate": plate,
		"colour": "Green", "ac": false, "cngOrElectric": true}
	for k, x := range overrides {
		v[k] = x
	}
	return v
}

func (ta *d1App) setVehicle(d *d1Driver, v map[string]any) *resp {
	ta.t.Helper()
	return ta.put("/v1/driver/vehicle", v, d.token)
}

func (ta *d1App) setDayOff(d *d1Driver, day any) *resp {
	ta.t.Helper()
	return ta.put("/v1/driver/day-off", map[string]any{"dayOff": day}, d.token)
}

// completeProfile adds every document and photo, the UPI ID, an auto with
// [plate] and [dayOff] (1 Mon … 7 Sun, or nil for no fixed day).
func (ta *d1App) completeProfile(d *d1Driver, plate string, dayOff any) {
	ta.t.Helper()
	for _, kind := range d1FileKinds {
		ta.uploadOK(d, kind)
	}
	status(ta.t, ta.setUPI(d, "driver"+d.phone+"@okaxis"), 200)
	status(ta.t, ta.setVehicle(d, autoVehicle(plate, nil)), 200)
	status(ta.t, ta.setDayOff(d, dayOff), 200)
}

func (ta *d1App) submit(d *d1Driver) *resp {
	ta.t.Helper()
	return ta.post("/v1/driver/submit", nil, d.token)
}

func (ta *d1App) submitOK(d *d1Driver) {
	ta.t.Helper()
	res := ta.submit(d)
	status(ta.t, res, 200)
	eq(ta.t, res.str("status"), "submitted")
}

// plan is GET /v1/driver/plan.
func (ta *d1App) plan(d *d1Driver) *resp {
	ta.t.Helper()
	return ta.get("/v1/driver/plan", d.token)
}

// claim says the driver has paid for the period the plan shows now.
func (ta *d1App) claim(d *d1Driver) *resp {
	ta.t.Helper()
	p := ta.plan(d)
	status(ta.t, p, 200)
	return ta.post("/v1/driver/payments", map[string]string{"periodStart": p.str("periodStart")}, d.token)
}

// ---- the admin's steps ----

func (ta *d1App) review(admin string, d *d1Driver, kind, decision, reason string) *resp {
	ta.t.Helper()
	body := map[string]any{"decision": decision}
	if reason != "" {
		body["reason"] = reason
	}
	return ta.post("/v1/admin/drivers/"+d.id+"/items/"+kind+"/review", body, admin)
}

// reviewAll gives every reviewable item the same decision.
func (ta *d1App) reviewAll(admin string, d *d1Driver, decision string) {
	ta.t.Helper()
	reason := ""
	if decision == "rejected" {
		reason = "Not readable"
	}
	for _, kind := range d1Reviewables {
		status(ta.t, ta.review(admin, d, kind, decision, reason), 200)
	}
}

func (ta *d1App) approve(admin string, d *d1Driver) *resp {
	ta.t.Helper()
	return ta.post("/v1/admin/drivers/"+d.id+"/approve", nil, admin)
}

func (ta *d1App) confirmPayment(admin, paymentID string) *resp {
	ta.t.Helper()
	return ta.post("/v1/admin/payments/"+paymentID+"/confirm", nil, admin)
}

// approved takes a new auto driver from sign-up to approved.
func (ta *d1App) approved(admin, name, phone, plate string, dayOff any) *d1Driver {
	ta.t.Helper()
	d := ta.signUp(name, phone)
	ta.completeProfile(d, plate, dayOff)
	ta.submitOK(d)
	ta.reviewAll(admin, d, "verified")
	status(ta.t, ta.approve(admin, d), 200)
	return d
}

// live takes a new auto driver all the way to live: approved, claimed and
// confirmed.
func (ta *d1App) live(admin, name, phone, plate string, dayOff any) *d1Driver {
	ta.t.Helper()
	d := ta.approved(admin, name, phone, plate, dayOff)
	c := ta.claim(d)
	status(ta.t, c, 201)
	status(ta.t, ta.confirmPayment(admin, c.str("payment.id")), 200)
	return d
}

// ---- matching ----

// onlyOurAutos takes the demo seed's autos out of matching, so only the
// drivers a test registers can be proposed.
func (ta *d1App) onlyOurAutos() {
	ta.t.Helper()
	ta.exec(`update drivers set status = 'paused' where ride_type = 'auto'`)
}

// proposedFor is the driver ids a new rider is offered for an auto commute.
func (ta *d1App) proposedFor(riderPhone string, overrides map[string]any) []any {
	ta.t.Helper()
	o := map[string]any{"rideType": "auto", "preferences": []string{}}
	for k, v := range overrides {
		o[k] = v
	}
	res := ta.propose(ta.signIn(riderPhone), o)
	status(ta.t, res, 200)
	return pluck(res.list("proposals"), "driver.id")
}

func isProposed(ids []any, d *d1Driver) bool { return slices.Contains(ids, any(d.id)) }

// ---- events ----

// transitions is the driver's status changes from driver_events, oldest first.
func (ta *d1App) transitions(d *d1Driver) []string {
	ta.t.Helper()
	return ta.strings(`select from_status::text || ' → ' || to_status::text from driver_events
		where driver_id = $1 and from_status is not null and to_status is not null and from_status <> to_status
		order by at`, d.id)
}

func (ta *d1App) eventCount(d *d1Driver) int {
	ta.t.Helper()
	return ta.count(`select count(*)::int from driver_events where driver_id = $1`, d.id)
}

// rawPost is a POST that is safe to call from another goroutine: it never
// touches testing.T.
func (ta *d1App) rawPost(url, token string) (int, []byte) {
	req := httptest.NewRequest("POST", url, bytes.NewReader([]byte("{}")))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	ta.h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func d1ErrCode(body []byte) string {
	var v struct{ Error string }
	json.Unmarshal(body, &v)
	return v.Error
}
