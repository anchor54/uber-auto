package app

import (
	"net/url"
	"regexp"
	"slices"
	"sync"
	"testing"
	"time"
)

// Flow D1: driver registration, verification and subscription.
// Scenarios D1-I1 … D1-I15 from the spec, then every error code and role.

const (
	raviNewPhone  = "9876500001"
	anilPhone     = "9876500002"
	meenaPhone    = "9876500003"
	raviPlate     = "KA 05 AB 1234"
	anilPlate     = "KA 05 AB 5678"
	meenaPlate    = "KA 05 AB 9012"
	sunday        = 7
	wednesday     = 3
	plateInUseMsg = "This number plate is registered to another driver. If it's your vehicle, contact support."
)

// D1-I1: Ravi, an auto driver, signs up on Sat 21 Nov and goes live.
func TestD1_I1_HappyPathToLive(t *testing.T) {
	ta := startD1(t)
	admin := ta.adminSignIn()

	ravi := ta.signUp("Ravi Kumar", raviNewPhone)
	me := ta.me(ravi)
	matches(t, me.json(), map[string]any{"id": ravi.id, "name": "Ravi Kumar", "phone": raviNewPhone, "city": d1City,
		"status": "signing_up", "dayOffSet": false, "subscriptionUntil": nil, "vehicle": nil, "latestPayment": nil})
	if !regexp.MustCompile(`^DRV-[0-9]{5}$`).MatchString(me.str("reference")) {
		t.Fatalf("reference %q", me.str("reference"))
	}

	// The upload link is for the driver's own path.
	u := ta.post("/v1/driver/items/licence/upload-url", map[string]string{"contentType": "image/jpeg"}, ravi.token)
	status(t, u, 200)
	matches(t, u.json(), map[string]any{"method": "PUT", "path": "driver-docs/" + ravi.id + "/licence",
		"headers": map[string]any{"content-type": "image/jpeg"}})

	ta.completeProfile(ravi, raviPlate, sunday)
	me = ta.me(ravi)
	eq(t, len(me.list("missing")), 0, "missing after completing")
	for _, kind := range d1ItemKinds {
		eq(t, d1ItemState(me.json(), kind), "uploaded", kind)
	}
	eq(t, pathIn(d1ItemOf(me.json(), "upi_id"), "value"), "driver"+raviNewPhone+"@okaxis")
	matches(t, me.json(), map[string]any{"dayOff": sunday, "dayOffSet": true,
		"vehicle": map[string]any{"type": "auto", "plate": raviPlate, "reviewState": "uploaded"}})

	ta.later(time.Minute)
	ta.submitOK(ravi)
	pending := ta.get("/v1/admin/pending", admin)
	status(t, pending, 200)
	eq(t, pluck(pending.list("drivers"), "id"), []any{ravi.id})
	matches(t, pending.at("drivers.0"), map[string]any{"name": "Ravi Kumar", "phone": raviNewPhone, "city": d1City, "status": "submitted"})

	ta.later(time.Minute)
	ta.reviewAll(admin, ravi, "verified")
	eq(t, ta.me(ravi).str("status"), "submitted", "all verified waits for approval")
	approved := ta.approve(admin, ravi)
	status(t, approved, 200)
	eq(t, approved.str("status"), "approved")
	eq(t, ta.get("/v1/admin/pending", admin).list("drivers"), []any{})

	// 21–30 Nov is 10 days of 30: ₹799 × 10 ÷ 30 = 266.33, rounded up.
	ta.later(time.Minute)
	plan := ta.plan(ravi)
	status(t, plan, 200)
	matches(t, plan.json(), map[string]any{
		"plan":         map[string]any{"rideType": "auto", "vehicleType": "auto", "cabClass": nil, "monthlyPrice": 799},
		"amountDueNow": 267, "periodStart": "2026-11-21", "periodEnd": "2026-11-30",
		"reference": me.str("reference"), "payTo": map[string]any{"upiId": "commute.plans@okaxis"}, "latestPayment": nil,
	})

	claim := ta.claim(ravi)
	status(t, claim, 201)
	matches(t, claim.at("payment"), map[string]any{"periodStart": "2026-11-21", "periodEnd": "2026-11-30", "amount": 267, "status": "claimed"})
	eq(t, claim.str("driver.status"), "approved", "claiming doesn't make him live")
	eq(t, claim.str("driver.latestPayment.status"), "claimed")
	payments := ta.get("/v1/admin/pending", admin).list("payments")
	eq(t, len(payments), 1)
	matches(t, payments[0], map[string]any{"id": claim.str("payment.id"), "driverId": ravi.id, "driverName": "Ravi Kumar",
		"reference": me.str("reference"), "periodStart": "2026-11-21", "periodEnd": "2026-11-30", "amount": 267})

	ta.later(time.Minute)
	confirmed := ta.confirmPayment(admin, claim.str("payment.id"))
	status(t, confirmed, 200)
	eq(t, confirmed.str("payment.status"), "confirmed")
	matches(t, confirmed.at("driver"), map[string]any{"status": "live", "subscriptionUntil": "2026-11-30"})
	matches(t, ta.me(ravi).json(), map[string]any{"status": "live", "subscriptionUntil": "2026-11-30"})
	eq(t, ta.strings(`select subscription_until::text from drivers where id = $1`, ravi.id), []string{"2026-11-30"})
	eq(t, ta.get("/v1/admin/pending", admin).list("payments"), []any{})

	// Each push once, even when the worker runs again.
	ta.sendPushes()
	ta.sendPushes()
	eq(t, ta.pushes.to(d1Device(raviNewPhone)), []string{
		"You're approved | You're approved. Choose your plan to go live.",
		"You're live | You're live. New requests for auto drivers will show up in Requests.",
	})
	eq(t, ta.pushes.to(d1Device(adminPhone)), []string{
		"Driver waiting | Ravi Kumar is waiting for review",
		"Payment claimed | Ravi Kumar says they've paid ₹267",
	})

	eq(t, ta.transitions(ravi), []string{"signing_up → submitted", "submitted → approved", "approved → live"})
}

// D1-I2: of three auto drivers only the live, paid-up one is offered.
func TestD1_I2_OnlyLivePaidDriversAreOffered(t *testing.T) {
	ta := startD1(t)
	ta.onlyOurAutos()
	admin := ta.adminSignIn()
	ravi := ta.live(admin, "Ravi Kumar", raviNewPhone, raviPlate, sunday)
	anil := ta.approved(admin, "Anil R", anilPhone, anilPlate, sunday) // approved, never paid
	meena := ta.live(admin, "Meena S", meenaPhone, meenaPlate, sunday)
	ta.exec(`update drivers set subscription_until = '2026-11-20' where id = $1`, meena.id) // paid up to yesterday

	ids := ta.proposedFor("", nil)
	eq(t, ids, []any{ravi.id})
	if isProposed(ids, anil) || isProposed(ids, meena) {
		t.Fatal("unpaid driver offered")
	}
}

// D1-I3: submitting with two documents missing names both and changes nothing.
func TestD1_I3_SubmitWithMissingItems(t *testing.T) {
	ta := startD1(t)
	ravi := ta.signUp("Ravi Kumar", raviNewPhone)
	for _, kind := range d1FileKinds {
		if kind != "insurance" && kind != "permit" {
			ta.uploadOK(ravi, kind)
		}
	}
	status(t, ta.setUPI(ravi, "ravi@okaxis"), 200) // 5 of the 7 documents
	status(t, ta.setVehicle(ravi, autoVehicle(raviPlate, nil)), 200)
	status(t, ta.setDayOff(ravi, sunday), 200)

	res := ta.submit(ravi)
	status(t, res, 409)
	eq(t, res.errCode(), "incomplete")
	eq(t, res.list("missing"), []any{"insurance", "permit"})
	eq(t, res.str("state.status"), "signing_up")
	eq(t, ta.me(ravi).str("status"), "signing_up")
	eq(t, ta.eventCount(ravi), 0)
}

// D1-I4: one rejected item sends Ravi back to replace just that one.
func TestD1_I4_RejectedItemAndResubmission(t *testing.T) {
	ta := startD1(t)
	admin := ta.adminSignIn()
	ravi := ta.signUp("Ravi Kumar", raviNewPhone)
	ta.completeProfile(ravi, raviPlate, sunday)
	ta.submitOK(ravi)

	rejected := ta.review(admin, ravi, "licence", "rejected", "photo blurred")
	status(t, rejected, 200)
	matches(t, rejected.at("item"), map[string]any{"kind": "licence", "state": "rejected", "rejectReason": "photo blurred"})
	eq(t, rejected.str("driver.status"), "submitted", "others still to review")
	for _, kind := range d1Reviewables {
		if kind != "licence" {
			status(t, ta.review(admin, ravi, kind, "verified", ""), 200)
		}
	}
	me := ta.me(ravi)
	eq(t, me.str("status"), "changes_needed")
	eq(t, pathIn(d1ItemOf(me.json(), "licence"), "rejectReason"), "photo blurred")

	ta.sendPushes()
	eq(t, ta.pushes.to(d1Device(raviNewPhone)), []string{
		"A few things need fixing | A few things need fixing: Driving licence. Open the app to see why."})

	// Verified items stay locked.
	again := ta.upload(ravi, "rc")
	status(t, again, 409)
	eq(t, again.errCode(), "locked")
	eq(t, again.str("state.status"), "changes_needed")
	eq(t, ta.setVehicle(ravi, autoVehicle(raviPlate, map[string]any{"colour": "Yellow"})).errCode(), "locked")
	eq(t, ta.setUPI(ravi, "ravi.new@okaxis").errCode(), "locked")
	eq(t, ta.setDayOff(ravi, 1).errCode(), "locked")

	// The rejected one can be replaced, which clears the reason.
	replaced := ta.upload(ravi, "licence")
	status(t, replaced, 200)
	matches(t, replaced.at("item"), map[string]any{"kind": "licence", "state": "uploaded", "rejectReason": nil})

	ta.submitOK(ravi)
	me = ta.me(ravi)
	for _, kind := range d1ItemKinds {
		want := "verified"
		if kind == "licence" {
			want = "uploaded"
		}
		eq(t, d1ItemState(me.json(), kind), want, kind)
	}
	eq(t, me.str("vehicle.reviewState"), "verified")
	eq(t, me.str("vehicle.colour"), "Green")
	eq(t, ta.transitions(ravi), []string{"signing_up → submitted", "submitted → changes_needed", "changes_needed → submitted"})
}

// D1-I5: after submitting, nothing can be changed.
func TestD1_I5_LockedAfterSubmit(t *testing.T) {
	ta := startD1(t)
	ravi := ta.signUp("Ravi Kumar", raviNewPhone)
	ta.completeProfile(ravi, raviPlate, sunday)
	ta.submitOK(ravi)
	before := ta.me(ravi).json()

	ta.store.Put("driver-docs/"+ravi.id+"/licence", []byte("new licence"))
	for name, res := range map[string]*resp{
		"vehicle":    ta.setVehicle(ravi, autoVehicle("KA 05 ZZ 0001", nil)),
		"day off":    ta.setDayOff(ravi, 1),
		"upload url": ta.post("/v1/driver/items/licence/upload-url", map[string]string{"contentType": "image/jpeg"}, ravi.token),
		"uploaded":   ta.post("/v1/driver/items/licence/uploaded", map[string]string{"path": "driver-docs/" + ravi.id + "/licence"}, ravi.token),
		"upi":        ta.setUPI(ravi, "ravi.new@okaxis"),
	} {
		eq(t, []any{res.status, res.errCode(), res.str("state.status")}, []any{409, "locked", "submitted"}, name)
	}
	eq(t, ta.me(ravi).json(), before, "nothing changed")
}

// D1-I6: approving before every item is verified is refused.
func TestD1_I6_ApproveTooEarly(t *testing.T) {
	ta := startD1(t)
	admin := ta.adminSignIn()
	ravi := ta.signUp("Ravi Kumar", raviNewPhone)
	ta.completeProfile(ravi, raviPlate, sunday)
	ta.submitOK(ravi)
	for _, kind := range d1ItemKinds[:6] {
		status(t, ta.review(admin, ravi, kind, "verified", ""), 200)
	}
	res := ta.approve(admin, ravi)
	status(t, res, 409)
	eq(t, res.errCode(), "items_not_verified")
	eq(t, res.list("items"), []any{"upi_id", "vehicle_front", "vehicle_back", "vehicle_interior", "vehicle"})
	eq(t, ta.me(ravi).str("status"), "submitted")
}

// D1-I7: a rejected application is final: no plan, no payment, no offers.
func TestD1_I7_RejectedApplication(t *testing.T) {
	ta := startD1(t)
	ta.onlyOurAutos()
	admin := ta.adminSignIn()
	ravi := ta.signUp("Ravi Kumar", raviNewPhone)
	ta.completeProfile(ravi, raviPlate, sunday)
	ta.submitOK(ravi)

	res := ta.post("/v1/admin/drivers/"+ravi.id+"/reject", map[string]string{"reason": "Licence not valid for commercial use"}, admin)
	status(t, res, 200)
	matches(t, res.json(), map[string]any{"status": "rejected", "rejectReason": "Licence not valid for commercial use"})
	ta.sendPushes()
	eq(t, ta.pushes.to(d1Device(raviNewPhone)), []string{
		"Application update | We can't approve your application. Open the app for details."})

	pay := ta.post("/v1/driver/payments", map[string]string{"periodStart": "2026-11-21"}, ravi.token)
	status(t, pay, 409)
	eq(t, pay.errCode(), "not_approved")
	eq(t, ta.plan(ravi).errCode(), "not_approved")
	if isProposed(ta.proposedFor("", nil), ravi) {
		t.Fatal("rejected driver offered")
	}
	eq(t, ta.transitions(ravi), []string{"signing_up → submitted", "submitted → rejected"})
}

// D1-I8: two admins confirm the same payment at once: one wins.
func TestD1_I8_ConcurrentPaymentConfirmation(t *testing.T) {
	ta := startD1(t)
	admin := ta.adminSignIn()
	ravi := ta.approved(admin, "Ravi Kumar", raviNewPhone, raviPlate, sunday)
	claim := ta.claim(ravi)
	status(t, claim, 201)
	ta.sendPushes() // "approved" out of the way
	eventsBefore := ta.eventCount(ravi)

	url := "/v1/admin/payments/" + claim.str("payment.id") + "/confirm"
	var wg sync.WaitGroup
	start := make(chan struct{})
	codes := make([]int, 2)
	errs := make([]string, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			code, body := ta.rawPost(url, admin)
			codes[i], errs[i] = code, d1ErrCode(body)
		}()
	}
	close(start)
	wg.Wait()

	slices.Sort(codes)
	eq(t, codes, []int{200, 409})
	if !slices.Contains(errs, "wrong_status") {
		t.Fatalf("loser's error: %v", errs)
	}
	eq(t, ta.eventCount(ravi), eventsBefore+1, "one events row")
	eq(t, ta.strings(`select status::text from subscription_payments where driver_id = $1`, ravi.id), []string{"confirmed"})
	matches(t, ta.me(ravi).json(), map[string]any{"status": "live", "subscriptionUntil": "2026-11-30"})
	ta.sendPushes()
	eq(t, ta.pushes.to(d1Device(raviNewPhone)), []string{
		"You're approved | You're approved. Choose your plan to go live.",
		"You're live | You're live. New requests for auto drivers will show up in Requests."})
}

// D1-I9: claiming the same period twice keeps one claim.
func TestD1_I9_DoubleClaim(t *testing.T) {
	ta := startD1(t)
	admin := ta.adminSignIn()
	ravi := ta.approved(admin, "Ravi Kumar", raviNewPhone, raviPlate, sunday)
	first := ta.claim(ravi)
	status(t, first, 201)
	second := ta.claim(ravi)
	status(t, second, 409)
	eq(t, second.errCode(), "already_claimed")
	matches(t, second.at("state"), map[string]any{"id": first.str("payment.id"), "status": "claimed"})
	eq(t, ta.count(`select count(*)::int from subscription_payments where driver_id = $1`, ravi.id), 1)
}

// D1-I10: a payment the admin can't find can be claimed again.
func TestD1_I10_PaymentRejectedThenClaimedAgain(t *testing.T) {
	ta := startD1(t)
	admin := ta.adminSignIn()
	ravi := ta.approved(admin, "Ravi Kumar", raviNewPhone, raviPlate, sunday)
	first := ta.claim(ravi)
	status(t, first, 201)

	ta.later(time.Hour)
	rej := ta.post("/v1/admin/payments/"+first.str("payment.id")+"/reject", map[string]string{"reason": "not received"}, admin)
	status(t, rej, 200)
	matches(t, rej.at("payment"), map[string]any{"status": "rejected", "rejectReason": "not received"})
	eq(t, rej.str("driver.status"), "approved")
	eq(t, ta.plan(ravi).str("latestPayment.status"), "rejected")

	ta.later(time.Hour)
	second := ta.claim(ravi)
	status(t, second, 201)
	if second.str("payment.id") == first.str("payment.id") {
		t.Fatal("same payment row reused")
	}
	status(t, ta.confirmPayment(admin, second.str("payment.id")), 200)

	payment := func(id string) []string {
		return ta.strings(`select status::text from subscription_payments where id::text = $1`, id)
	}
	eq(t, payment(first.str("payment.id")), []string{"rejected"})
	eq(t, payment(second.str("payment.id")), []string{"confirmed"})
	eq(t, ta.me(ravi).str("status"), "live")
	ta.sendPushes()
	eq(t, ta.pushes.to(d1Device(raviNewPhone)), []string{
		"You're approved | You're approved. Choose your plan to go live.",
		"Payment not found | We couldn't find your payment. Open the app to try again.",
		"You're live | You're live. New requests for auto drivers will show up in Requests."})
}

// D1-I11: approved at 23:30 on the last day of the month, Ravi pays for one
// day; from 1 Dec he is not offered until he renews (D5).
func TestD1_I11_ProrationAtMonthEnd(t *testing.T) {
	ta := startD1(t)
	ta.now = time.Date(2026, 11, 30, 18, 0, 0, 0, time.UTC) // Mon 30 Nov 23:30 IST
	ta.onlyOurAutos()
	admin := ta.adminSignIn()
	ravi := ta.approved(admin, "Ravi Kumar", raviNewPhone, raviPlate, sunday)

	plan := ta.plan(ravi)
	status(t, plan, 200)
	matches(t, plan.json(), map[string]any{"amountDueNow": 27, "periodStart": "2026-11-30", "periodEnd": "2026-11-30"})
	claim := ta.claim(ravi)
	status(t, claim, 201)
	status(t, ta.confirmPayment(admin, claim.str("payment.id")), 200)
	matches(t, ta.me(ravi).json(), map[string]any{"status": "live", "subscriptionUntil": "2026-11-30"})
	if !isProposed(ta.proposedFor("9886054321", nil), ravi) {
		t.Fatal("not offered on his last paid day")
	}

	ta.now = time.Date(2026, 11, 30, 19, 0, 0, 0, time.UTC) // Tue 1 Dec 00:30 IST
	if isProposed(ta.proposedFor("9886054322", nil), ravi) {
		t.Fatal("offered after the subscription ran out")
	}
}

// D1-I12: a commute on the driver's day off clashes.
func TestD1_I12_DayOffFeedsTheClashCheck(t *testing.T) {
	ta := startD1(t)
	ta.onlyOurAutos()
	admin := ta.adminSignIn()
	ravi := ta.live(admin, "Ravi Kumar", raviNewPhone, raviPlate, wednesday)
	anil := ta.live(admin, "Anil R", anilPhone, anilPlate, nil) // no fixed day off

	monWedFri := map[string]any{"repeat": "chosen_days", "weekdays": []int{1, 3, 5}, "time": 510}
	ids := ta.proposedFor("9886054321", monWedFri)
	if isProposed(ids, ravi) {
		t.Fatal("offered a commute on his day off")
	}
	if !isProposed(ids, anil) {
		t.Fatal("driver with no day off not offered")
	}
	monTueThu := map[string]any{"repeat": "chosen_days", "weekdays": []int{1, 2, 4}, "time": 510}
	if !isProposed(ta.proposedFor("9886054322", monTueThu), ravi) {
		t.Fatal("not offered on his working days")
	}
}

// D1-I13: a driver can't claim a file stored under another driver's path.
func TestD1_I13_StoragePathOwnership(t *testing.T) {
	ta := startD1(t)
	ravi := ta.signUp("Ravi Kumar", raviNewPhone)
	anil := ta.signUp("Anil R", anilPhone)
	u := ta.post("/v1/driver/items/licence/upload-url", map[string]string{"contentType": "image/png"}, anil.token)
	status(t, u, 200)
	ta.store.Put(u.str("path"), []byte("anil's licence"))

	res := ta.post("/v1/driver/items/licence/uploaded", map[string]string{"path": u.str("path")}, ravi.token)
	status(t, res, 403)
	eq(t, res.errCode(), "not_your_path")
	eq(t, d1ItemState(ta.me(ravi).json(), "licence"), "missing")
	eq(t, d1ItemState(ta.me(anil).json(), "licence"), "missing")
	eq(t, ta.count(`select count(*)::int from driver_items where driver_id in ($1, $2)`, ravi.id, anil.id), 0)
}

// D1-I14: a rider can't act as a driver, nor a driver as an admin.
func TestD1_I14_Roles(t *testing.T) {
	ta := startD1(t)
	ravi := ta.signUp("Ravi Kumar", raviNewPhone)
	res := ta.post("/v1/driver/submit", nil, ta.signIn(""))
	status(t, res, 403)
	eq(t, res.errCode(), "drivers_only")
	res = ta.approve(ravi.token, ravi)
	status(t, res, 403)
	eq(t, res.errCode(), "admins_only")
}

// D1-I15: a plate already on another driver is refused, however it's typed.
func TestD1_I15_PlateUniqueness(t *testing.T) {
	ta := startD1(t)
	ravi := ta.signUp("Ravi Kumar", raviNewPhone)
	anil := ta.signUp("Anil R", anilPhone)
	status(t, ta.setVehicle(ravi, autoVehicle(raviPlate, nil)), 200)

	for _, plate := range []string{"ka 05 ab 1234", "KA05AB1234", "ka-05-ab-1234", "ka 01 ab 1234" /* d_ravi's in the seed */} {
		res := ta.setVehicle(anil, autoVehicle(plate, nil))
		status(t, res, 409)
		eq(t, res.errCode(), "plate_in_use", plate)
		eq(t, res.str("message"), plateInUseMsg)
	}
	eq(t, ta.me(anil).at("vehicle"), nil)
	// Saving his own plate again is fine.
	status(t, ta.setVehicle(ravi, autoVehicle("ka 05 ab 1234", map[string]any{"colour": "Yellow"})), 200)
}

// ---- driver sign-in, sign-up and profile ----

func TestD1_SignInBeforeAndAfterRegistering(t *testing.T) {
	ta := startD1(t)
	// Any valid phone gets a code now.
	status(t, ta.post("/v1/driver/auth/otp", map[string]string{"phone": raviNewPhone}, ""), 204)
	v := ta.post("/v1/driver/auth/verify", map[string]string{"phone": raviNewPhone, "code": demoOTP}, "")
	status(t, v, 200)
	matches(t, v.json(), map[string]any{"hasProfile": false, "driver": nil})
	me := ta.get("/v1/driver/me", v.str("token"))
	status(t, me, 404)
	eq(t, me.errCode(), "not_registered")

	reg := ta.post("/v1/driver/register", map[string]any{"name": "  Ravi Kumar ", "city": d1City, "termsAccepted": true}, v.str("token"))
	status(t, reg, 201)
	matches(t, reg.json(), map[string]any{"name": "Ravi Kumar", "phone": raviNewPhone, "status": "signing_up"})
	eq(t, reg.list("missing"), []any{"profile_photo", "id_proof", "licence", "rc", "insurance", "permit", "upi_id",
		"vehicle_front", "vehicle_back", "vehicle_interior", "vehicle", "day_off"})
	eq(t, ta.count(`select count(*)::int from drivers where id = $1 and terms_accepted_at is not null`, reg.str("id")), 1)
	// Signing in again finds the profile.
	ta.post("/v1/driver/auth/otp", map[string]string{"phone": raviNewPhone}, "")
	v = ta.post("/v1/driver/auth/verify", map[string]string{"phone": raviNewPhone, "code": demoOTP}, "")
	status(t, v, 200)
	matches(t, v.json(), map[string]any{"hasProfile": true, "driver": map[string]any{"id": reg.str("id"), "status": "signing_up"}})
}

func TestD1_SignupConfigNeedsNoSession(t *testing.T) {
	ta := startD1(t)
	res := ta.get("/v1/driver/signup-config", "")
	status(t, res, 200)
	eq(t, res.list("cities"), []any{map[string]any{"id": "blr", "name": "Bengaluru"}})
	labels := map[string]string{"profile_photo": "Profile photo", "id_proof": "ID proof", "licence": "Driving licence",
		"rc": "Vehicle RC", "insurance": "Insurance", "permit": "Permit", "upi_id": "UPI ID", "vehicle_front": "Front photo",
		"vehicle_back": "Back photo", "vehicle_interior": "Interior photo"}
	items := res.list("items")
	eq(t, pluck(items, "kind")[:7], []any{"profile_photo", "id_proof", "licence", "rc", "insurance", "permit", "upi_id"})
	for _, it := range items {
		eq(t, pathIn(it, "label"), labels[pathIn(it, "kind").(string)], pathIn(it, "kind"))
	}
	eq(t, pathIn(items[0], "hint"), "A clear selfie, no sunglasses")
}

func TestD1_DriverSignInErrors(t *testing.T) {
	ta := startD1(t, func(c *Config) { c.OTPResendSeconds = 30; c.OTPMaxSendsPerHour = 2 })
	otp := func() *resp { return ta.post("/v1/driver/auth/otp", map[string]string{"phone": raviNewPhone}, "") }

	bad := ta.post("/v1/driver/auth/otp", map[string]string{"phone": "1234567890"}, "")
	eq(t, []any{bad.status, bad.errCode()}, []any{400, "invalid_phone"})
	status(t, otp(), 204)
	eq(t, []any{otp().status, otp().errCode()}, []any{429, "resend_too_soon"})
	ta.later(31 * time.Second)
	status(t, otp(), 204)
	ta.later(31 * time.Second)
	eq(t, []any{otp().status, otp().errCode()}, []any{429, "too_many_codes"})

	wrong := ta.post("/v1/driver/auth/verify", map[string]string{"phone": raviNewPhone, "code": "000000"}, "")
	eq(t, []any{wrong.status, wrong.errCode()}, []any{401, "invalid_code"})
}

func TestD1_RegisterErrors(t *testing.T) {
	ta := startD1(t)
	token := ta.driverSignIn(raviNewPhone)
	ok := map[string]any{"name": "Ravi Kumar", "city": d1City, "termsAccepted": true}
	with := func(k string, v any) map[string]any {
		b := map[string]any{}
		for key, x := range ok {
			b[key] = x
		}
		b[k] = v
		return b
	}
	for name, body := range map[string]map[string]any{
		"one-letter name":  with("name", "  R "),
		"unknown city":     with("city", "mys"),
		"terms not ticked": with("termsAccepted", false),
	} {
		res := ta.post("/v1/driver/register", body, token)
		eq(t, []any{res.status, res.errCode()}, []any{400, "invalid_request"}, name)
	}
	eq(t, ta.count(`select count(*)::int from drivers where phone = $1`, raviNewPhone), 0)

	status(t, ta.post("/v1/driver/register", ok, token), 201)
	again := ta.post("/v1/driver/register", ok, token)
	eq(t, []any{again.status, again.errCode()}, []any{409, "already_registered"})
	// A new session on the same phone, and a driver from the seed.
	again = ta.post("/v1/driver/register", ok, ta.driverSignIn(raviNewPhone))
	eq(t, []any{again.status, again.errCode()}, []any{409, "already_registered"})
	again = ta.post("/v1/driver/register", ok, ta.driverSignIn(raviPhone))
	eq(t, []any{again.status, again.errCode()}, []any{409, "already_registered"})

	// One phone can't be both a rider and a driver.
	ta.signIn("9886054321")
	rider := ta.post("/v1/driver/register", ok, ta.driverSignIn("9886054321"))
	eq(t, []any{rider.status, rider.errCode()}, []any{409, "phone_in_use_as_rider"})
}

func TestD1_UploadErrors(t *testing.T) {
	ta := startD1(t)
	ravi := ta.signUp("Ravi Kumar", raviNewPhone)
	gif := ta.post("/v1/driver/items/licence/upload-url", map[string]string{"contentType": "image/gif"}, ravi.token)
	status(t, gif, 400)
	for _, kind := range []string{"upi_id", "vehicle", "selfie"} {
		status(t, ta.post("/v1/driver/items/"+kind+"/upload-url", map[string]string{"contentType": "image/jpeg"}, ravi.token), 400)
	}

	// Reporting an upload that never reached storage.
	u := ta.post("/v1/driver/items/licence/upload-url", map[string]string{"contentType": "image/jpeg"}, ravi.token)
	res := ta.post("/v1/driver/items/licence/uploaded", map[string]string{"path": u.str("path")}, ravi.token)
	eq(t, []any{res.status, res.errCode()}, []any{422, "not_uploaded"})
	eq(t, d1ItemState(ta.me(ravi).json(), "licence"), "missing")

	for _, path := range []string{"driver-docs/someone/licence", "driver-docs/" + ravi.id + "x/licence", "other/" + ravi.id + "/licence"} {
		ta.store.Put(path, []byte("x"))
		res := ta.post("/v1/driver/items/licence/uploaded", map[string]string{"path": path}, ravi.token)
		eq(t, []any{res.status, res.errCode()}, []any{403, "not_your_path"}, path)
	}

	// Uploading again overwrites the same path.
	ta.uploadOK(ravi, "licence")
	u2 := ta.post("/v1/driver/items/licence/upload-url", map[string]string{"contentType": "image/png"}, ravi.token)
	eq(t, u2.str("path"), u.str("path"))
	eq(t, u2.str("headers.content-type"), "image/png")
}

func TestD1_UPI(t *testing.T) {
	ta := startD1(t)
	ravi := ta.signUp("Ravi Kumar", raviNewPhone)
	bad := ta.setUPI(ravi, "ravi@1bank")
	eq(t, []any{bad.status, bad.errCode()}, []any{422, "bad_upi"})
	ok := ta.setUPI(ravi, "ravi.kumar@okbank")
	status(t, ok, 200)
	matches(t, ok.at("item"), map[string]any{"kind": "upi_id", "label": "UPI ID", "state": "uploaded", "value": "ravi.kumar@okbank"})
	eq(t, d1ItemState(ok.at("driver"), "upi_id"), "uploaded")
}

func TestD1_VehicleRules(t *testing.T) {
	ta := startD1(t)
	ravi := ta.signUp("Ravi Kumar", raviNewPhone)
	for name, v := range map[string]map[string]any{
		"unknown type":       autoVehicle(raviPlate, map[string]any{"type": "truck"}),
		"cab without class":  autoVehicle(raviPlate, map[string]any{"type": "cab"}),
		"cab with odd class": autoVehicle(raviPlate, map[string]any{"type": "cab", "cabClass": "limo"}),
		"auto with a class":  autoVehicle(raviPlate, map[string]any{"cabClass": "sedan"}),
		"before 1990":        autoVehicle(raviPlate, map[string]any{"year": 1989}),
		"after next year":    autoVehicle(raviPlate, map[string]any{"year": 2028}),
		"no plate":           autoVehicle("", nil),
	} {
		res := ta.setVehicle(ravi, v)
		eq(t, res.status, 400, name)
	}
	eq(t, ta.me(ravi).at("vehicle"), nil)

	// AC only counts for cabs.
	auto := ta.setVehicle(ravi, autoVehicle("ka 05  ab 1234", map[string]any{"ac": true, "year": 2027}))
	status(t, auto, 200)
	matches(t, auto.at("vehicle"), map[string]any{"type": "auto", "cabClass": nil, "plate": "KA 05 AB 1234", "ac": false,
		"year": 2027, "reviewState": "uploaded"})
	eq(t, auto.str("driver.plan.rideType"), "auto")

	cab := ta.setVehicle(ravi, map[string]any{"type": "cab", "cabClass": "sedan", "makeModel": "Dzire", "year": 2023,
		"plate": "KA 05 AB 1234", "colour": "White", "ac": true, "cngOrElectric": false})
	status(t, cab, 200)
	matches(t, cab.at("vehicle"), map[string]any{"type": "cab", "cabClass": "sedan", "ac": true})
	matches(t, cab.at("driver.plan"), map[string]any{"rideType": "cab_sedan", "vehicleType": "cab", "cabClass": "sedan", "monthlyPrice": 1499})
	eq(t, ta.strings(`select ride_type || '|' || vehicle || '|' || plate || '|' || has_ac::text from drivers where id = $1`, ravi.id),
		[]string{"cab_sedan|White Dzire · AC|KA 05 AB 1234|true"})
	eq(t, ta.count(`select count(*)::int from vehicles where plate_key = 'KA05AB1234'`), 1)
}

func TestD1_DayOff(t *testing.T) {
	ta := startD1(t)
	ravi := ta.signUp("Ravi Kumar", raviNewPhone)
	for _, day := range []any{0, 8, "sun"} {
		status(t, ta.setDayOff(ravi, day), 400)
	}
	eq(t, ta.me(ravi).at("dayOffSet"), false)
	res := ta.setDayOff(ravi, sunday)
	status(t, res, 200)
	matches(t, res.json(), map[string]any{"dayOff": sunday, "dayOffSet": true})
	// "No fixed day off" is a choice too.
	res = ta.setDayOff(ravi, nil)
	status(t, res, 200)
	matches(t, res.json(), map[string]any{"dayOff": nil, "dayOffSet": true})
	if slices.Contains(res.list("missing"), any("day_off")) {
		t.Fatal("no fixed day off counted as missing")
	}
}

func TestD1_SubmitErrors(t *testing.T) {
	ta := startD1(t)
	ravi := ta.signUp("Ravi Kumar", raviNewPhone)
	res := ta.submit(ravi)
	eq(t, []any{res.status, res.errCode()}, []any{409, "incomplete"})
	eq(t, res.list("missing"), []any{"profile_photo", "id_proof", "licence", "rc", "insurance", "permit", "upi_id",
		"vehicle_front", "vehicle_back", "vehicle_interior", "vehicle", "day_off"})

	ta.completeProfile(ravi, raviPlate, nil)
	ta.submitOK(ravi)
	again := ta.submit(ravi)
	eq(t, []any{again.status, again.errCode(), again.str("state.status")}, []any{409, "wrong_status", "submitted"})
	eq(t, ta.transitions(ravi), []string{"signing_up → submitted"})
}

func TestD1_PlanAndPaymentErrors(t *testing.T) {
	ta := startD1(t)
	admin := ta.adminSignIn()
	ravi := ta.signUp("Ravi Kumar", raviNewPhone)
	plan := ta.plan(ravi)
	eq(t, []any{plan.status, plan.errCode()}, []any{409, "not_approved"})
	pay := ta.post("/v1/driver/payments", map[string]string{"periodStart": "2026-11-21"}, ravi.token)
	eq(t, []any{pay.status, pay.errCode()}, []any{409, "not_approved"})

	ta.completeProfile(ravi, raviPlate, sunday)
	ta.submitOK(ravi)
	ta.reviewAll(admin, ravi, "verified")
	status(t, ta.approve(admin, ravi), 200)
	for _, start := range []string{"2026-11-01", "2026-11-22", "2026-12-01", "21-11-2026"} {
		res := ta.post("/v1/driver/payments", map[string]string{"periodStart": start}, ravi.token)
		eq(t, []any{res.status, res.errCode()}, []any{422, "wrong_period"}, start)
	}
	eq(t, ta.count(`select count(*)::int from subscription_payments where driver_id = $1`, ravi.id), 0)
}

func TestD1_AdminSignIn(t *testing.T) {
	ta := startD1(t)
	res := ta.post("/v1/admin/auth/otp", map[string]string{"phone": raviNewPhone}, "")
	eq(t, []any{res.status, res.errCode()}, []any{403, "not_an_admin"})
	eq(t, len(ta.sentCodes), 0)

	status(t, ta.post("/v1/admin/auth/otp", map[string]string{"phone": adminPhone}, ""), 204)
	wrong := ta.post("/v1/admin/auth/verify", map[string]string{"phone": adminPhone, "code": "000000"}, "")
	eq(t, []any{wrong.status, wrong.errCode()}, []any{401, "invalid_code"})
	status(t, ta.get("/v1/admin/pending", ta.adminSignIn()), 200)
}

// The admin opens each file through a short-lived signed link; the driver's
// own view has none.
func TestD1_AdminSeesSignedLinks(t *testing.T) {
	ta := startD1(t)
	admin := ta.adminSignIn()
	ravi := ta.signUp("Ravi Kumar", raviNewPhone)
	ta.completeProfile(ravi, raviPlate, sunday)

	nf := ta.get("/v1/admin/drivers/drv_nope", admin)
	eq(t, []any{nf.status, nf.errCode()}, []any{404, "not_found"})

	view := ta.get("/v1/admin/drivers/"+ravi.id, admin)
	status(t, view, 200)
	for _, kind := range d1FileKinds {
		link, _ := pathIn(d1ItemOf(view.json(), kind), "url").(string)
		u, err := url.Parse(link)
		if link == "" || err != nil {
			t.Fatalf("%s: url %q", kind, link)
		}
		// The in-memory store is served by the API.
		file := ta.get(u.RequestURI(), "")
		status(t, file, 200)
		eq(t, string(file.body), "jpeg bytes of "+kind, kind)
	}
	eq(t, pathIn(d1ItemOf(view.json(), "upi_id"), "url"), nil)
	eq(t, view.at("vehicle.url"), nil)
	for _, it := range ta.me(ravi).list("items") {
		if _, has := it.(map[string]any)["url"]; has {
			t.Fatalf("driver sees a link: %v", it)
		}
	}
}

func TestD1_ReviewErrors(t *testing.T) {
	ta := startD1(t)
	admin := ta.adminSignIn()
	ravi := ta.signUp("Ravi Kumar", raviNewPhone)
	ta.completeProfile(ravi, raviPlate, sunday)

	early := ta.review(admin, ravi, "licence", "verified", "")
	eq(t, []any{early.status, early.errCode(), early.str("state.status")}, []any{409, "wrong_status", "signing_up"})

	ta.submitOK(ravi)
	status(t, ta.review(admin, ravi, "licence", "rejected", ""), 400)
	status(t, ta.review(admin, ravi, "licence", "maybe", ""), 400)
	status(t, ta.review(admin, &d1Driver{id: "drv_nope"}, "licence", "verified", ""), 404)
	eq(t, d1ItemState(ta.me(ravi).json(), "licence"), "uploaded")
}

func TestD1_ApproveAndRejectErrors(t *testing.T) {
	ta := startD1(t)
	admin := ta.adminSignIn()
	ravi := ta.approved(admin, "Ravi Kumar", raviNewPhone, raviPlate, sunday)
	again := ta.approve(admin, ravi)
	eq(t, []any{again.status, again.errCode(), again.str("state.status")}, []any{409, "wrong_status", "approved"})
	status(t, ta.approve(admin, &d1Driver{id: "drv_nope"}), 404)

	anil := ta.signUp("Anil R", anilPhone)
	ta.completeProfile(anil, anilPlate, sunday)
	ta.submitOK(anil)
	reject := func(reason string) *resp {
		return ta.post("/v1/admin/drivers/"+anil.id+"/reject", map[string]string{"reason": reason}, admin)
	}
	status(t, reject(""), 400)
	status(t, reject("Not eligible"), 200)
	twice := reject("Not eligible")
	eq(t, []any{twice.status, twice.errCode(), twice.str("state.status")}, []any{409, "wrong_status", "rejected"})
}

func TestD1_AdminPaymentErrors(t *testing.T) {
	ta := startD1(t)
	admin := ta.adminSignIn()
	nobody := "00000000-0000-0000-0000-000000000000"
	eq(t, ta.confirmPayment(admin, nobody).status, 404)
	eq(t, ta.post("/v1/admin/payments/"+nobody+"/reject", map[string]string{"reason": "x"}, admin).status, 404)

	ravi := ta.approved(admin, "Ravi Kumar", raviNewPhone, raviPlate, sunday)
	id := ta.claim(ravi).str("payment.id")
	status(t, ta.post("/v1/admin/payments/"+id+"/reject", map[string]string{"reason": ""}, admin), 400)
	status(t, ta.confirmPayment(admin, id), 200)
	again := ta.confirmPayment(admin, id)
	eq(t, []any{again.status, again.errCode(), again.str("state.id"), again.str("state.status")}, []any{409, "wrong_status", id, "confirmed"})
	rej := ta.post("/v1/admin/payments/"+id+"/reject", map[string]string{"reason": "not received"}, admin)
	eq(t, []any{rej.status, rej.errCode(), rej.str("state.status")}, []any{409, "wrong_status", "confirmed"})
}

func TestD1_PendingIsOldestFirst(t *testing.T) {
	ta := startD1(t)
	admin := ta.adminSignIn()
	ravi := ta.signUp("Ravi Kumar", raviNewPhone)
	anil := ta.signUp("Anil R", anilPhone)
	ta.completeProfile(ravi, raviPlate, sunday)
	ta.completeProfile(anil, anilPlate, sunday)
	ta.submitOK(anil)
	ta.later(time.Minute)
	ta.submitOK(ravi)
	eq(t, pluck(ta.get("/v1/admin/pending", admin).list("drivers"), "id"), []any{anil.id, ravi.id})

	for _, d := range []*d1Driver{ravi, anil} {
		ta.reviewAll(admin, d, "verified")
		status(t, ta.approve(admin, d), 200)
	}
	ravisClaim := ta.claim(ravi)
	ta.later(time.Minute)
	anilsClaim := ta.claim(anil)
	eq(t, pluck(ta.get("/v1/admin/pending", admin).list("payments"), "id"), []any{ravisClaim.str("payment.id"), anilsClaim.str("payment.id")})
}

// Every D1 route takes only its own role.
func TestD1_EveryRouteChecksTheRole(t *testing.T) {
	ta := startD1(t)
	rider, admin := ta.signIn(""), ta.adminSignIn()
	driver := ta.signUp("Ravi Kumar", raviNewPhone).token

	driverRoutes := []string{
		"GET /v1/driver/me", "POST /v1/driver/register", "POST /v1/driver/items/licence/upload-url",
		"POST /v1/driver/items/licence/uploaded", "PUT /v1/driver/upi", "PUT /v1/driver/vehicle", "PUT /v1/driver/day-off",
		"POST /v1/driver/submit", "GET /v1/driver/plan", "POST /v1/driver/payments",
	}
	adminRoutes := []string{
		"GET /v1/admin/pending", "GET /v1/admin/drivers/drv_x", "POST /v1/admin/drivers/drv_x/items/licence/review",
		"POST /v1/admin/drivers/drv_x/approve", "POST /v1/admin/drivers/drv_x/reject",
		"POST /v1/admin/payments/00000000-0000-0000-0000-000000000000/confirm",
		"POST /v1/admin/payments/00000000-0000-0000-0000-000000000000/reject",
	}
	call := func(route, token string) *resp {
		var method, path string
		for i := range route {
			if route[i] == ' ' {
				method, path = route[:i], route[i+1:]
				break
			}
		}
		var body any
		if method != "GET" {
			body = map[string]any{}
		}
		return ta.do(method, path, body, bearer(token))
	}
	check := func(route, who, token string, wantStatus int, wantCode string) {
		t.Helper()
		res := call(route, token)
		eq(t, []any{res.status, res.errCode()}, []any{wantStatus, wantCode}, route, " as ", who)
	}
	for _, route := range driverRoutes {
		check(route, "rider", rider, 403, "drivers_only")
		check(route, "admin", admin, 403, "drivers_only")
		check(route, "nobody", "", 401, "unauthorized")
	}
	for _, route := range adminRoutes {
		check(route, "rider", rider, 403, "admins_only")
		check(route, "driver", driver, 403, "admins_only")
		check(route, "nobody", "", 401, "unauthorized")
	}
}
