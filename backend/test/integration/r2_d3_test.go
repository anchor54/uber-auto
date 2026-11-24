package integration

// R2+D3 "The Daily Ride": one test per scenario of the spec's Integration
// tests table (R2-I1 .. R2-I17). Contract and helpers: r2_d3_helpers_test.go.

import (
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"commute-backend/internal/testkit"
)

// R2-I1: Clock Mon 16 Nov 02:00; RunOnce twice -> rides for every Mon/Wed/Fri
// up to 30 Nov, all assigned to Ravi with pickup_at 03:00 UTC; the second run
// inserts nothing.
func TestR2_I1_GenerationFills14DaysOnce(t *testing.T) {
	w := startR2(t)
	w.setAt(16, 2, 0)
	w.Sweep(t)
	total := w.count(t, `SELECT count(*) FROM rides`)

	var seriesID string
	w.one(t, `SELECT id::text FROM series WHERE rider_id = $1`, []any{w.uid("Priya")}, &seriesID)
	rides := w.rides(t, seriesID)
	wantDates := []string{"2026-11-16", "2026-11-18", "2026-11-20", "2026-11-23", "2026-11-25", "2026-11-27", "2026-11-30"}

	t.Run("every Mon/Wed/Fri up to 30 Nov", func(t *testing.T) {
		var got []string
		for _, r := range rides {
			got = append(got, r.LocalDate)
		}
		if strings.Join(got, ",") != strings.Join(wantDates, ",") {
			t.Errorf("Priya's ride dates %v, want %v", got, wantDates)
		}
	})
	t.Run("assigned to Ravi at 03:00 UTC", func(t *testing.T) {
		for _, r := range rides {
			if r.Status != "assigned" {
				t.Errorf("%s: status %s, want assigned", r.LocalDate, r.Status)
			}
			if r.DriverID != w.uid("Ravi") {
				t.Errorf("%s: driver %s, want Ravi", r.LocalDate, r.DriverID)
			}
			p := r.PickupAt.UTC()
			if p.Hour() != 3 || p.Minute() != 0 || p.Format("2006-01-02") != r.LocalDate {
				t.Errorf("%s: pickup_at %s, want 03:00 UTC that day", r.LocalDate, p)
			}
			// busy_until = pickup + 51 min trip + 10 min.
			if want := r.PickupAt.Add(61 * time.Minute); !r.BusyUntil.Equal(want) {
				t.Errorf("%s: busy_until %s, want %s", r.LocalDate, r.BusyUntil.In(ist), want.In(ist))
			}
		}
	})
	t.Run("fare snapshot is the series fare", func(t *testing.T) {
		n := w.count(t, `SELECT count(*) FROM rides r JOIN series s ON s.id = r.series_id
			WHERE s.id = $1 AND r.fare_snapshot <> s.suggested_fare`, seriesID)
		if n != 0 {
			t.Errorf("%d rides with a fare_snapshot other than the series fare", n)
		}
	})
	t.Run("Ankit's weekday rides too", func(t *testing.T) {
		// Mon 16 .. Mon 30 Nov: 11 weekdays.
		if n := w.count(t, `SELECT count(*) FROM rides WHERE rider_id = $1 AND status = 'assigned'
			AND local_date BETWEEN '2026-11-16' AND '2026-11-30'`, w.uid("Ankit")); n != 11 {
			t.Errorf("Ankit has %d rides 16-30 Nov, want 11", n)
		}
		if n := w.count(t, `SELECT count(*) FROM rides WHERE local_date > '2026-11-30'`); n != 0 {
			t.Errorf("%d rides after today + 14 (30 Nov)", n)
		}
	})
	t.Run("second run inserts nothing", func(t *testing.T) {
		w.Sweep(t)
		if n := w.count(t, `SELECT count(*) FROM rides`); n != total {
			t.Errorf("rides after the second run: %d, want %d", n, total)
		}
	})
}

// R2-I2: Clock Tue 17 Nov 20:00; RunOnce twice -> one push to Ravi listing
// Wednesday's 08:30 and 09:50 rides; reminder_sent_at set on both; the second
// run sends nothing.
func TestR2_I2_EveningReminderOnce(t *testing.T) {
	w := startR2(t)
	w.prepare(t, 17)
	priya := w.rideOn(t, "Priya", "2026-11-18")
	ankit := w.rideOn(t, "Ankit", "2026-11-18")
	if !w.ride(t, priya).null("reminder_sent_at") {
		t.Fatal("reminder_sent_at set before 20:00 the day before")
	}

	w.setAt(17, 20, 0)
	w.Sweep(t)

	t.Run("one push to Ravi", func(t *testing.T) {
		w.expectPushes(t, "Ravi", pushR2Tomorrow, 1)
		w.expectPushes(t, "Ravi", "Tomorrow: 2 rides", 1)
		w.expectPushes(t, "Ravi", "First at 08:30, Priya, Koramangala → Whitefield", 1)
	})
	t.Run("push carries both ride ids", func(t *testing.T) {
		// SPEC-AMBIGUITY: "Both carry the ride ids" - any data value may hold
		// them (e.g. "ride_ids": "id1,id2").
		for _, m := range w.Push.To("Ravi") {
			if !strings.Contains(m.Title+m.Body, "Tomorrow") {
				continue
			}
			var all string
			for _, v := range m.Data {
				all += v + " "
			}
			for _, id := range []string{priya, ankit} {
				if !strings.Contains(all, id) {
					t.Errorf("reminder data %v does not reference ride %s", m.Data, id)
				}
			}
		}
	})
	t.Run("markers set on both", func(t *testing.T) {
		for _, id := range []string{priya, ankit} {
			w.expectRideTime(t, w.ride(t, id), "reminder_sent_at", at(17, 20, 0))
		}
	})
	t.Run("not for later days", func(t *testing.T) {
		if id := w.rideOn(t, "Priya", "2026-11-20"); !w.ride(t, id).null("reminder_sent_at") {
			t.Error("Friday's ride got a reminder marker on Tuesday")
		}
	})
	t.Run("riders get nothing", func(t *testing.T) {
		w.expectPushes(t, "Priya", "", 0)
	})
	t.Run("second run sends nothing", func(t *testing.T) {
		w.Push.Reset()
		w.Advance(t, time.Minute)
		w.Sweep(t)
		w.expectPushes(t, "Ravi", "", 0)
	})
}

// R2-I3: first RunOnce at Tue 23:30 -> no push (past 23:00), markers set.
func TestR2_I3_ReminderAfterOutage(t *testing.T) {
	w := startR2(t)
	w.prepare(t, 17)
	priya := w.rideOn(t, "Priya", "2026-11-18")
	ankit := w.rideOn(t, "Ankit", "2026-11-18")

	w.setAt(17, 23, 30)
	w.Sweep(t)

	w.expectPushes(t, "Ravi", pushR2Tomorrow, 0)
	for _, id := range []string{priya, ankit} {
		if w.ride(t, id).null("reminder_sent_at") {
			t.Errorf("ride %s: reminder_sent_at not set after the too-late run", id)
		}
	}
	w.Sweep(t)
	w.expectPushes(t, "Ravi", pushR2Tomorrow, 0)
}

// R2-I4: the full ride. Wed 08:09 on_my_way; three location calls; 08:29
// arrived; 08:31 start; 09:22 complete; Ravi driver_paid upi; Priya
// rider_paid upi, rate 5.
func TestR2_I4_FullRideHappyPath(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)
	w.expectRideStatus(t, id, "assigned")
	before := w.eventsFor(t, id)

	// 08:09 On my way.
	w.setAt(18, 8, 9)
	got := w.drvOK(t, "Ravi", id, actOnMyWay, nil)
	if st := strOf(got, "status"); st != "on_the_way" {
		t.Errorf("on_my_way returned status %q, want on_the_way", st)
	}
	if pin, _ := got["pin"].(string); pin != "" {
		t.Errorf("driver's on_my_way response shows a PIN %q; it must be hidden", pin)
	}
	w.Sweep(t)
	r := w.expectRideStatus(t, id, "on_the_way")
	w.expectRideTime(t, r, "on_the_way_at", at(18, 8, 9))

	// Three location calls, 10 s apart, the last 0.5 km north of the pickup.
	pts := []place{
		{Lat: 12.9500, Lng: 77.6300},
		{Lat: 12.9450, Lng: 77.6260},
		{Lat: 12.9397, Lng: 77.6245},
	}
	for _, p := range pts {
		w.Advance(t, 10*time.Second)
		lr := w.drv(t, "Ravi", id, actLocation, locBody(p.Lat, p.Lng))
		expectStatus(t, lr, http.StatusNoContent)
	}
	last := pts[len(pts)-1]

	t.Run("ride.live: latest position and ETA", func(t *testing.T) {
		lr, m := w.live(t, "Priya", id)
		expectStatus(t, lr, http.StatusOK)
		if st := strOf(m, "status"); st != "on_the_way" {
			t.Errorf("live status %q, want on_the_way", st)
		}
		p := position(m)
		if p == nil {
			t.Fatalf("live has no position: %s", lr.Body)
		}
		if lat, lng := num(t, p["lat"], "lat"), num(t, p["lng"], "lng"); math.Abs(lat-last.Lat) > 1e-6 || math.Abs(lng-last.Lng) > 1e-6 {
			t.Errorf("position (%v, %v), want the latest (%v, %v)", lat, lng, last.Lat, last.Lng)
		}
		if _, ok := p["age_seconds"]; !ok {
			t.Errorf("position has no age_seconds: %v", p)
		}
		eta := num(t, m["eta_minutes"], "eta_minutes")
		// ETA = straight line x 1.4 at 18 km/h, at least 1 min.
		exact := roadKm(last, koramangala) / 18 * 60
		if eta < 1 || eta < math.Floor(exact) || eta > math.Ceil(exact) {
			t.Errorf("eta_minutes %v, want %.2f rounded (and >= 1)", eta, exact)
		}
	})
	t.Run("driver_locations row", func(t *testing.T) {
		var lat, lng float64
		w.one(t, `SELECT lat, lng FROM driver_locations WHERE driver_id = $1`, []any{w.uid("Ravi")}, &lat, &lng)
		if math.Abs(lat-last.Lat) > 1e-6 || math.Abs(lng-last.Lng) > 1e-6 {
			t.Errorf("driver_locations (%v, %v), want (%v, %v)", lat, lng, last.Lat, last.Lng)
		}
		if n := w.count(t, `SELECT count(*) FROM driver_locations WHERE driver_id = $1`, w.uid("Ravi")); n != 1 {
			t.Errorf("%d driver_locations rows for Ravi, want 1 (overwritten)", n)
		}
	})

	// 08:29 arrived.
	w.setAt(18, 8, 29)
	w.drvOK(t, "Ravi", id, actArrived, nil)
	w.Sweep(t)
	// 08:31 start. HLD: no PIN step - start needs no PIN.
	w.setAt(18, 8, 31)
	got = w.drvOK(t, "Ravi", id, actStart, nil)
	if v, ok := got["picked_up_at"]; ok && v == nil {
		t.Errorf("start returned picked_up_at null")
	}
	// 09:22 complete.
	w.setAt(18, 9, 22)
	got = w.drvOK(t, "Ravi", id, actEnd, nil)
	if st := strOf(got, "status"); st != "completed" {
		t.Errorf("end returned status %q, want completed", st)
	}
	w.Sweep(t)

	// Payment and rating.
	w.setAt(18, 9, 25)
	w.drvOK(t, "Ravi", id, actPayment, map[string]any{"mark": "upi"})
	pr := w.as(t, "Priya").Do(t, http.MethodPost, pathRideRiderPaid(id), map[string]any{"mark": "upi"})
	expectStatus(t, pr, http.StatusOK)
	rr := w.as(t, "Priya").Do(t, http.MethodPost, pathRideRate(id), map[string]any{"stars": 5, "tags": []string{"on_time"}, "note": ""})
	expectStatus(t, rr, http.StatusOK)

	r = w.expectRideStatus(t, id, "completed")
	t.Run("timestamps", func(t *testing.T) {
		w.expectRideTime(t, r, "on_the_way_at", at(18, 8, 9))
		w.expectRideTime(t, r, "arrived_at", at(18, 8, 29))
		w.expectRideTime(t, r, "picked_up_at", at(18, 8, 31))
		w.expectRideTime(t, r, "completed_at", at(18, 9, 22))
	})
	t.Run("marks and rating", func(t *testing.T) {
		if got := r.str("driver_paid_mark"); got != "upi" {
			t.Errorf("driver_paid_mark %q, want upi", got)
		}
		if got := r.str("rider_paid_mark"); got != "upi" {
			t.Errorf("rider_paid_mark %q, want upi", got)
		}
		if got := r.str("rider_stars"); got != "5" {
			t.Errorf("rider_stars %q, want 5", got)
		}
	})
	t.Run("Priya's pushes", func(t *testing.T) {
		w.expectPushes(t, "Priya", pushR2OnTheWay, 1)
		w.expectPushes(t, "Priya", pushR2Arrived, 1)
		w.expectPushes(t, "Priya", pushR2Completed, 1)
		w.expectSomePush(t, "Priya", "Pay Ravi "+w.fareText(t, id))
		// Order: on the way, arrived, completed.
		var order []string
		for _, s := range w.pushTextsTo("Priya") {
			for _, k := range []string{pushR2OnTheWay, pushR2Arrived, pushR2Completed} {
				if strings.Contains(s, k) {
					order = append(order, k)
				}
			}
		}
		if strings.Join(order, "|") != strings.Join([]string{pushR2OnTheWay, pushR2Arrived, pushR2Completed}, "|") {
			t.Errorf("Priya's ride pushes in order %q", order)
		}
	})
	t.Run("events written", func(t *testing.T) {
		// on my way, arrived, start, complete (+ marks/rating): at least 4.
		if n := w.eventsFor(t, id) - before; n < 4 {
			t.Errorf("%d events for the ride's changes, want >= 4", n)
		}
	})
	t.Run("other rides untouched", func(t *testing.T) {
		w.expectRideStatus(t, w.rideOn(t, "Ankit", "2026-11-18"), "assigned")
		w.expectRideStatus(t, w.rideOn(t, "Priya", "2026-11-20"), "assigned")
	})
}

// R2-I5: 07:20 on_my_way -> 409 too_early.
func TestR2_I5_OnMyWayTooEarly(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)
	w.setAt(18, 7, 20)
	r := w.drv(t, "Ravi", id, actOnMyWay, nil)
	expectErrorWithState(t, r, http.StatusConflict, "too_early")
	w.expectRideStatus(t, id, "assigned")
}

// R2-I6: wrong PIN.
// HLD: no PIN step for now. The spec's "start with 0000 -> 422 wrong_pin; the
// right PIN then works" becomes: start without any PIN succeeds and sets
// picked_up_at; it is never refused with wrong_pin.
func TestR2_I6_StartWithoutPIN(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)
	w.setAt(18, 8, 9)
	w.drvOK(t, "Ravi", id, actOnMyWay, nil)
	w.setAt(18, 8, 29)
	w.drvOK(t, "Ravi", id, actArrived, nil)
	if !w.ride(t, id).null("picked_up_at") {
		t.Fatal("picked_up_at set before start")
	}
	w.setAt(18, 8, 31)
	r := w.drv(t, "Ravi", id, actStart, map[string]any{})
	if r.Status == http.StatusUnprocessableEntity {
		t.Fatalf("start refused (HLD: no PIN step): %s", r.Body)
	}
	expectStatus(t, r, http.StatusOK)
	rr := w.expectRideStatus(t, id, "on_the_way")
	w.expectRideTime(t, rr, "picked_up_at", at(18, 8, 31))

	// Starting twice is not allowed to move the pickup time.
	w.setAt(18, 8, 32)
	r = w.drv(t, "Ravi", id, actStart, map[string]any{})
	if r.Status == http.StatusOK {
		w.expectRideTime(t, w.ride(t, id), "picked_up_at", at(18, 8, 31))
	} else {
		expectError(t, r, http.StatusConflict, "wrong_status")
	}
}

// R2-I7: arrived at 08:33; no_show at 08:42 -> 409 too_soon with 08:43; at
// 08:43 it succeeds: rider_no_show, push to Priya; her Friday ride still
// assigned.
func TestR2_I7_NoShowTiming(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)
	w.setAt(18, 8, 9)
	w.drvOK(t, "Ravi", id, actOnMyWay, nil)
	w.setAt(18, 8, 33)
	w.drvOK(t, "Ravi", id, actArrived, nil)

	w.setAt(18, 8, 42)
	r := w.drv(t, "Ravi", id, actNoShow, nil)
	m := expectErrorWithState(t, r, http.StatusConflict, "too_soon")
	expectOpensAt(t, m, at(18, 8, 43))
	w.expectRideStatus(t, id, "on_the_way")

	w.setAt(18, 8, 43)
	got := w.drvOK(t, "Ravi", id, actNoShow, nil)
	if st := strOf(got, "status"); st != "rider_no_show" {
		t.Errorf("no_show returned status %q, want rider_no_show", st)
	}
	w.expectRideStatus(t, id, "rider_no_show")
	w.Sweep(t)
	w.expectPushes(t, "Priya", pushR2NoShow, 1)

	fri := w.rideOn(t, "Priya", "2026-11-20")
	if r := w.expectRideStatus(t, fri, "assigned"); r.str("driver_id") != w.uid("Ravi") {
		t.Errorf("Friday's ride driver %s, want Ravi", r.str("driver_id"))
	}
	w.expectRideStatus(t, w.rideOn(t, "Ankit", "2026-11-18"), "assigned")
}

// R2-I8: arrived at 08:20 (before pickup); no_show at 08:35 -> 409; 08:40 -> 200.
func TestR2_I8_NoShowCountedFromPickup(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)
	w.setAt(18, 8, 9)
	w.drvOK(t, "Ravi", id, actOnMyWay, nil)
	w.setAt(18, 8, 20)
	w.drvOK(t, "Ravi", id, actArrived, nil)

	w.setAt(18, 8, 35)
	m := expectErrorWithState(t, w.drv(t, "Ravi", id, actNoShow, nil), http.StatusConflict, "too_soon")
	expectOpensAt(t, m, at(18, 8, 40))

	w.setAt(18, 8, 40)
	w.drvOK(t, "Ravi", id, actNoShow, nil)
	w.expectRideStatus(t, id, "rider_no_show")
}

// R2-I9: no on_my_way; clock 08:15; RunOnce -> ride needs_driver, driver_id
// null; one-ride offer round 1 expiring 08:20 to Suresh, Anil, Meena (not
// Ravi); Priya's "hasn't started yet" push.
func TestR2_I9_OnMyWayCheckHandsOver(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)
	w.setAt(18, 8, 15)
	w.Sweep(t)

	r := w.expectRideStatus(t, id, "needs_driver")
	if !r.null("driver_id") {
		t.Errorf("driver_id %s, want null", r.str("driver_id"))
	}
	offs := w.rideOffers(t, id)
	if len(offs) != 1 {
		t.Fatalf("%d offers for the ride, want 1: %+v", len(offs), offs)
	}
	o := offs[0]
	if o.Kind != "ride" || o.Round != 1 || o.Status != "open" {
		t.Errorf("offer %+v, want kind ride, round 1, open", o)
	}
	if !o.ExpiresAt.Equal(at(18, 8, 20)) {
		t.Errorf("offer expires_at %s, want 08:20", o.ExpiresAt.In(ist))
	}
	w.expectRecipients(t, o.ID, "Suresh", "Anil", "Meena")
	w.expectSomePush(t, "Priya", pushR2NotStarted)
	w.expectPushes(t, "Priya", pushR2NotStarted, 1)

	t.Run("only this ride moves", func(t *testing.T) {
		fri := w.ride(t, w.rideOn(t, "Priya", "2026-11-20"))
		if fri.str("status") != "assigned" || fri.str("driver_id") != w.uid("Ravi") {
			t.Errorf("Friday's ride %s / %s, want assigned to Ravi", fri.str("status"), fri.str("driver_id"))
		}
	})
	t.Run("a second sweep opens nothing more", func(t *testing.T) {
		w.Advance(t, time.Minute)
		w.Sweep(t)
		if n := len(w.rideOffers(t, id)); n != 1 {
			t.Errorf("%d offers after a second sweep, want 1", n)
		}
		w.expectPushes(t, "Priya", pushR2NotStarted, 1)
	})
}

// R2-I10: at 08:15 run on_my_way and RunOnce in two goroutines, 50 times with
// fresh data -> each run ends in exactly one of: on_the_way with no offer, or
// needs_driver with an offer and Ravi's call 409 ride_reassigned.
func TestR2_I10_RaceOnMyWayAgainstCheck(t *testing.T) {
	runs := 50
	if testing.Short() {
		runs = 10
	}
	var mu sync.Mutex
	outcomes := map[string]int{}
	for i := 0; i < runs; i++ {
		t.Run(fmt.Sprintf("run%02d", i), func(t *testing.T) {
			w := startR2(t)
			id := w.r102(t)
			ravi := w.as(t, "Ravi")
			w.setAt(18, 8, 15)

			var resp *testkit.Resp
			var wg sync.WaitGroup
			gate := make(chan struct{})
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-gate
				resp = ravi.Do(t, http.MethodPost, pathDrvRide(id, actOnMyWay), map[string]any{})
			}()
			go func() {
				defer wg.Done()
				<-gate
				w.Sweep(t)
			}()
			close(gate)
			wg.Wait()

			r := w.ride(t, id)
			offs := w.rideOffers(t, id)
			switch r.str("status") {
			case "on_the_way":
				expectStatus(t, resp, http.StatusOK)
				if len(offs) != 0 {
					t.Errorf("on_the_way and %d offers", len(offs))
				}
				if r.str("driver_id") != w.uid("Ravi") {
					t.Errorf("on_the_way with driver %s", r.str("driver_id"))
				}
				mu.Lock()
				outcomes["on_the_way"]++
				mu.Unlock()
			case "needs_driver":
				expectErrorWithState(t, resp, http.StatusConflict, "ride_reassigned")
				if len(offs) != 1 {
					t.Errorf("needs_driver and %d offers, want 1", len(offs))
				}
				if !r.null("driver_id") {
					t.Errorf("needs_driver with driver %s", r.str("driver_id"))
				}
				mu.Lock()
				outcomes["needs_driver"]++
				mu.Unlock()
			default:
				t.Errorf("ride ended %q (Ravi's call %d %s)", r.str("status"), resp.Status, resp.Body)
			}
		})
	}
	t.Logf("outcomes: %v", outcomes)
}

// R2-I11: no calls until 08:45; RunOnce -> ride failed, fail_reason
// missed_by_outage; no offer; admin push.
func TestR2_I11_Outage(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)
	w.setAt(18, 8, 45)
	w.Sweep(t)

	r := w.expectRideStatus(t, id, "failed")
	if got := r.str("fail_reason"); got != "missed_by_outage" {
		t.Errorf("fail_reason %q, want missed_by_outage", got)
	}
	if n := len(w.rideOffers(t, id)); n != 0 {
		t.Errorf("%d offers after an outage failure, want none", n)
	}
	w.expectSomePush(t, "Admin", pushR2Outage)
	for _, d := range []string{"Suresh", "Anil", "Meena"} {
		w.expectPushes(t, d, "", 0)
	}
	// Ankit's 09:50 ride isn't due yet.
	w.expectRideStatus(t, w.rideOn(t, "Ankit", "2026-11-18"), "assigned")

	t.Run("alert once", func(t *testing.T) {
		w.Advance(t, time.Minute)
		w.Sweep(t)
		w.expectPushes(t, "Admin", pushR2Outage, 1)
	})
}

// R2-I12: Tue 18:00 Ravi gives up Wednesday's ride, reason "vehicle" -> ride
// needs_driver; offer to the three others; Priya's push; Friday's and next
// Wednesday's rides still Ravi's; give_up again -> 409.
func TestR2_I12_GiveUpARide(t *testing.T) {
	w := startR2(t)
	w.prepare(t, 17)
	id := w.rideOn(t, "Priya", "2026-11-18")
	before := w.eventsFor(t, id)

	w.setAt(17, 18, 0)
	got := w.drvOK(t, "Ravi", id, actGiveUp, map[string]any{"reason": "vehicle"})
	if st := strOf(got, "status"); st != "needs_driver" {
		t.Errorf("give_up returned status %q, want needs_driver", st)
	}
	w.Sweep(t)

	r := w.expectRideStatus(t, id, "needs_driver")
	if !r.null("driver_id") {
		t.Errorf("driver_id %s, want null", r.str("driver_id"))
	}
	if got := r.str("give_up_reason"); got != "vehicle" {
		t.Errorf("give_up_reason %q, want vehicle", got)
	}
	offs := w.rideOffers(t, id)
	if len(offs) != 1 {
		t.Fatalf("%d offers, want 1", len(offs))
	}
	if o := offs[0]; o.Kind != "ride" || o.Round != 1 || !o.ExpiresAt.Equal(at(17, 18, 5)) {
		t.Errorf("offer %+v, want kind ride, round 1, expiring 18:05", o)
	}
	w.expectRecipients(t, offs[0].ID, "Suresh", "Anil", "Meena")
	for _, d := range []string{"Suresh", "Anil", "Meena"} {
		if len(w.Push.To(d)) == 0 {
			t.Errorf("%s got no offer push", d)
		}
	}
	w.expectPushes(t, "Priya", pushR2GaveUp, 1)
	w.expectSomePush(t, "Priya", "Ravi "+pushR2GaveUp)
	if n := w.eventsFor(t, id) - before; n < 1 {
		t.Error("no events row for the give-up")
	}

	t.Run("later rides stay with Ravi", func(t *testing.T) {
		for _, d := range []string{"2026-11-20", "2026-11-25"} {
			x := w.ride(t, w.rideOn(t, "Priya", d))
			if x.str("status") != "assigned" || x.str("driver_id") != w.uid("Ravi") {
				t.Errorf("%s: %s / %s, want assigned to Ravi", d, x.str("status"), x.str("driver_id"))
			}
		}
		w.expectRideStatus(t, w.rideOn(t, "Ankit", "2026-11-18"), "assigned")
	})
	t.Run("give up again", func(t *testing.T) {
		r := w.drv(t, "Ravi", id, actGiveUp, map[string]any{"reason": "vehicle"})
		expectStatus(t, r, http.StatusConflict)
	})
}

// R2-I13: Wed 08:35 give_up -> 409 too_late.
func TestR2_I13_GiveUpAfterPickupTime(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)
	w.setAt(18, 8, 35)
	r := w.drv(t, "Ravi", id, actGiveUp, map[string]any{"reason": "personal"})
	expectErrorWithState(t, r, http.StatusConflict, "too_late")
	if r := w.ride(t, id); r.str("driver_id") != w.uid("Ravi") {
		t.Errorf("driver_id %s after a refused give-up, want Ravi", r.str("driver_id"))
	}
	if n := len(w.rideOffers(t, id)); n != 0 {
		t.Errorf("%d offers after a refused give-up", n)
	}
}

// R2-I14: on_my_way at 08:09, nothing else; clock 12:31; RunOnce ->
// completed, event reason auto_closed, admin push.
func TestR2_I14_StaleRideClosed(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)
	w.setAt(18, 8, 9)
	w.drvOK(t, "Ravi", id, actOnMyWay, nil)

	w.setAt(18, 12, 31)
	w.Sweep(t)

	r := w.expectRideStatus(t, id, "completed")
	if r.null("completed_at") {
		t.Error("completed_at not set by the stale close")
	}
	if n := w.count(t, `SELECT count(*) FROM events WHERE entity_id = $1
		AND (type ILIKE '%auto_closed%' OR data::text ILIKE '%auto_closed%')`, id); n != 1 {
		t.Errorf("%d auto_closed events for the ride, want 1", n)
	}
	w.expectSomePush(t, "Admin", pushR2AutoClosed)
	w.expectPushes(t, "Admin", pushR2AutoClosed, 1)

	w.Advance(t, time.Minute)
	w.Sweep(t)
	w.expectPushes(t, "Admin", pushR2AutoClosed, 1)
}

// R2-I15: another rider calls ride.live for R-102 -> 403; Priya calls it
// before On my way -> a response with no position.
func TestR2_I15_LocationPrivacy(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)
	w.setAt(18, 7, 45)

	r, _ := w.live(t, "Ankit", id)
	expectStatus(t, r, http.StatusForbidden)

	r, m := w.live(t, "Priya", id)
	expectStatus(t, r, http.StatusOK)
	if p := position(m); p != nil {
		t.Errorf("position before On my way: %v", p)
	}
	if v, ok := m["eta_minutes"]; ok && v != nil {
		t.Errorf("eta_minutes %v before On my way, want null", v)
	}
	if st := strOf(m, "status"); st != "assigned" {
		t.Errorf("live status %q, want assigned", st)
	}

	t.Run("other rider still refused while on the way", func(t *testing.T) {
		w.setAt(18, 8, 9)
		w.drvOK(t, "Ravi", id, actOnMyWay, nil)
		expectStatus(t, w.drv(t, "Ravi", id, actLocation, locBody(12.94, 77.63)), http.StatusNoContent)
		r, _ := w.live(t, "Ankit", id)
		expectStatus(t, r, http.StatusForbidden)
		if strings.Contains(string(r.Body), "77.63") {
			t.Errorf("403 body leaks the position: %s", r.Body)
		}
	})
}

// R2-I16: insert directly a second assigned ride for Ravi at Wed 08:45 -> the
// database refuses it (exclusion violation).
func TestR2_I16_OverlapBackstop(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)

	// Another slot of S-17 (Monday's), so unique (slot_id, local_date) is not
	// what refuses it.
	var otherSlot string
	w.one(t, `SELECT s.id::text FROM series_slots s JOIN rides r ON r.series_id = s.series_id
		WHERE r.id = $1 AND s.id <> r.slot_id ORDER BY s.weekday LIMIT 1`, []any{id}, &otherSlot)

	rows, err := w.DB.Query(ctx(), `SELECT column_name FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'rides' ORDER BY ordinal_position`)
	if err != nil {
		t.Fatal(err)
	}
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, c)
	}
	rows.Close()

	insert := func(status string) error {
		var sel, names []string
		for _, c := range cols {
			names = append(names, c)
			switch c {
			case "id":
				sel = append(sel, "gen_random_uuid()")
			case "slot_id":
				sel = append(sel, "$2::uuid")
			case "pickup_at":
				sel = append(sel, "pickup_at + interval '15 minutes'")
			case "drop_at":
				sel = append(sel, "drop_at + interval '15 minutes'")
			case "busy_until":
				sel = append(sel, "busy_until + interval '15 minutes'")
			case "status":
				sel = append(sel, "$3")
			default:
				sel = append(sel, c)
			}
		}
		_, err := w.DB.Exec(ctx(), `INSERT INTO rides (`+strings.Join(names, ", ")+`) SELECT `+
			strings.Join(sel, ", ")+` FROM rides WHERE id = $1`, id, otherSlot, status)
		return err
	}

	err = insert("assigned")
	if err == nil {
		t.Fatal("a second assigned ride for Ravi at 08:45 was accepted")
	}
	if code := pgCode(err); code != "23P01" {
		t.Errorf("error %v (SQLSTATE %s), want an exclusion violation 23P01", err, code)
	}
	// Sanity: only the overlap refuses it.
	if err := insert("cancelled"); err != nil {
		t.Errorf("the same row as cancelled was refused: %v", err)
	}
}

// R2-I17: Ravi marks not_paid, then cash before midnight, then upi at 00:05 ->
// the first two succeed; the third 409 too_late; Earnings totals use cash.
func TestR2_I17_PaymentMarks(t *testing.T) {
	w := startR2(t)
	id := w.r102(t)
	w.toCompleted(t, id)

	w.setAt(18, 9, 25)
	w.drvOK(t, "Ravi", id, actPayment, map[string]any{"mark": "not_paid"})
	if got := w.ride(t, id).str("driver_paid_mark"); got != "not_paid" {
		t.Errorf("driver_paid_mark %q, want not_paid", got)
	}
	w.setAt(18, 23, 50)
	w.drvOK(t, "Ravi", id, actPayment, map[string]any{"mark": "cash"})
	w.setAt(19, 0, 5)
	r := w.drv(t, "Ravi", id, actPayment, map[string]any{"mark": "upi"})
	expectErrorWithState(t, r, http.StatusConflict, "too_late")

	// SPEC-AMBIGUITY: "Earnings totals use cash" - there is no Earnings
	// endpoint in this doc; the stored mark is what Earnings reads.
	if got := w.ride(t, id).str("driver_paid_mark"); got != "cash" {
		t.Errorf("driver_paid_mark %q, want cash", got)
	}
}
