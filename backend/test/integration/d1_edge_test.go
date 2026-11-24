package integration

// D1 edge cases and API errors (spec Edge cases and Endpoints tables), plus
// the hand-off to R1+D2 for drivers at every D1 status.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// auth.verify says whether a driver profile exists.
func TestD1_Edge_VerifyTellsWhetherProfileExists(t *testing.T) {
	w := startD1(t)
	_, m := w.driverAuth(t, "9811111111")
	if v, ok := m["driver_exists"].(bool); !ok || v {
		t.Errorf("new number: driver_exists = %v, want false; %v", m["driver_exists"], m)
	}
	ravi := w.signUpDriver(t, "Ravi Kumar")
	_, m = w.driverAuth(t, ravi.Phone)
	if v, ok := m["driver_exists"].(bool); !ok || !v {
		t.Errorf("registered: driver_exists = %v, want true; %v", m["driver_exists"], m)
	}
	if id := userIDOf(t, m); id != ravi.ID {
		t.Errorf("signed in as %s, want %s", id, ravi.ID)
	}
}

// auth.verify with a wrong code: 401 wrong_code.
func TestD1_Edge_WrongCode(t *testing.T) {
	w := startD1(t)
	c := &api{base: w.BaseURL}
	expectStatus(t, c.Do(t, http.MethodPost, pathDriverOTP, map[string]string{"phone": "9811111112"}), http.StatusNoContent)
	r := c.Do(t, http.MethodPost, pathDriverVerify, map[string]string{"phone": "9811111112", "code": "000000"})
	expectStatus(t, r, http.StatusUnauthorized)
	// SPEC-AMBIGUITY: D1 names the code wrong_code; R1+D2's API.md uses
	// invalid_code for the same failure. Either is accepted.
	if code, _ := r.Map(t)["code"].(string); code != "wrong_code" && code != "invalid_code" {
		t.Errorf("code %q, want wrong_code", code)
	}
}

// A rider's phone tries to register as a driver: 409 phone_in_use_as_rider.
func TestD1_Edge_RiderPhoneCannotRegister(t *testing.T) {
	w := startD1(t)
	var phone string
	w.one(t, `SELECT phone FROM users WHERE id = $1`, []any{w.uid("Priya")}, &phone)
	c := &api{base: w.BaseURL}
	expectStatus(t, c.Do(t, http.MethodPost, pathDriverOTP, map[string]string{"phone": phone}), http.StatusNoContent)
	r := c.Do(t, http.MethodPost, pathDriverVerify, map[string]string{"phone": phone, "code": "123456"})
	// SPEC-AMBIGUITY: the spec puts phone_in_use_as_rider on driver.register,
	// so driver auth.verify must give a rider's number a session first (the
	// literal reading). Refusing at verify with the same code is accepted too.
	if r.Status == http.StatusConflict {
		expectError(t, r, http.StatusConflict, "phone_in_use_as_rider")
	} else {
		expectStatus(t, r, http.StatusOK)
		c.token, _ = r.Map(t)["token"].(string)
		r = c.Do(t, http.MethodPost, pathDriverSignup, map[string]any{"name": "Priya Sharma", "city": "blr", "terms_accepted": true})
		expectError(t, r, http.StatusConflict, "phone_in_use_as_rider")
	}
	if n := w.count(t, `SELECT count(*) FROM drivers WHERE user_id = $1`, w.uid("Priya")); n != 0 {
		t.Errorf("a drivers row was created for Priya")
	}
	var role string
	w.one(t, `SELECT role FROM users WHERE id = $1`, []any{w.uid("Priya")}, &role)
	if role != "rider" {
		t.Errorf("Priya's role %s, want rider", role)
	}
	// Priya can still use the rider app.
	expectStatus(t, w.as(t, "Priya").Do(t, http.MethodGet, pathSeries, nil), http.StatusOK)
}

// Registering twice: 409 already_registered; one drivers row, same reference.
func TestD1_Edge_RegisterTwice(t *testing.T) {
	w := startD1(t)
	ravi := w.signUpDriver(t, "Ravi Kumar")
	r := ravi.Do(t, http.MethodPost, pathDriverSignup, map[string]any{"name": "Ravi K", "city": "blr", "terms_accepted": true})
	expectError(t, r, http.StatusConflict, "already_registered")
	if n := w.count(t, `SELECT count(*) FROM drivers WHERE user_id = $1`, ravi.ID); n != 1 {
		t.Errorf("%d drivers rows", n)
	}
	var name string
	w.one(t, `SELECT name FROM drivers WHERE user_id = $1`, []any{ravi.ID}, &name)
	if name != "Ravi Kumar" {
		t.Errorf("name changed to %q", name)
	}
}

// References are DRV + 5 digits and unique.
func TestD1_Edge_ReferencesUnique(t *testing.T) {
	w := startD1(t)
	seen := map[string]bool{}
	for i, n := range []string{"Ravi Kumar", "Anil Gowda", "Meena Rao", "Suresh M"} {
		d := w.signUpDriver(t, n)
		var ref string
		w.one(t, `SELECT reference FROM drivers WHERE user_id = $1`, []any{d.ID}, &ref)
		if !referenceRE.MatchString(ref) {
			t.Errorf("driver %d reference %q, want DRV-nnnnn", i, ref)
		}
		if seen[ref] {
			t.Errorf("reference %s repeated", ref)
		}
		seen[ref] = true
	}
}

// The upload finishes but the app dies before item_uploaded: the item still
// shows "Upload"; uploading again uses the same path driver-docs/{driver}/{kind}.
func TestD1_Edge_UploadNotConfirmed(t *testing.T) {
	w := startD1(t)
	ravi := w.signUpDriver(t, "Ravi Kumar")
	u, p1 := ravi.uploadURL(t, "licence")
	putFile(t, u)
	if want := "driver-docs/" + ravi.ID + "/licence"; p1 != want {
		t.Errorf("storage_path %q, want %q", p1, want)
	}
	if st := w.itemState(t, ravi, "licence"); st != "" {
		t.Errorf("licence item %q before item_uploaded, want none", st)
	}
	got := ravi.get(t)
	if it := findBy(got, "kind", "licence"); it != nil {
		if s, _ := it["state"].(string); s == "uploaded" || s == "verified" || s == "rejected" {
			t.Errorf("driver.get licence state %q before item_uploaded, want not added", s)
		}
	}
	_, p2 := ravi.uploadURL(t, "licence")
	if p2 != p1 {
		t.Errorf("second upload path %q, want the same %q", p2, p1)
	}
	expectStatus(t, ravi.Do(t, http.MethodPut, pathDriverDocument("licence"), map[string]string{"storage_path": p2}), http.StatusOK)
	if st := w.itemState(t, ravi, "licence"); st != "uploaded" {
		t.Errorf("licence %q, want uploaded", st)
	}
	// Each item can be replaced until submit.
	ravi.upload(t, "licence")
	if n := w.count(t, `SELECT count(*) FROM driver_items WHERE driver_id = $1 AND kind::text = 'licence'`, ravi.ID); n != 1 {
		t.Errorf("%d licence rows after replacing, want 1", n)
	}
}

// UPI ID must look like name@handle: 422 bad_upi, nothing saved.
func TestD1_Edge_BadUPI(t *testing.T) {
	w := startD1(t)
	ravi := w.signUpDriver(t, "Ravi Kumar")
	for _, bad := range []string{"ravi", "ravi@", "@okaxis", "ravi okaxis"} {
		expectError(t, ravi.Do(t, http.MethodPut, pathDriverUPI, map[string]string{"upi_id": bad}), http.StatusUnprocessableEntity, "bad_upi")
	}
	var upi *string
	w.one(t, `SELECT upi_id FROM drivers WHERE user_id = $1`, []any{ravi.ID}, &upi)
	if upi != nil && *upi != "" {
		t.Errorf("upi_id saved as %q", *upi)
	}
	ravi.setUPI(t)
	w.one(t, `SELECT upi_id FROM drivers WHERE user_id = $1`, []any{ravi.ID}, &upi)
	if upi == nil || *upi != "ravi@okaxis" {
		t.Errorf("upi_id %v, want ravi@okaxis", upi)
	}
}

// plans.get and claim_payment before approval: 409 not_approved.
func TestD1_Edge_PlanAndClaimNeedApproval(t *testing.T) {
	w := startD1(t)
	ravi := w.signUpDriver(t, "Ravi Kumar")
	expectError(t, ravi.Do(t, http.MethodGet, pathDriverPlan, nil), http.StatusConflict, "not_approved")
	expectError(t, ravi.claim(t, "2026-11-21"), http.StatusConflict, "not_approved")
	ravi.fillAll(t, 7)
	ravi.submit(t)
	expectError(t, ravi.Do(t, http.MethodGet, pathDriverPlan, nil), http.StatusConflict, "not_approved")
	expectError(t, ravi.claim(t, "2026-11-21"), http.StatusConflict, "not_approved")
	if n := len(w.payments(t, ravi)); n != 0 {
		t.Errorf("%d payment rows", n)
	}
}

// Submit when already submitted: 409 wrong_status.
func TestD1_Edge_SubmitTwice(t *testing.T) {
	w := startD1(t)
	ravi := w.submittedDriver(t, "Ravi Kumar", 7)
	expectError(t, ravi.Do(t, http.MethodPost, pathDriverSubmit, nil), http.StatusConflict, "wrong_status")
	if n := w.statusEvents(t, ravi.ID, "submitted"); n != 1 {
		t.Errorf("%d submitted events, want 1", n)
	}
}

// "No fixed day off" is a valid choice: day_off null, submit goes through.
func TestD1_Edge_NoFixedDayOff(t *testing.T) {
	w := startD1(t)
	ravi := w.signUpDriver(t, "Ravi Kumar")
	ravi.setDayOff(t, 3)
	ravi.fillAll(t, 0)
	var dayOff *int
	w.one(t, `SELECT day_off FROM drivers WHERE user_id = $1`, []any{ravi.ID}, &dayOff)
	if dayOff != nil {
		t.Errorf("day_off %d, want null", *dayOff)
	}
	ravi.submit(t)
	if st := w.driverStatus(t, ravi); st != "submitted" {
		t.Errorf("status %s, want submitted", st)
	}
	expectStatus(t, ravi.Do(t, http.MethodPut, pathDriverDayOff, dayOffBody(8)), http.StatusConflict, http.StatusBadRequest, http.StatusUnprocessableEntity)
}

// Reviewing an item of a driver who hasn't submitted: 409 wrong_status.
func TestD1_Edge_ReviewNeedsSubmitted(t *testing.T) {
	w := startD1(t)
	ravi := w.signUpDriver(t, "Ravi Kumar")
	ravi.fillAll(t, 7)
	expectError(t, w.review(t, ravi, "licence", "verified", ""), http.StatusConflict, "wrong_status")
	if st := w.itemState(t, ravi, "licence"); st != "uploaded" {
		t.Errorf("licence %s, want uploaded", st)
	}
	expectError(t, w.approve(t, ravi), http.StatusConflict, "wrong_status")
}

// Approve while an item is rejected: 409 items_not_verified.
func TestD1_Edge_ApproveWithRejectedItem(t *testing.T) {
	w := startD1(t)
	ravi := w.submittedDriver(t, "Ravi Kumar", 7)
	// Reject first while the rest are still pending, so the driver stays submitted.
	expectStatus(t, w.review(t, ravi, "licence", "rejected", "photo blurred"), http.StatusOK)
	expectError(t, w.approve(t, ravi), http.StatusConflict, "items_not_verified")
	if st := w.driverStatus(t, ravi); st != "submitted" {
		t.Errorf("status %s, want submitted", st)
	}
}

// One push lists every rejected item.
func TestD1_Edge_OnePushListsEveryRejectedItem(t *testing.T) {
	w := startD1(t)
	ravi := w.submittedDriver(t, "Ravi Kumar", 7)
	w.Sweep(t)
	w.Push.Reset()
	expectStatus(t, w.review(t, ravi, "licence", "rejected", "photo blurred"), http.StatusOK)
	expectStatus(t, w.review(t, ravi, "insurance", "rejected", "expired"), http.StatusOK)
	var rest []string
	for _, it := range reviewItems {
		if it != "licence" && it != "insurance" {
			rest = append(rest, it)
		}
	}
	w.verify(t, ravi, rest...)
	if st := w.driverStatus(t, ravi); st != "changes_needed" {
		t.Fatalf("status %s, want changes_needed", st)
	}
	if n := w.statusEvents(t, ravi.ID, "changes_needed"); n != 1 {
		t.Errorf("%d changes_needed events, want 1", n)
	}
	w.Sweep(t)
	w.expectDriverPushes(t, ravi, pushFixItems, 1)
	for _, m := range w.Push.All() {
		if m.UserID.String() != ravi.ID {
			continue
		}
		b := strings.ToLower(m.Body)
		if !strings.Contains(b, "licence") || !strings.Contains(b, "insurance") {
			t.Errorf("fix push %q should name licence and insurance", m.Body)
		}
	}
	// Both rejected items can be replaced; a verified one can't.
	ravi.upload(t, "licence")
	ravi.upload(t, "insurance")
	expectError(t, ravi.Do(t, http.MethodPut, pathDriverUPI, map[string]string{"upi_id": "x@ybl"}), http.StatusConflict, "locked")
	expectError(t, ravi.Do(t, http.MethodPut, pathDriverDayOff, dayOffBody(3)), http.StatusConflict, "locked")
}

// A rejected vehicle can be replaced in changes_needed (set_vehicle allowed),
// while the documents stay locked.
func TestD1_Edge_RejectedVehicleCanBeReplaced(t *testing.T) {
	w := startD1(t)
	ravi := w.submittedDriver(t, "Ravi Kumar", 7)
	expectStatus(t, w.review(t, ravi, "vehicle", "rejected", "plate unreadable"), http.StatusOK)
	var rest []string
	for _, it := range reviewItems {
		if it != "vehicle" {
			rest = append(rest, it)
		}
	}
	w.verify(t, ravi, rest...)
	if st := w.driverStatus(t, ravi); st != "changes_needed" {
		t.Fatalf("status %s, want changes_needed", st)
	}
	v := autoVehicle(ravi.Plate)
	v.Colour = "Yellow"
	expectStatus(t, ravi.Do(t, http.MethodPut, pathDriverVehicle, v), http.StatusOK)
	expectError(t, ravi.Do(t, http.MethodPost, pathDriverUpload,
		map[string]string{"kind": "licence", "content_type": "image/jpeg"}), http.StatusConflict, "locked")
	ravi.submit(t)
	var colour, state string
	w.one(t, `SELECT colour, review_state::text FROM vehicles WHERE driver_id = $1`, []any{ravi.ID}, &colour, &state)
	if colour != "Yellow" || state == "verified" || state == "rejected" {
		t.Errorf("vehicle %s %s after replacing, want Yellow and pending review", colour, state)
	}
}

// admin.pending lists submitted drivers and claimed payments, oldest first.
func TestD1_Edge_AdminPending(t *testing.T) {
	w := startD1(t)
	ravi := w.submittedDriver(t, "Ravi Kumar", 7)
	w.Advance(t, time.Minute)
	anil := w.submittedDriver(t, "Anil Gowda", 7)
	w.Advance(t, time.Minute)
	meena, pay := w.claimedDriver(t, "Meena Rao", 7)
	w.signUpDriver(t, "Suresh M") // signing_up: not pending

	r := w.adminAPI(t).Do(t, http.MethodGet, pathAdminPending, nil)
	expectStatus(t, r, http.StatusOK)
	m := r.Map(t)
	ds := idList(m, "drivers")
	if fmt.Sprint(ds) != fmt.Sprint([]string{ravi.ID, anil.ID}) {
		t.Errorf("pending drivers %v, want [Ravi %s, Anil %s]", ds, ravi.ID, anil.ID)
	}
	if ps := idList(m, "payments"); fmt.Sprint(ps) != fmt.Sprint([]string{pay}) {
		t.Errorf("pending payments %v, want [%s] (Meena %s)", ps, pay, meena.ID)
	}

	w.verify(t, ravi, reviewItems...)
	expectStatus(t, w.approve(t, ravi), http.StatusOK)
	expectStatus(t, w.confirmPayment(t, pay), http.StatusOK)
	m = w.adminAPI(t).Do(t, http.MethodGet, pathAdminPending, nil).Map(t)
	if ds := idList(m, "drivers"); fmt.Sprint(ds) != fmt.Sprint([]string{anil.ID}) {
		t.Errorf("pending drivers %v, want [Anil]", ds)
	}
	if ps := idList(m, "payments"); len(ps) != 0 {
		t.Errorf("pending payments %v, want none", ps)
	}
}

// admin.show_driver: each file item with a signed read link; 404 unknown driver.
func TestD1_Edge_AdminShowDriver(t *testing.T) {
	w := startD1(t)
	ravi := w.submittedDriver(t, "Ravi Kumar", 7)
	r := w.adminAPI(t).Do(t, http.MethodGet, pathAdminDriver(ravi.ID), nil)
	expectStatus(t, r, http.StatusOK)
	body := anyJSON(t, r)
	for _, k := range fileKinds {
		it := findBy(body, "kind", k)
		if it == nil {
			t.Errorf("show_driver has no %s item: %s", k, r.Body)
			continue
		}
		if u, _ := it["url"].(string); u == "" {
			t.Errorf("%s item has no signed url: %v", k, it)
		}
		if s, _ := it["state"].(string); s != "uploaded" {
			t.Errorf("%s state %q, want uploaded", k, s)
		}
	}
	expectStatus(t, w.adminAPI(t).Do(t, http.MethodGet, pathAdminDriver("00000000-0000-0000-0000-000000000000"), nil), http.StatusNotFound)
}

// Admin pushes: "{name} is waiting for review" on submit and
// "{name} says they've paid ₹{amount}" on claim.
func TestD1_Edge_AdminPushes(t *testing.T) {
	w := startD1(t)
	ravi := w.submittedDriver(t, "Ravi Kumar", 7)
	w.Sweep(t)
	w.expectPushes(t, "Admin", "Ravi Kumar "+pushAdminWaiting, 1)
	w.verify(t, ravi, reviewItems...)
	expectStatus(t, w.approve(t, ravi), http.StatusOK)
	_, due, ps, _ := ravi.plan(t)
	expectStatus(t, ravi.claim(t, ps), http.StatusOK, http.StatusCreated)
	w.Sweep(t)
	w.expectPushes(t, "Admin", fmt.Sprintf("Ravi Kumar %s ₹%d", pushAdminPaid, int(due)), 1)
}

// A rejected application is final: submit, review and approve all 409 wrong_status.
func TestD1_Edge_RejectedIsFinal(t *testing.T) {
	w := startD1(t)
	ravi := w.submittedDriver(t, "Ravi Kumar", 7)
	expectStatus(t, w.adminAPI(t).Do(t, http.MethodPost, pathAdminReject(ravi.ID), map[string]string{"reason": "fake documents"}), http.StatusOK)
	expectError(t, ravi.Do(t, http.MethodPost, pathDriverSubmit, nil), http.StatusConflict, "wrong_status")
	expectError(t, w.approve(t, ravi), http.StatusConflict, "wrong_status")
	expectError(t, w.review(t, ravi, "licence", "verified", ""), http.StatusConflict, "wrong_status")
	expectError(t, w.adminAPI(t).Do(t, http.MethodPost, pathAdminReject(ravi.ID), map[string]string{"reason": "again"}), http.StatusConflict, "wrong_status")
	if st := w.driverStatus(t, ravi); st != "rejected" {
		t.Errorf("status %s, want rejected", st)
	}
}

// An approved driver can't edit documents or vehicle (locked).
func TestD1_Edge_ApprovedIsLocked(t *testing.T) {
	w := startD1(t)
	ravi := w.approvedDriver(t, "Ravi Kumar", 7)
	v := autoVehicle(ravi.Plate)
	v.VehicleType, v.Type = "bike", "bike"
	expectError(t, ravi.Do(t, http.MethodPut, pathDriverVehicle, v), http.StatusConflict, "locked")
	expectError(t, ravi.Do(t, http.MethodPost, pathDriverUpload,
		map[string]string{"kind": "licence", "content_type": "image/jpeg"}), http.StatusConflict, "locked")
	var vtype string
	w.one(t, `SELECT type::text FROM vehicles WHERE driver_id = $1`, []any{ravi.ID}, &vtype)
	if vtype != "auto" {
		t.Errorf("vehicle type changed to %s", vtype)
	}
}

// A rejected payment can't then be confirmed, and a confirmed one can't be rejected.
func TestD1_Edge_PaymentReviewOnlyFromClaimed(t *testing.T) {
	w := startD1(t)
	ravi, pay := w.claimedDriver(t, "Ravi Kumar", 7)
	admin := w.adminAPI(t)
	expectStatus(t, admin.Do(t, http.MethodPost, pathAdminRejectPay(pay), map[string]string{"reason": "not received"}), http.StatusOK)
	expectError(t, w.confirmPayment(t, pay), http.StatusConflict, "wrong_status")
	if st := w.driverStatus(t, ravi); st != "approved" {
		t.Errorf("status %s, want approved", st)
	}
	_, _, ps, _ := ravi.plan(t)
	expectStatus(t, ravi.claim(t, ps), http.StatusOK, http.StatusCreated)
	second := w.latestPayment(t, ravi).ID
	expectStatus(t, w.confirmPayment(t, second), http.StatusOK)
	expectError(t, admin.Do(t, http.MethodPost, pathAdminRejectPay(second), map[string]string{"reason": "x"}), http.StatusConflict, "wrong_status")
	if st := w.driverStatus(t, ravi); st != "live" {
		t.Errorf("status %s, want live", st)
	}
	// A live driver's claim for the same period: already_claimed.
	expectError(t, ravi.claim(t, ps), http.StatusConflict, "already_claimed")
}

// Hand-off: drivers at every pre-live D1 status get no offers; only the live,
// paid one does.
func TestD1_Edge_UnverifiedDriversGetNoOffers(t *testing.T) {
	w := startD1(t)
	signing := w.signUpDriver(t, "Suresh M")
	signing.fillAll(t, 0)
	submitted := w.submittedDriver(t, "Anil Gowda", 0)
	changes := w.submittedDriver(t, "Meena Rao", 0)
	expectStatus(t, w.review(t, changes, "licence", "rejected", "blurred"), http.StatusOK)
	var rest []string
	for _, it := range reviewItems {
		if it != "licence" {
			rest = append(rest, it)
		}
	}
	w.verify(t, changes, rest...)
	_, _ = w.claimedDriver(t, "Kiran Das", 0) // approved, claimed, not confirmed
	kiran := w.drivers["Kiran"]
	live := w.liveDriver(t, "Ravi Kumar", 0)
	if st := w.driverStatus(t, changes); st != "changes_needed" {
		t.Fatalf("Meena %s, want changes_needed", st)
	}

	id, _ := w.createSeries(t, "Priya", priyaReq())
	off := w.openOffer(t, id)
	w.expectOfferTo(t, off.ID, live)
	for _, d := range []*driver{signing, submitted, changes, kiran} {
		if w.everRecipient(t, d) {
			t.Errorf("%s (%s) received an offer", d.Name, w.driverStatus(t, d))
		}
		expectStatus(t, d.Do(t, http.MethodGet, pathOffers, nil), http.StatusForbidden)
	}
}

// Hand-off: a newly live driver can win a series (day off Sunday doesn't clash
// with Mon/Wed/Fri), and his vehicle shows on the rider's series.
func TestD1_Edge_NewDriverWinsSeries(t *testing.T) {
	w := startD1(t)
	ravi := w.liveDriver(t, "Ravi Kumar", 7)
	id, _ := w.createSeries(t, "Priya", priyaReq())
	off := w.openOffer(t, id)
	expectStatus(t, ravi.Do(t, http.MethodPost, pathMarkInterested(off.ID), nil), http.StatusNoContent)
	w.closeWindow(t)
	if st := w.seriesStatus(t, id); st != "active" {
		t.Fatalf("series %s, want active", st)
	}
	for _, s := range w.slots(t, id) {
		if s.DriverID != ravi.ID {
			t.Errorf("slot %d driver %q, want Ravi", s.Weekday, s.DriverID)
		}
	}
	r := w.as(t, "Priya").Do(t, http.MethodGet, pathSeriesID(id), nil)
	expectStatus(t, r, http.StatusOK)
	if v, ok := findKey(anyJSON(t, r), "vehicle_number"); !ok || v != ravi.Plate {
		t.Errorf("slot driver vehicle_number %v, want %s", v, ravi.Plate)
	}
	if n := w.count(t, `SELECT count(*) FROM rides WHERE driver_id = $1`, ravi.ID); n == 0 {
		t.Errorf("no rides for Ravi after winning")
	}
}
