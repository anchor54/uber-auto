package integration

// R2+D3 edge cases and endpoint errors that the spec's Edge cases, Endpoints
// and Sweeper tables make checkable. Contract: r2_d3_helpers_test.go.

import (
	"net/http"
	"strings"
	"testing"
)

// "On my way allowed from 60 min before pickup": 07:29 too early, 07:30 works.
func TestD3_Edge_OnMyWayOpensAt60Minutes(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)
	w.setAt(18, 7, 29)
	expectErrorWithState(t, w.drv(t, "Ravi", id, actOnMyWay, nil), http.StatusConflict, "too_early")
	w.setAt(18, 7, 30)
	w.drvOK(t, "Ravi", id, actOnMyWay, nil)
	w.expectRideStatus(t, id, "on_the_way")
	// Tapping again is wrong_status (already on the way).
	expectErrorWithState(t, w.drv(t, "Ravi", id, actOnMyWay, nil), http.StatusConflict, "wrong_status")
}

// The check runs at pickup - 15 exactly, not before.
func TestD3_Edge_CheckBoundary(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)
	w.setAt(18, 8, 14)
	w.Sweep(t)
	w.expectRideStatus(t, id, "assigned")
	if n := len(w.rideOffers(t, id)); n != 0 {
		t.Errorf("%d offers at 08:14", n)
	}
	w.setAt(18, 8, 15)
	w.Sweep(t)
	w.expectRideStatus(t, id, "needs_driver")
}

// "Ravi taps On my way after the ride went to someone else": 409
// ride_reassigned; Today (the run) no longer lists the ride.
func TestD3_Edge_OnMyWayAfterHandover(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)
	w.setAt(18, 8, 15)
	w.Sweep(t)
	w.setAt(18, 8, 16)
	expectErrorWithState(t, w.drv(t, "Ravi", id, actOnMyWay, nil), http.StatusConflict, "ride_reassigned")
	w.expectRideStatus(t, id, "needs_driver")

	r := w.as(t, "Ravi").Do(t, http.MethodGet, pathDrvRunFrom("2026-11-18", 1), nil)
	expectStatus(t, r, http.StatusOK)
	if findObj(anyJSON(t, r), id) != nil {
		t.Errorf("Ravi's run still lists the handed-over ride: %s", r.Body)
	}
	if findObj(anyJSON(t, r), w.rideOn(t, "Ankit", "2026-11-18")) == nil {
		t.Errorf("Ravi's run lost Ankit's 09:50 ride: %s", r.Body)
	}
}

// The check leaves a ride that is already on the way alone.
func TestD3_Edge_CheckIgnoresRideOnTheWay(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)
	w.setAt(18, 8, 9)
	w.drvOK(t, "Ravi", id, actOnMyWay, nil)
	w.setAt(18, 8, 15)
	w.Sweep(t)
	w.setAt(18, 8, 31)
	w.Sweep(t)
	w.expectRideStatus(t, id, "on_the_way")
	if n := len(w.rideOffers(t, id)); n != 0 {
		t.Errorf("%d offers for a ride on the way", n)
	}
	w.expectPushes(t, "Priya", pushR2NotStarted, 0)
	w.expectPushes(t, "Admin", pushR2Outage, 0)
}

// Outage boundary: the first check runs exactly at pickup -> failed, no offer.
func TestR2_Edge_OutageAtPickupTime(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)
	w.setAt(18, 8, 30)
	w.Sweep(t)
	r := w.expectRideStatus(t, id, "failed")
	if got := r.str("fail_reason"); got != "missed_by_outage" {
		t.Errorf("fail_reason %q, want missed_by_outage", got)
	}
	if n := len(w.rideOffers(t, id)); n != 0 {
		t.Errorf("%d offers, want none", n)
	}
	w.expectSomePush(t, "Admin", pushR2Outage)
}

// close_stale_rides: due at pickup + 4 h (12:30), not before.
func TestD3_Edge_StaleCloseBoundary(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)
	w.setAt(18, 8, 9)
	w.drvOK(t, "Ravi", id, actOnMyWay, nil)
	w.setAt(18, 12, 29)
	w.Sweep(t)
	w.expectRideStatus(t, id, "on_the_way")
	w.setAt(18, 12, 30)
	w.Sweep(t)
	w.expectRideStatus(t, id, "completed")
}

// "Location arrives for a ride that is no longer on the way": 409
// not_on_the_way (before On my way and after End ride).
func TestD3_Edge_LocationNotOnTheWay(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)
	w.setAt(18, 7, 45)
	expectError(t, w.drv(t, "Ravi", id, actLocation, locBody(12.95, 77.63)), http.StatusConflict, "not_on_the_way")
	w.toCompleted(t, id)
	expectError(t, w.drv(t, "Ravi", id, actLocation, locBody(12.95, 77.63)), http.StatusConflict, "not_on_the_way")
}

// "End ride without Start ride": 409 not_started; arrived/start/end before On
// my way: 409 wrong_status.
func TestD3_Edge_OrderOfSteps(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)
	w.setAt(18, 7, 45)
	for _, a := range []string{actArrived, actStart} {
		expectErrorWithState(t, w.drv(t, "Ravi", id, a, nil), http.StatusConflict, "wrong_status")
	}
	r := w.drv(t, "Ravi", id, actEnd, nil)
	expectStatus(t, r, http.StatusConflict)
	w.expectRideStatus(t, id, "assigned")

	w.setAt(18, 8, 9)
	w.drvOK(t, "Ravi", id, actOnMyWay, nil)
	w.setAt(18, 8, 29)
	w.drvOK(t, "Ravi", id, actArrived, nil)
	expectErrorWithState(t, w.drv(t, "Ravi", id, actEnd, nil), http.StatusConflict, "not_started")
	w.expectRideStatus(t, id, "on_the_way")
	if !w.ride(t, id).null("completed_at") {
		t.Error("completed_at set by a refused End ride")
	}

	// After completion nothing moves it.
	w.setAt(18, 8, 31)
	w.drvOK(t, "Ravi", id, actStart, nil)
	w.setAt(18, 9, 22)
	w.drvOK(t, "Ravi", id, actEnd, nil)
	expectErrorWithState(t, w.drv(t, "Ravi", id, actNoShow, nil), http.StatusConflict, "wrong_status")
	expectErrorWithState(t, w.drv(t, "Ravi", id, actGiveUp, map[string]any{"reason": "other"}), http.StatusConflict, "wrong_status")
	w.expectRideStatus(t, id, "completed")
}

// "The driver app is killed while on the way": the position ages; the ride
// state is unaffected. ETA is at least 1 minute.
func TestR2_Edge_LivePositionAgesAndETAFloor(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)
	w.setAt(18, 8, 20)
	w.drvOK(t, "Ravi", id, actOnMyWay, nil)
	expectStatus(t, w.drv(t, "Ravi", id, actLocation, locBody(koramangala.Lat, koramangala.Lng)), http.StatusNoContent)

	_, m := w.live(t, "Priya", id)
	if eta := num(t, m["eta_minutes"], "eta_minutes"); eta != 1 {
		t.Errorf("eta_minutes %v at the pickup point, want 1 (at least 1 min)", eta)
	}

	w.setAt(18, 8, 22)
	r, m := w.live(t, "Priya", id)
	expectStatus(t, r, http.StatusOK)
	p := position(m)
	if p == nil {
		t.Fatalf("no position: %s", r.Body)
	}
	if age := num(t, p["age_seconds"], "age_seconds"); age < 120 {
		t.Errorf("age_seconds %v two minutes after the last update, want >= 120", age)
	}
	if st := strOf(m, "status"); st != "on_the_way" {
		t.Errorf("status %q, want on_the_way", st)
	}
}

// "I'm running late": a push to Ravi, once per ride.
func TestR2_Edge_RunningLate(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)
	priya := w.as(t, "Priya")

	// SPEC-AMBIGUITY: the button lives on Track ride after On my way; the
	// literal reading is that before it the call is 409 wrong_status.
	w.setAt(18, 7, 45)
	expectError(t, priya.Do(t, http.MethodPost, pathRideRunningLate(id), nil), http.StatusConflict, "wrong_status")

	w.setAt(18, 8, 9)
	w.drvOK(t, "Ravi", id, actOnMyWay, nil)
	w.setAt(18, 8, 20)
	expectStatus(t, priya.Do(t, http.MethodPost, pathRideRunningLate(id), nil), http.StatusNoContent)
	w.expectRideTime(t, w.ride(t, id), "late_notice_at", at(18, 8, 20))
	w.Sweep(t)
	w.expectPushes(t, "Ravi", pushR2RunningLate, 1)

	expectError(t, priya.Do(t, http.MethodPost, pathRideRunningLate(id), nil), http.StatusConflict, "already_sent")
	w.Sweep(t)
	w.expectPushes(t, "Ravi", pushR2RunningLate, 1)

	// It doesn't change the 10-minute wait (counted from 08:30 here).
	w.setAt(18, 8, 25)
	w.drvOK(t, "Ravi", id, actArrived, nil)
	w.setAt(18, 8, 40)
	w.drvOK(t, "Ravi", id, actNoShow, nil)
}

// Paying, rating and flagging need a completed ride; rating once.
func TestR2_Edge_PayRateFlagNeedCompleted(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)
	priya := w.as(t, "Priya")
	w.setAt(18, 8, 9)
	w.drvOK(t, "Ravi", id, actOnMyWay, nil)

	expectError(t, priya.Do(t, http.MethodPost, pathRideRiderPaid(id), map[string]any{"mark": "upi"}), http.StatusConflict, "not_completed")
	expectError(t, priya.Do(t, http.MethodPost, pathRideRate(id), map[string]any{"stars": 4}), http.StatusConflict, "not_completed")
	expectError(t, w.drv(t, "Ravi", id, actPayment, map[string]any{"mark": "cash"}), http.StatusConflict, "not_completed")
	expectError(t, w.drv(t, "Ravi", id, actFlag, map[string]any{"tags": []string{"kept_me_waiting"}}), http.StatusConflict, "not_completed")

	w.setAt(18, 8, 29)
	w.drvOK(t, "Ravi", id, actArrived, nil)
	w.setAt(18, 8, 31)
	w.drvOK(t, "Ravi", id, actStart, nil)
	w.setAt(18, 9, 22)
	w.drvOK(t, "Ravi", id, actEnd, nil)

	w.setAt(18, 9, 30)
	expectStatus(t, priya.Do(t, http.MethodPost, pathRideRiderPaid(id), map[string]any{"mark": "cash"}), http.StatusOK)
	expectStatus(t, priya.Do(t, http.MethodPost, pathRideRate(id),
		map[string]any{"stars": 4, "tags": []string{"on_time", "clean_vehicle"}, "note": "Thanks Ravi"}), http.StatusOK)
	expectError(t, priya.Do(t, http.MethodPost, pathRideRate(id), map[string]any{"stars": 5}), http.StatusConflict, "already_rated")
	expectStatus(t, w.drv(t, "Ravi", id, actFlag, map[string]any{"tags": []string{"kept_me_waiting"}}), http.StatusOK)

	r := w.ride(t, id)
	if r.str("rider_paid_mark") != "cash" {
		t.Errorf("rider_paid_mark %q, want cash", r.str("rider_paid_mark"))
	}
	if r.str("rider_stars") != "4" {
		t.Errorf("rider_stars %q, want 4 (the second rating must not overwrite)", r.str("rider_stars"))
	}
	if !strings.Contains(r.str("rider_tags"), "clean_vehicle") || r.str("rider_note") != "Thanks Ravi" {
		t.Errorf("rider_tags %q / rider_note %q", r.str("rider_tags"), r.str("rider_note"))
	}
	if !strings.Contains(r.str("driver_flags"), "kept_me_waiting") {
		t.Errorf("driver_flags %q", r.str("driver_flags"))
	}
}

// Go API: a rider can't call driver endpoints and the reverse; a driver can't
// act on another driver's ride; ride.live 404 for an unknown ride.
func TestR2_Edge_Roles(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)
	w.setAt(18, 8, 0)

	expectStatus(t, w.drv(t, "Priya", id, actOnMyWay, nil), http.StatusForbidden)
	r, _ := w.live(t, "Ravi", id)
	expectStatus(t, r, http.StatusForbidden)
	expectStatus(t, w.as(t, "Ravi").Do(t, http.MethodGet, pathRidesNext, nil), http.StatusForbidden)
	expectStatus(t, w.as(t, "Priya").Do(t, http.MethodGet, pathDrvRunFrom("2026-11-18", 1), nil), http.StatusForbidden)

	for _, a := range []string{actOnMyWay, actGiveUp} {
		expectStatus(t, w.drv(t, "Suresh", id, a, map[string]any{"reason": "other"}), http.StatusForbidden, http.StatusNotFound)
	}
	w.expectRideStatus(t, id, "assigned")
	if w.ride(t, id).str("driver_id") != w.uid("Ravi") {
		t.Error("another driver moved Ravi's ride")
	}

	w.drvOK(t, "Ravi", id, actOnMyWay, nil)
	expectStatus(t, w.drv(t, "Suresh", id, actLocation, locBody(12.95, 77.63)), http.StatusForbidden, http.StatusNotFound)
	expectStatus(t, w.drv(t, "Suresh", id, actArrived, nil), http.StatusForbidden, http.StatusNotFound)

	r, _ = w.live(t, "Priya", "00000000-0000-0000-0000-000000000000")
	expectStatus(t, r, http.StatusNotFound)
}

// "A ride generated while the slot has no driver": created needs_driver with
// its one-ride offer.
func TestR2_Edge_GeneratedWithoutDriver(t *testing.T) {
	w := startR2(t)
	w.exec(t, `UPDATE series_slots SET driver_id = NULL
		WHERE weekday = 5 AND series_id = (SELECT id FROM series WHERE rider_id = $1)`, w.uid("Priya"))
	w.setAt(16, 2, 0)
	w.Sweep(t)

	for _, d := range []string{"2026-11-20", "2026-11-27"} {
		id := w.rideOn(t, "Priya", d)
		r := w.expectRideStatus(t, id, "needs_driver")
		if !r.null("driver_id") {
			t.Errorf("%s: driver %s, want null", d, r.str("driver_id"))
		}
		offs := w.rideOffers(t, id)
		if len(offs) != 1 || offs[0].Kind != "ride" || offs[0].Round != 1 {
			t.Errorf("%s: offers %+v, want one ride offer, round 1", d, offs)
		}
	}
	w.expectRideStatus(t, w.rideOn(t, "Priya", "2026-11-18"), "assigned")
}

// The 20:00 reminder lists only rides still assigned to the driver.
func TestD3_Edge_ReminderSkipsGivenUpRide(t *testing.T) {
	w := startR2(t)
	w.prepare(t, 17)
	id := w.rideOn(t, "Priya", "2026-11-18")
	w.setAt(17, 18, 0)
	w.drvOK(t, "Ravi", id, actGiveUp, map[string]any{"reason": "personal"})
	w.Sweep(t)
	w.Push.Reset()

	w.setAt(17, 20, 0)
	w.Sweep(t)
	w.expectPushes(t, "Ravi", pushR2Tomorrow, 1)
	w.expectPushes(t, "Ravi", "Tomorrow: 1 ride", 1)
	w.expectPushes(t, "Ravi", "First at 09:50, Ankit", 1)
	if w.ride(t, id).str("driver_id") == w.uid("Ravi") {
		t.Error("the given-up ride is Ravi's again")
	}
}

// rides.schedule (D3.1 Today): the driver's rides in time order, max 14 days.
func TestD3_Edge_DriverSchedule(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)
	ankit := w.rideOn(t, "Ankit", "2026-11-18")
	w.setAt(18, 7, 0)

	r := w.as(t, "Ravi").Do(t, http.MethodGet, pathDrvRunFrom("2026-11-18", 1), nil)
	expectStatus(t, r, http.StatusOK)
	list, _ := r.Map(t)["rides"].([]any)
	var ids []string
	for _, x := range list {
		if m, ok := x.(map[string]any); ok {
			ids = append(ids, strOf(m, "id"))
			if strOf(m, "status") == "" {
				t.Errorf("ride without status: %v", m)
			}
		}
	}
	if strings.Join(ids, ",") != id+","+ankit {
		t.Errorf("Wednesday's run %v, want [Priya 08:30, Ankit 09:50]", ids)
	}
	if !strings.Contains(string(r.Body), "Priya") || !strings.Contains(string(r.Body), "Koramangala") {
		t.Errorf("run lacks rider name or route: %s", r.Body)
	}

	r = w.as(t, "Ravi").Do(t, http.MethodGet, pathDrvRunFrom("2026-11-18", 14), nil)
	expectStatus(t, r, http.StatusOK)
	v := anyJSON(t, r)
	for _, d := range []string{"2026-11-30", "2026-12-01"} {
		if findObj(v, w.rideOn(t, "Ankit", d)) == nil {
			t.Errorf("14-day run lacks Ankit's ride on %s", d)
		}
	}
	if findObj(v, w.rideOn(t, "Priya", "2026-12-02")) != nil {
		t.Error("14-day run from 18 Nov includes 2 Dec")
	}
}

// rides.next (R2.1): the next ride with its driver and state.
func TestR2_Edge_RidesNext(t *testing.T) {
	w := startR2(t)
	w.prepare(t, 17)
	id := w.rideOn(t, "Priya", "2026-11-18")

	w.setAt(17, 12, 0)
	r := w.as(t, "Priya").Do(t, http.MethodGet, pathRidesNext, nil)
	expectStatus(t, r, http.StatusOK)
	m := findObj(anyJSON(t, r), id)
	if m == nil {
		t.Fatalf("rides.next lacks Wednesday's ride: %s", r.Body)
	}
	if st := strOf(m, "status"); st != "assigned" {
		t.Errorf("next ride status %q, want assigned", st)
	}
	drv, _ := m["driver"].(map[string]any)
	if !strings.Contains(strOf(drv, "name"), "Ravi") {
		t.Errorf("next ride driver %v, want Ravi", m["driver"])
	}

	// After Ravi gives it up it shows needs_driver ("Finding another driver").
	w.setAt(17, 18, 0)
	w.drvOK(t, "Ravi", id, actGiveUp, map[string]any{"reason": "vehicle"})
	r = w.as(t, "Priya").Do(t, http.MethodGet, pathRidesNext, nil)
	expectStatus(t, r, http.StatusOK)
	if m := findObj(anyJSON(t, r), id); m == nil || strOf(m, "status") != "needs_driver" {
		t.Errorf("after give-up rides.next shows %v, want the ride needs_driver", m)
	}

	// Thursday: the next ride is Friday's.
	w.Clock.Set(at(19, 10, 0))
	r = w.as(t, "Priya").Do(t, http.MethodGet, pathRidesNext, nil)
	expectStatus(t, r, http.StatusOK)
	if findObj(anyJSON(t, r), w.rideOn(t, "Priya", "2026-11-20")) == nil {
		t.Errorf("on Thursday rides.next lacks Friday's ride: %s", r.Body)
	}
}
