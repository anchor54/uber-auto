package integration

// D1 "Driver Registration, Verification and Subscription": the spec's
// Integration tests D1-I1 .. D1-I15. Edge cases are in d1_edge_test.go;
// paths, seed and JSON shapes are listed at the top of d1_helpers_test.go.
//
// Seed ("d1"): rider Priya, an admin, the plans; no drivers. Each test signs
// its drivers up through the driver app's flow. Clock Sat 21 Nov 2026 10:00 IST.

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"commute-backend/internal/testkit"
)

// D1-I1 Happy path to live: signing_up -> submitted -> approved -> live;
// subscription_until 30 Nov; amount due = price x 10 / 30 rounded up; the
// "approved" and "live" pushes go once each, on the sweep; one events row per
// transition; and from the next offer, Ravi is a recipient.
func TestD1_I1_HappyPathToLive(t *testing.T) {
	w := startD1(t)

	// 1. Sign up.
	ravi := w.signUpDriver(t, "Ravi Kumar")
	if st := w.driverStatus(t, ravi); st != "signing_up" {
		t.Fatalf("after register: status %s, want signing_up", st)
	}
	got := ravi.get(t)
	if st := statusOf(got); st != "signing_up" {
		t.Errorf("driver.get status %q, want signing_up; body %v", st, got)
	}
	ref := strField(got, "reference")
	if !referenceRE.MatchString(ref) {
		t.Errorf("reference %q, want DRV-nnnnn", ref)
	}
	var dbRef string
	w.one(t, `SELECT reference FROM drivers WHERE user_id = $1`, []any{ravi.ID}, &dbRef)
	if dbRef != ref {
		t.Errorf("drivers.reference %q, driver.get says %q", dbRef, ref)
	}

	// 2. Seven documents (six files + UPI), vehicle + three photos, day off Sunday.
	ravi.fillAll(t, 7)
	for _, k := range fileKinds {
		if st := w.itemState(t, ravi, k); st != "uploaded" {
			t.Errorf("item %s state %q, want uploaded", k, st)
		}
		if p, want := w.itemPath(t, ravi, k), "driver-docs/"+ravi.ID+"/"+k; p != want {
			t.Errorf("item %s storage_path %q, want %q", k, p, want)
		}
	}
	var plate, vtype string
	w.one(t, `SELECT plate, type::text FROM vehicles WHERE driver_id = $1`, []any{ravi.ID}, &plate, &vtype)
	if plate != "KA 05 AB 1234" || vtype != "auto" {
		t.Errorf("vehicle %q %q, want auto KA 05 AB 1234", vtype, plate)
	}
	var dayOff *int
	var upi *string
	w.one(t, `SELECT day_off, upi_id FROM drivers WHERE user_id = $1`, []any{ravi.ID}, &dayOff, &upi)
	if dayOff == nil || *dayOff != 7 {
		t.Errorf("day_off %v, want 7 (Sunday)", dayOff)
	}
	if upi == nil || *upi != "ravi@okaxis" {
		t.Errorf("upi_id %v, want ravi@okaxis", upi)
	}

	// 3. Submit.
	ravi.submit(t)
	if st := w.driverStatus(t, ravi); st != "submitted" {
		t.Fatalf("after submit: status %s, want submitted", st)
	}

	// 4. Admin verifies every item and approves.
	w.verify(t, ravi, reviewItems...)
	if st := w.driverStatus(t, ravi); st != "submitted" {
		t.Errorf("after verifying every item: status %s, want submitted until approve", st)
	}
	r := w.approve(t, ravi)
	expectStatus(t, r, http.StatusOK)
	if st := w.driverStatus(t, ravi); st != "approved" {
		t.Fatalf("after approve: status %s, want approved", st)
	}
	if n := w.pushesToID(ravi.ID, pushApproved); n != 0 {
		t.Errorf("approved push sent before the sweep (%d)", n)
	}
	w.Sweep(t)
	w.expectDriverPushes(t, ravi, pushApproved, 1)
	w.expectPushes(t, "Admin", "Ravi Kumar "+pushAdminWaiting, 1)

	// 5. Plan and amount due: 21..30 Nov = 10 of 30 days.
	price, due, ps, pe := ravi.plan(t)
	if want := w.monthlyPrice(t, "auto"); price != want {
		t.Errorf("monthly_price %v, want %v (plans row for auto)", price, want)
	}
	if want := prorated(price, 10, 30); due != want {
		t.Errorf("amount_due %v, want %v (price %v x 10 / 30 rounded up)", due, want, price)
	}
	// SPEC-AMBIGUITY: the first period runs from today (D1.7 "Pay now
	// ({today}-{last day of month})") to the month's last day.
	if ps != "2026-11-21" || pe != "2026-11-30" {
		t.Errorf("period %s..%s, want 2026-11-21..2026-11-30", ps, pe)
	}

	// Claim.
	r = ravi.claim(t, ps)
	expectStatus(t, r, http.StatusOK, http.StatusCreated)
	pay := w.latestPayment(t, ravi)
	if pay.Status != "claimed" || pay.Amount != due || pay.PeriodEnd != "2026-11-30" {
		t.Errorf("payment %+v, want claimed, amount %v, period_end 2026-11-30", pay, due)
	}
	if st := w.driverStatus(t, ravi); st != "approved" {
		t.Errorf("after claim: status %s, want approved until confirmed", st)
	}

	// 6. Admin confirms.
	r = w.confirmPayment(t, pay.ID)
	expectStatus(t, r, http.StatusOK)
	if st := w.driverStatus(t, ravi); st != "live" {
		t.Fatalf("after confirm: status %s, want live", st)
	}
	if su := w.subscriptionUntil(t, ravi); su != "2026-11-30" {
		t.Errorf("subscription_until %q, want 2026-11-30", su)
	}
	if p := w.latestPayment(t, ravi); p.Status != "confirmed" {
		t.Errorf("payment status %s, want confirmed", p.Status)
	}
	if n := w.pushesToID(ravi.ID, pushLive); n != 0 {
		t.Errorf("live push sent before the sweep (%d)", n)
	}
	w.Sweep(t)
	w.expectDriverPushes(t, ravi, pushLive, 1)
	w.expectDriverPushes(t, ravi, pushApproved, 1)
	w.expectPushes(t, "Admin", "Ravi Kumar "+pushAdminPaid, 1)
	for _, m := range w.Push.All() {
		if m.UserID.String() == ravi.ID && strings.Contains(m.Title+" "+m.Body, pushLive) &&
			!strings.Contains(strings.ToLower(m.Body), "auto") {
			t.Errorf("live push %q doesn't name the vehicle type (auto)", m.Body)
		}
	}

	// One events row per transition.
	for _, st := range []string{"submitted", "approved", "live"} {
		if n := w.statusEvents(t, ravi.ID, st); n != 1 {
			t.Errorf("%d events with new_status %s for Ravi, want 1", n, st)
		}
	}
	if n := w.statusEvents(t, pay.ID, "confirmed"); n != 1 {
		t.Errorf("%d events with new_status confirmed for the payment, want 1", n)
	}
	if n := w.eventsFor(t, ravi.ID); n < 4 {
		t.Errorf("%d events for Ravi, want at least 4 (register + 3 transitions)", n)
	}

	// driver.get reflects it.
	got = ravi.get(t)
	if st := statusOf(got); st != "live" {
		t.Errorf("driver.get status %q, want live", st)
	}

	// 7. From the next offer that opens, Ravi is a recipient.
	id, _ := w.createSeries(t, "Priya", priyaReq())
	off := w.openOffer(t, id)
	w.expectOfferTo(t, off.ID, ravi)
	w.Sweep(t)
	w.expectDriverPushes(t, ravi, pushNewRequest, 1)
	r = ravi.Do(t, http.MethodGet, pathOffers, nil)
	expectStatus(t, r, http.StatusOK)
	if findObj(anyJSON(t, r), off.ID) == nil {
		t.Errorf("offers.list for Ravi doesn't include offer %s: %s", off.ID, r.Body)
	}
}

// D1-I2 Live driver receives offers; others don't: Ravi live, Anil approved
// (unpaid), Meena live with subscription_until = yesterday -> recipients = Ravi.
func TestD1_I2_OnlyLivePaidDriversGetOffers(t *testing.T) {
	w := startD1(t)
	ravi := w.liveDriver(t, "Ravi Kumar", 0)
	anil := w.approvedDriver(t, "Anil Gowda", 0)
	meena := w.liveDriver(t, "Meena Rao", 0)
	w.exec(t, `UPDATE drivers SET subscription_until = $2 WHERE user_id = $1`, meena.ID, "2026-11-20")
	w.Sweep(t)
	w.Push.Reset()

	id, _ := w.createSeries(t, "Priya", priyaReq())
	off := w.openOffer(t, id)
	w.expectOfferTo(t, off.ID, ravi)

	w.Sweep(t)
	w.expectDriverPushes(t, ravi, pushNewRequest, 1)
	w.expectDriverPushes(t, anil, pushNewRequest, 0)
	w.expectDriverPushes(t, meena, pushNewRequest, 0)

	r := ravi.Do(t, http.MethodGet, pathOffers, nil)
	expectStatus(t, r, http.StatusOK)
	if findObj(anyJSON(t, r), off.ID) == nil {
		t.Errorf("Ravi's offers.list lacks %s: %s", off.ID, r.Body)
	}
	expectStatus(t, anil.Do(t, http.MethodGet, pathOffers, nil), http.StatusForbidden)
	expectError(t, meena.Do(t, http.MethodGet, pathOffers, nil), http.StatusForbidden, "not_eligible")
	expectStatus(t, anil.Do(t, http.MethodGet, pathOffer(off.ID), nil), http.StatusForbidden, http.StatusNotFound)
}

// D1-I3 Submit with missing items: 5 of the 7 documents -> 409 incomplete
// listing the two kinds; status stays signing_up.
func TestD1_I3_SubmitIncomplete(t *testing.T) {
	w := startD1(t)
	ravi := w.signUpDriver(t, "Ravi Kumar")
	// Five of seven: four files + UPI; insurance and permit missing.
	// Everything else (vehicle, photos, day off) is present so only the two
	// documents are missing.
	for _, k := range []string{"profile_photo", "id_proof", "licence", "rc"} {
		ravi.upload(t, k)
	}
	ravi.setUPI(t)
	ravi.setVehicle(t)
	for _, k := range photoKinds {
		ravi.upload(t, k)
	}
	ravi.setDayOff(t, 7)

	r := ravi.Do(t, http.MethodPost, pathDriverSubmit, nil)
	expectError(t, r, http.StatusConflict, "incomplete")
	missing := missingList(t, r)
	for _, k := range []string{"insurance", "permit"} {
		if !contains(missing, k) {
			t.Errorf("missing list %v lacks %s", missing, k)
		}
	}
	for _, k := range missing {
		if k != "insurance" && k != "permit" {
			t.Errorf("missing list %v names %q, want only insurance and permit", missing, k)
		}
	}
	if st := w.driverStatus(t, ravi); st != "signing_up" {
		t.Errorf("status %s, want signing_up", st)
	}
	if n := w.statusEvents(t, ravi.ID, "submitted"); n != 0 {
		t.Errorf("%d submitted events after a failed submit", n)
	}
	w.Sweep(t)
	w.expectPushes(t, "Admin", pushAdminWaiting, 0)

	// Adding the two lets the submit through.
	ravi.upload(t, "insurance")
	ravi.upload(t, "permit")
	ravi.submit(t)
	if st := w.driverStatus(t, ravi); st != "submitted" {
		t.Errorf("status %s after completing, want submitted", st)
	}
}

// D1-I4 Rejected item and resubmission: licence rejected ("photo blurred"),
// the rest verified -> changes_needed; one push naming the licence; RC again
// -> 409 locked; replacing the licence works; submit -> submitted; verified
// items keep verified.
func TestD1_I4_RejectedItemResubmit(t *testing.T) {
	w := startD1(t)
	ravi := w.submittedDriver(t, "Ravi Kumar", 7)
	w.Sweep(t)
	w.Push.Reset()

	expectStatus(t, w.review(t, ravi, "licence", "rejected", "photo blurred"), http.StatusOK)
	// The driver moves only when the last pending item is reviewed.
	if st := w.driverStatus(t, ravi); st != "submitted" {
		t.Errorf("after one rejection with items pending: status %s, want submitted", st)
	}
	var rest []string
	for _, it := range reviewItems {
		if it != "licence" {
			rest = append(rest, it)
		}
	}
	w.verify(t, ravi, rest...)
	if st := w.driverStatus(t, ravi); st != "changes_needed" {
		t.Fatalf("after the review: status %s, want changes_needed", st)
	}
	if st := w.itemState(t, ravi, "licence"); st != "rejected" {
		t.Errorf("licence state %s, want rejected", st)
	}
	var reason string
	w.one(t, `SELECT coalesce(reject_reason, '') FROM driver_items WHERE driver_id = $1 AND kind::text = 'licence'`,
		[]any{ravi.ID}, &reason)
	if reason != "photo blurred" {
		t.Errorf("licence reject_reason %q, want photo blurred", reason)
	}
	// driver.get shows the item's state and reason.
	got := ravi.get(t)
	if it := findBy(got, "kind", "licence"); it == nil {
		t.Errorf("driver.get has no licence item: %v", got)
	} else {
		if s, _ := it["state"].(string); s != "rejected" {
			t.Errorf("driver.get licence state %q, want rejected", s)
		}
		if s, _ := it["reject_reason"].(string); s != "photo blurred" {
			t.Errorf("driver.get licence reject_reason %q, want photo blurred", s)
		}
	}

	// One push naming the licence.
	w.Sweep(t)
	w.expectDriverPushes(t, ravi, pushFixItems, 1)
	for _, m := range w.Push.All() {
		if m.UserID.String() == ravi.ID && strings.Contains(m.Title+" "+m.Body, pushFixItems) &&
			!strings.Contains(strings.ToLower(m.Body), "licence") {
			t.Errorf("fix push %q doesn't name the licence", m.Body)
		}
	}
	if n := len(w.pushTexts(ravi.ID)); n != 1 {
		t.Errorf("Ravi got %d pushes, want 1: %q", n, w.pushTexts(ravi.ID))
	}

	// Verified items are locked.
	rcPath := w.itemPath(t, ravi, "rc")
	expectError(t, ravi.Do(t, http.MethodPost, pathDriverUpload,
		map[string]string{"kind": "rc", "content_type": "image/jpeg"}), http.StatusConflict, "locked")
	expectError(t, ravi.Do(t, http.MethodPut, pathDriverDocument("rc"),
		map[string]string{"storage_path": rcPath}), http.StatusConflict, "locked")
	if st := w.itemState(t, ravi, "rc"); st != "verified" {
		t.Errorf("rc state %s after the refused upload, want verified", st)
	}

	// Replacing the licence works.
	ravi.upload(t, "licence")
	if st := w.itemState(t, ravi, "licence"); st != "uploaded" {
		t.Errorf("licence state %s after replacing, want uploaded", st)
	}
	ravi.submit(t)
	if st := w.driverStatus(t, ravi); st != "submitted" {
		t.Fatalf("after resubmit: status %s, want submitted", st)
	}
	for _, k := range fileKinds {
		if k == "licence" {
			continue
		}
		if st := w.itemState(t, ravi, k); st != "verified" {
			t.Errorf("%s state %s after resubmit, want verified (kept)", k, st)
		}
	}
	var vstate string
	w.one(t, `SELECT review_state::text FROM vehicles WHERE driver_id = $1`, []any{ravi.ID}, &vstate)
	if vstate != "verified" {
		t.Errorf("vehicle review_state %s after resubmit, want verified (kept)", vstate)
	}

	// Only the licence needs review now.
	w.verify(t, ravi, "licence")
	expectStatus(t, w.approve(t, ravi), http.StatusOK)
	if st := w.driverStatus(t, ravi); st != "approved" {
		t.Errorf("status %s, want approved", st)
	}
}

// D1-I5 Locked after submit: set_vehicle, set_day_off, upload_url -> 409
// locked; nothing changed.
func TestD1_I5_LockedAfterSubmit(t *testing.T) {
	w := startD1(t)
	ravi := w.submittedDriver(t, "Ravi Kumar", 7)
	licPath := w.itemPath(t, ravi, "licence")

	v := autoVehicle("KA 09 ZZ 9999")
	v.Colour = "Yellow"
	expectError(t, ravi.Do(t, http.MethodPut, pathDriverVehicle, v), http.StatusConflict, "locked")
	expectError(t, ravi.Do(t, http.MethodPut, pathDriverDayOff, dayOffBody(3)), http.StatusConflict, "locked")
	expectError(t, ravi.Do(t, http.MethodPost, pathDriverUpload,
		map[string]string{"kind": "licence", "content_type": "image/jpeg"}), http.StatusConflict, "locked")
	// The other item calls are locked too.
	expectError(t, ravi.Do(t, http.MethodPut, pathDriverDocument("licence"),
		map[string]string{"storage_path": licPath}), http.StatusConflict, "locked")
	expectError(t, ravi.Do(t, http.MethodPut, pathDriverUPI,
		map[string]string{"upi_id": "other@ybl"}), http.StatusConflict, "locked")

	var plate, colour string
	w.one(t, `SELECT plate, colour FROM vehicles WHERE driver_id = $1`, []any{ravi.ID}, &plate, &colour)
	if plate != ravi.Plate || colour != "Green" {
		t.Errorf("vehicle changed to %s %s", plate, colour)
	}
	var dayOff *int
	var upi *string
	w.one(t, `SELECT day_off, upi_id FROM drivers WHERE user_id = $1`, []any{ravi.ID}, &dayOff, &upi)
	if dayOff == nil || *dayOff != 7 {
		t.Errorf("day_off changed to %v", dayOff)
	}
	if upi == nil || *upi != "ravi@okaxis" {
		t.Errorf("upi_id changed to %v", upi)
	}
	for _, k := range fileKinds {
		if st := w.itemState(t, ravi, k); st != "uploaded" {
			t.Errorf("%s state %s, want uploaded", k, st)
		}
	}
	if st := w.driverStatus(t, ravi); st != "submitted" {
		t.Errorf("status %s, want submitted", st)
	}
	if n := w.count(t, `SELECT count(*) FROM vehicles WHERE plate = 'KA 09 ZZ 9999'`); n != 0 {
		t.Errorf("a vehicle with the refused plate exists")
	}
}

// D1-I6 Approve too early: six items verified -> 409 items_not_verified.
func TestD1_I6_ApproveTooEarly(t *testing.T) {
	w := startD1(t)
	ravi := w.submittedDriver(t, "Ravi Kumar", 7)
	w.verify(t, ravi, docKinds...) // six items
	expectError(t, w.approve(t, ravi), http.StatusConflict, "items_not_verified")
	if st := w.driverStatus(t, ravi); st != "submitted" {
		t.Errorf("status %s, want submitted", st)
	}

	// Every item but one: still refused.
	w.verify(t, ravi, "upi", "vehicle", "vehicle_front", "vehicle_back")
	expectError(t, w.approve(t, ravi), http.StatusConflict, "items_not_verified")
	if st := w.driverStatus(t, ravi); st != "submitted" {
		t.Errorf("status %s, want submitted", st)
	}
	if n := w.statusEvents(t, ravi.ID, "approved"); n != 0 {
		t.Errorf("%d approved events", n)
	}
	w.Sweep(t)
	w.expectDriverPushes(t, ravi, pushApproved, 0)
}

// D1-I7 Rejected application: status rejected; push; claim_payment -> 409
// not_approved; not a recipient of any offer.
func TestD1_I7_RejectedApplication(t *testing.T) {
	w := startD1(t)
	ravi := w.submittedDriver(t, "Ravi Kumar", 7)
	anil := w.liveDriver(t, "Anil Gowda", 0) // so an offer opens
	w.Sweep(t)
	w.Push.Reset()

	r := w.adminAPI(t).Do(t, http.MethodPost, pathAdminReject(ravi.ID), map[string]string{"reason": "licence not valid for commercial use"})
	expectStatus(t, r, http.StatusOK)
	if st := w.driverStatus(t, ravi); st != "rejected" {
		t.Fatalf("status %s, want rejected", st)
	}
	var reason string
	w.one(t, `SELECT coalesce(reject_reason, '') FROM drivers WHERE user_id = $1`, []any{ravi.ID}, &reason)
	if reason != "licence not valid for commercial use" {
		t.Errorf("reject_reason %q", reason)
	}
	got := ravi.get(t)
	if st := statusOf(got); st != "rejected" {
		t.Errorf("driver.get status %q, want rejected", st)
	}
	if s := strField(got, "reject_reason"); s != "licence not valid for commercial use" {
		t.Errorf("driver.get reject_reason %q", s)
	}
	if n := w.statusEvents(t, ravi.ID, "rejected"); n != 1 {
		t.Errorf("%d rejected events, want 1", n)
	}
	w.Sweep(t)
	w.expectDriverPushes(t, ravi, pushAppRejected, 1)

	expectError(t, ravi.claim(t, "2026-11-21"), http.StatusConflict, "not_approved")
	expectError(t, ravi.Do(t, http.MethodGet, pathDriverPlan, nil), http.StatusConflict, "not_approved")
	if n := len(w.payments(t, ravi)); n != 0 {
		t.Errorf("%d payment rows for a rejected driver", n)
	}
	// Read only.
	expectError(t, ravi.Do(t, http.MethodPost, pathDriverUpload,
		map[string]string{"kind": "licence", "content_type": "image/jpeg"}), http.StatusConflict, "locked")

	id, _ := w.createSeries(t, "Priya", priyaReq())
	off := w.openOffer(t, id)
	w.expectOfferTo(t, off.ID, anil)
	if w.everRecipient(t, ravi) {
		t.Errorf("rejected Ravi received an offer")
	}
	w.Sweep(t)
	w.expectDriverPushes(t, ravi, pushNewRequest, 0)
}

// D1-I8 Concurrent payment confirmation: exactly one 200 and one 409; one
// "live" push; subscription_until written once; one events row.
func TestD1_I8_ConcurrentConfirm(t *testing.T) {
	w := startD1(t)
	ravi, pay := w.claimedDriver(t, "Ravi Kumar", 7)
	w.Sweep(t)
	w.Push.Reset()
	admin := w.adminAPI(t)

	var wg sync.WaitGroup
	resps := make([]*testkit.Resp, 2)
	errs := make([]error, 2)
	gate := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-gate
			resps[i], errs[i] = admin.raw(http.MethodPost, pathAdminConfirmPay(pay), nil)
		}(i)
	}
	close(gate)
	wg.Wait()
	ok, conflict := 0, 0
	for i, r := range resps {
		if errs[i] != nil {
			t.Fatalf("confirm %d: %v", i, errs[i])
		}
		switch r.Status {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
			expectError(t, r, http.StatusConflict, "wrong_status")
		default:
			t.Errorf("confirm %d: status %d %s", i, r.Status, r.Body)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("got %d x 200 and %d x 409, want one each", ok, conflict)
	}
	if st := w.driverStatus(t, ravi); st != "live" {
		t.Errorf("status %s, want live", st)
	}
	if su := w.subscriptionUntil(t, ravi); su != "2026-11-30" {
		t.Errorf("subscription_until %q, want 2026-11-30", su)
	}
	if n := w.statusEvents(t, pay, "confirmed"); n != 1 {
		t.Errorf("%d confirmed events for the payment, want 1", n)
	}
	if n := w.statusEvents(t, ravi.ID, "live"); n != 1 {
		t.Errorf("%d live events for Ravi, want 1", n)
	}
	if n := w.count(t, `SELECT count(*) FROM subscription_payments WHERE driver_id = $1 AND status::text = 'confirmed'`, ravi.ID); n != 1 {
		t.Errorf("%d confirmed payments, want 1", n)
	}
	w.Sweep(t)
	w.expectDriverPushes(t, ravi, pushLive, 1)

	// A third, later confirm is refused too.
	expectError(t, w.confirmPayment(t, pay), http.StatusConflict, "wrong_status")
	w.Sweep(t)
	w.expectDriverPushes(t, ravi, pushLive, 1)
}

// D1-I9 Double claim: second call 409 already_claimed; one payment row.
func TestD1_I9_DoubleClaim(t *testing.T) {
	w := startD1(t)
	ravi := w.approvedDriver(t, "Ravi Kumar", 7)
	_, _, ps, _ := ravi.plan(t)
	expectStatus(t, ravi.claim(t, ps), http.StatusOK, http.StatusCreated)
	expectError(t, ravi.claim(t, ps), http.StatusConflict, "already_claimed")
	if n := len(w.payments(t, ravi)); n != 1 {
		t.Errorf("%d payment rows, want 1", n)
	}
	if p := w.latestPayment(t, ravi); p.Status != "claimed" {
		t.Errorf("payment status %s, want claimed", p.Status)
	}
	// The waiting state comes from driver.get's latest payment.
	got := ravi.get(t)
	lp, ok := got["latest_payment"].(map[string]any)
	if !ok {
		t.Fatalf("driver.get has no latest_payment: %v", got)
	}
	if s, _ := lp["status"].(string); s != "claimed" {
		t.Errorf("latest_payment.status %q, want claimed", s)
	}
	w.Sweep(t)
	w.expectPushes(t, "Admin", pushAdminPaid, 1)
}

// D1-I10 Payment rejected, then claimed again: first row rejected, second
// confirmed; driver live.
func TestD1_I10_PaymentRejectedThenClaimedAgain(t *testing.T) {
	w := startD1(t)
	ravi, first := w.claimedDriver(t, "Ravi Kumar", 7)
	w.Sweep(t)
	w.Push.Reset()

	r := w.adminAPI(t).Do(t, http.MethodPost, pathAdminRejectPay(first), map[string]string{"reason": "not received"})
	expectStatus(t, r, http.StatusOK)
	if st := w.driverStatus(t, ravi); st != "approved" {
		t.Errorf("after reject-payment: status %s, want approved", st)
	}
	got := ravi.get(t)
	if lp, ok := got["latest_payment"].(map[string]any); !ok {
		t.Errorf("driver.get has no latest_payment: %v", got)
	} else {
		if s, _ := lp["status"].(string); s != "rejected" {
			t.Errorf("latest_payment.status %q, want rejected", s)
		}
		if s, _ := lp["reject_reason"].(string); s != "not received" {
			t.Errorf("latest_payment.reject_reason %q, want not received", s)
		}
	}
	w.Sweep(t)
	w.expectDriverPushes(t, ravi, pushPayRejected, 1)

	_, _, ps, _ := ravi.plan(t)
	expectStatus(t, ravi.claim(t, ps), http.StatusOK, http.StatusCreated)
	pays := w.payments(t, ravi)
	if len(pays) != 2 {
		t.Fatalf("%d payment rows, want 2: %+v", len(pays), pays)
	}
	expectStatus(t, w.confirmPayment(t, pays[1].ID), http.StatusOK)

	pays = w.payments(t, ravi)
	if pays[0].ID != first || pays[0].Status != "rejected" || pays[0].RejectReason != "not received" {
		t.Errorf("first payment %+v, want rejected (not received)", pays[0])
	}
	if pays[1].Status != "confirmed" {
		t.Errorf("second payment %+v, want confirmed", pays[1])
	}
	if st := w.driverStatus(t, ravi); st != "live" {
		t.Errorf("status %s, want live", st)
	}
	if su := w.subscriptionUntil(t, ravi); su != "2026-11-30" {
		t.Errorf("subscription_until %q, want 2026-11-30", su)
	}
	w.Sweep(t)
	w.expectDriverPushes(t, ravi, pushLive, 1)
}

// D1-I11 Proration at month end: approved at 30 Nov 23:30 IST -> amount due
// covers one day (30 Nov), period ends 30 Nov; at 1 Dec the D5 renewal is due.
func TestD1_I11_ProrationAtMonthEnd(t *testing.T) {
	w := startD1(t)
	ravi := w.submittedDriver(t, "Ravi Kumar", 7)
	w.setClock(time.Date(2026, 11, 30, 23, 30, 0, 0, ist))
	w.verify(t, ravi, reviewItems...)
	expectStatus(t, w.approve(t, ravi), http.StatusOK)

	price, due, ps, pe := ravi.plan(t)
	if want := prorated(price, 1, 30); due != want {
		t.Errorf("amount_due %v, want %v (one day of %v)", due, want, price)
	}
	if ps != "2026-11-30" || pe != "2026-11-30" {
		t.Errorf("period %s..%s, want 2026-11-30..2026-11-30", ps, pe)
	}
	expectStatus(t, ravi.claim(t, ps), http.StatusOK, http.StatusCreated)
	pay := w.latestPayment(t, ravi)
	if pay.Amount != due || pay.PeriodEnd != "2026-11-30" {
		t.Errorf("payment %+v, want amount %v ending 2026-11-30", pay, due)
	}
	w.Advance(t, 5*time.Minute)
	expectStatus(t, w.confirmPayment(t, pay.ID), http.StatusOK)
	if su := w.subscriptionUntil(t, ravi); su != "2026-11-30" {
		t.Errorf("subscription_until %q, want 2026-11-30", su)
	}
	// Still 30 Nov: eligible.
	expectStatus(t, ravi.Do(t, http.MethodGet, pathOffers, nil), http.StatusOK)

	// SPEC-AMBIGUITY: "at 1 Dec the D5 renewal is due". D5 isn't built; the
	// checkable part is that the paid-up rule (subscription_until >= today)
	// stops Ravi receiving offers from 1 Dec until he renews.
	w.setClock(time.Date(2026, 12, 1, 0, 30, 0, 0, ist))
	expectError(t, ravi.Do(t, http.MethodGet, pathOffers, nil), http.StatusForbidden, "not_eligible")
	id, _ := w.createSeries(t, "Priya", priyaReq())
	_ = id
	if w.everRecipient(t, ravi) {
		t.Errorf("Ravi received an offer on 1 Dec with subscription_until 30 Nov")
	}
}

// D1-I12 Day off feeds the clash check: Ravi live with day off WED; a rider
// creates Mon/Wed/Fri 08:30; Ravi is interested; close -> clashed, not assigned.
func TestD1_I12_DayOffClash(t *testing.T) {
	w := startD1(t)
	ravi := w.liveDriver(t, "Ravi Kumar", 3)
	id, _ := w.createSeries(t, "Priya", priyaReq())
	off := w.openOffer(t, id)
	w.expectOfferTo(t, off.ID, ravi) // the day off doesn't stop the offer
	expectStatus(t, ravi.Do(t, http.MethodPost, pathMarkInterested(off.ID), nil), http.StatusNoContent)
	if rec, ok := w.recipients(t, off.ID)[ravi.ID]; !ok || !rec.Interested {
		t.Fatalf("Ravi's interest not recorded: %+v", rec)
	}
	w.closeWindow(t)

	if res := w.recipients(t, off.ID)[ravi.ID].Result; res != "clashed" {
		t.Errorf("Ravi's result %q, want clashed", res)
	}
	for _, s := range w.slots(t, id) {
		if s.DriverID == ravi.ID {
			t.Errorf("slot weekday %d assigned to Ravi", s.Weekday)
		}
	}
	if st := w.seriesStatus(t, id); st == "active" {
		t.Errorf("series active, want not assigned")
	}
	if n := w.count(t, `SELECT count(*) FROM rides WHERE driver_id = $1`, ravi.ID); n != 0 {
		t.Errorf("%d rides for Ravi", n)
	}
}

// D1-I13 Storage path ownership: Ravi calls item_uploaded with Anil's path
// -> 403 not_your_path; nothing saved.
func TestD1_I13_StoragePathOwnership(t *testing.T) {
	w := startD1(t)
	ravi := w.signUpDriver(t, "Ravi Kumar")
	anil := w.signUpDriver(t, "Anil Gowda")
	u, anilPath := anil.uploadURL(t, "licence")
	putFile(t, u)
	if want := "driver-docs/" + anil.ID + "/licence"; anilPath != want {
		t.Errorf("Anil's storage_path %q, want %q", anilPath, want)
	}

	expectError(t, ravi.Do(t, http.MethodPut, pathDriverDocument("licence"),
		map[string]string{"storage_path": anilPath}), http.StatusForbidden, "not_your_path")
	if st := w.itemState(t, ravi, "licence"); st != "" {
		t.Errorf("Ravi has a licence item (%s) after not_your_path", st)
	}
	if st := w.itemState(t, anil, "licence"); st != "" {
		t.Errorf("Anil has a licence item (%s); he never confirmed the upload", st)
	}
	if n := w.count(t, `SELECT count(*) FROM driver_items WHERE storage_path = $1`, anilPath); n != 0 {
		t.Errorf("%d items reference Anil's path", n)
	}
	// A path outside driver-docs/{driver_id}/ is refused the same way.
	expectError(t, ravi.Do(t, http.MethodPut, pathDriverDocument("licence"),
		map[string]string{"storage_path": "driver-docs/" + ravi.ID + "/../" + anil.ID + "/licence"}),
		http.StatusForbidden, "not_your_path")
	expectError(t, ravi.Do(t, http.MethodPut, pathDriverDocument("licence"),
		map[string]string{"storage_path": "elsewhere/" + ravi.ID + "/licence"}),
		http.StatusForbidden, "not_your_path")
	if st := w.itemState(t, ravi, "licence"); st != "" {
		t.Errorf("Ravi has a licence item (%s)", st)
	}
}

// D1-I14 Roles: a rider token calls driver.submit; a driver token calls
// admin.approve -> both 403.
func TestD1_I14_Roles(t *testing.T) {
	w := startD1(t)
	ravi := w.submittedDriver(t, "Ravi Kumar", 7)
	w.verify(t, ravi, reviewItems...)

	expectStatus(t, w.asAPI(t, "Priya").Do(t, http.MethodPost, pathDriverSubmit, nil), http.StatusForbidden)
	expectStatus(t, ravi.Do(t, http.MethodPost, pathAdminApprove(ravi.ID), nil), http.StatusForbidden)
	if st := w.driverStatus(t, ravi); st != "submitted" {
		t.Errorf("status %s after a driver's approve, want submitted", st)
	}
	// The rest of the admin surface is closed to drivers and riders too.
	for _, c := range []*api{ravi.api, w.asAPI(t, "Priya")} {
		expectStatus(t, c.Do(t, http.MethodGet, pathAdminPending, nil), http.StatusForbidden)
		expectStatus(t, c.Do(t, http.MethodGet, pathAdminDriver(ravi.ID), nil), http.StatusForbidden)
		expectStatus(t, c.Do(t, http.MethodPost, pathAdminReviewItem(ravi.ID),
			map[string]string{"item": "licence", "state": "rejected", "reason": "x"}), http.StatusForbidden)
	}
	// No token at all.
	expectStatus(t, (&api{base: w.BaseURL}).Do(t, http.MethodPost, pathDriverSubmit, nil), http.StatusUnauthorized)
	// The admin is not a driver.
	expectStatus(t, w.adminAPI(t).Do(t, http.MethodPost, pathDriverSubmit, nil), http.StatusForbidden)
}

// D1-I15 Plate uniqueness: Anil sets the plate Ravi already uses, in lower
// case -> 409 plate_in_use.
func TestD1_I15_PlateUniqueness(t *testing.T) {
	w := startD1(t)
	ravi := w.signUpDriver(t, "Ravi Kumar")
	anil := w.signUpDriver(t, "Anil Gowda")
	ravi.setVehicle(t) // KA 05 AB 1234

	r := anil.Do(t, http.MethodPut, pathDriverVehicle, autoVehicle(strings.ToLower(ravi.Plate)))
	expectError(t, r, http.StatusConflict, "plate_in_use")
	if n := w.count(t, `SELECT count(*) FROM vehicles WHERE driver_id = $1`, anil.ID); n != 0 {
		t.Errorf("Anil has %d vehicle rows after plate_in_use", n)
	}
	if n := w.count(t, `SELECT count(*) FROM vehicles WHERE upper(plate) = $1`, ravi.Plate); n != 1 {
		t.Errorf("%d vehicles with plate %s, want 1", n, ravi.Plate)
	}

	// A lower-case plate of his own is stored upper-cased.
	own := fmt.Sprintf("ka 05 ab %s", "9999")
	expectStatus(t, anil.Do(t, http.MethodPut, pathDriverVehicle, autoVehicle(own)), http.StatusOK)
	var plate string
	w.one(t, `SELECT plate FROM vehicles WHERE driver_id = $1`, []any{anil.ID}, &plate)
	if plate != "KA 05 AB 9999" {
		t.Errorf("stored plate %q, want KA 05 AB 9999", plate)
	}
}
