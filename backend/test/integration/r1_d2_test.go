package integration

// R1+D2 "Create a Series and Get a Driver": the spec's Integration tests
// R1-I1 .. R1-I8. I9 .. I14 are in r1_d2_concurrency_test.go and
// r1_d2_rounds_test.go; edge cases, API errors and D2 driver endpoints in
// r1_d2_edge_test.go.
//
// Seed ("spec"): rider Priya; live, paid auto drivers Ravi (drives Ankit
// weekdays 09:50 from Whitefield), Meena (a Wednesday 09:00 ride that clashes
// with Priya), Anil and Suresh; a bike driver. Clock Thu 19 Nov 2026 10:00 IST.

import (
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"commute-backend/internal/testkit"
)

// RULING (orchestrator): rides are created for 14 days counted from the start
// date (Fri 20 Nov) to Thu 3 Dec inclusive: Mon/Wed/Fri in that range.
var priyaRideDates = []string{
	istDate(2026, 11, 20), istDate(2026, 11, 23), istDate(2026, 11, 25),
	istDate(2026, 11, 27), istDate(2026, 11, 30), istDate(2026, 12, 2),
}

// R1-I1 Preview: Koramangala -> Whitefield, auto, Mon/Wed/Fri 08:30 ->
// travel 51 min, start date Fri 20 Nov (RULING), suggested fare from the fare rules.
func TestR1_I1_Preview(t *testing.T) {
	w := start(t)
	r := w.as(t, "Priya").Do(t, http.MethodPost, pathPreview, priyaReq())
	expectStatus(t, r, http.StatusOK)
	body := anyJSON(t, r)

	t.Run("travel minutes", func(t *testing.T) {
		v, ok := findKey(body, "travel_minutes")
		if !ok {
			t.Fatalf("no travel_minutes in %s", r.Body)
		}
		if got := num(t, v, "travel_minutes"); got != 51 {
			t.Errorf("travel_minutes = %v, want 51", got)
		}
	})

	t.Run("start date", func(t *testing.T) {
		// RULING (orchestrator): the start date is the first chosen weekday
		// from tomorrow on: Thu 19 Nov with Mon/Wed/Fri -> Fri 20 Nov (the
		// spec's R1-I1 row said Mon 23 Nov).
		v, ok := findKey(body, "start_date")
		if !ok {
			t.Fatalf("no start_date in %s", r.Body)
		}
		if s, _ := v.(string); len(s) < 10 || s[:10] != "2026-11-20" {
			t.Errorf("start_date = %v, want 2026-11-20", v)
		}
	})

	t.Run("suggested fare", func(t *testing.T) {
		v, ok := findKey(body, "suggested_fare")
		if !ok {
			t.Fatalf("no suggested_fare in %s", r.Body)
		}
		got := num(t, v, "suggested_fare")
		want := w.fareFor(t, "auto", roadKm(koramangala, whitefield))
		// Rounding of the fare is unspecified: allow up to 1 rupee.
		if math.Abs(got-want) > 1 {
			t.Errorf("suggested_fare = %v, want about %.2f (fare_rules on %.2f road km)",
				got, want, roadKm(koramangala, whitefield))
		}
	})

	t.Run("per-slot pickup times", func(t *testing.T) {
		for _, d := range []int{1, 3, 5} {
			if !previewHasSlot(body, d, "outbound", "08:30") {
				t.Errorf("preview has no outbound slot weekday %d at 08:30: %s", d, r.Body)
			}
		}
	})

	t.Run("nothing written", func(t *testing.T) {
		if n := w.count(t, `SELECT count(*) FROM series WHERE rider_id = $1`, w.uid("Priya")); n != 0 {
			t.Errorf("preview created %d series for Priya", n)
		}
		if n := w.count(t, `SELECT count(*) FROM offers`); n != 0 {
			t.Errorf("preview opened %d offers", n)
		}
	})
}

// previewHasSlot looks for a slot object {weekday, direction, <time>} in a
// preview/series response. The time key may be local_pickup_time or time.
func previewHasSlot(body any, weekday int, direction, hhmm string) bool {
	v, ok := findKey(body, "slots")
	if !ok {
		return false
	}
	list, _ := v.([]any)
	for _, e := range list {
		m, _ := e.(map[string]any)
		if m == nil {
			continue
		}
		wd, _ := m["weekday"].(float64)
		dir, _ := m["direction"].(string)
		if int(wd) != weekday || (dir != "" && dir != direction) {
			continue
		}
		for _, k := range []string{"local_pickup_time", "pickup_time", "time"} {
			if s, _ := m[k].(string); len(s) >= 5 && s[:5] == hhmm {
				return true
			}
		}
	}
	return false
}

// R1-I2 Create opens round 1: series finding_driver; 3 slots with no driver;
// no rides; one offer (kind series, round 1, expires_at +15 min); recipients
// Ravi, Meena, Anil, Suresh (not the bike driver); one push each.
func TestR1_I2_CreateOpensRound1(t *testing.T) {
	w := start(t)
	id, r := w.createSeries(t, "Priya", priyaReq())
	body := anyJSON(t, r)

	t.Run("response", func(t *testing.T) {
		if st := seriesStatusOf(t, r); st != "finding_driver" {
			t.Errorf("response status %q, want finding_driver; body %s", st, r.Body)
		}
		if v, ok := findKey(body, "round"); !ok || num(t, v, "round") != 1 {
			t.Errorf("response round = %v (found %v), want 1", v, ok)
		}
		v, ok := findKey(body, "expires_at")
		if !ok {
			t.Fatalf("no expires_at in response %s", r.Body)
		}
		s, _ := v.(string)
		exp, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatalf("expires_at %q: %v", s, err)
		}
		if !exp.Equal(t0.Add(window)) {
			t.Errorf("expires_at = %v, want %v", exp, t0.Add(window))
		}
		slots, _ := findKey(body, "slots")
		if l, _ := slots.([]any); len(l) != 3 {
			t.Errorf("response has %d slots, want 3", len(l))
		}
	})

	t.Run("series row", func(t *testing.T) {
		s := w.series(t, id)
		if s.Status != "finding_driver" {
			t.Errorf("status %s, want finding_driver", s.Status)
		}
		if s.RiderID != w.uid("Priya") {
			t.Errorf("rider_id %s, want Priya", s.RiderID)
		}
		// RULING: start is the first chosen weekday from tomorrow on.
		if s.StartDate != "2026-11-20" {
			t.Errorf("start_date %s, want 2026-11-20", s.StartDate)
		}
		if s.TravelMinutes != 51 {
			t.Errorf("travel_minutes %d, want 51", s.TravelMinutes)
		}
		if s.TimeZone != "Asia/Kolkata" {
			t.Errorf("time_zone %s, want Asia/Kolkata", s.TimeZone)
		}
		if s.VehicleType != "auto" {
			t.Errorf("vehicle_type %s, want auto", s.VehicleType)
		}
		if s.UsualPayment != "upi" {
			t.Errorf("usual_payment %s, want upi", s.UsualPayment)
		}
		if math.Abs(s.SuggestedFare-w.fareFor(t, "auto", roadKm(koramangala, whitefield))) > 1 {
			t.Errorf("suggested_fare %v not from fare rules", s.SuggestedFare)
		}
	})

	t.Run("three slots, no driver", func(t *testing.T) {
		sl := w.slots(t, id)
		if len(sl) != 3 {
			t.Fatalf("%d slots, want 3: %+v", len(sl), sl)
		}
		for i, wd := range []int{1, 3, 5} {
			s := sl[i]
			if s.Weekday != wd || s.Direction != "outbound" || s.PickupTime != "08:30" {
				t.Errorf("slot %d = %+v, want weekday %d outbound 08:30", i, s, wd)
			}
			if s.DriverID != "" {
				t.Errorf("slot %d has driver %s, want none", i, s.DriverID)
			}
		}
	})

	t.Run("no rides", func(t *testing.T) {
		if n := len(w.rides(t, id)); n != 0 {
			t.Errorf("%d rides, want 0", n)
		}
	})

	var off offerRow
	t.Run("one offer, round 1, 15 min", func(t *testing.T) {
		all := w.offers(t, id)
		if len(all) != 1 {
			t.Fatalf("%d offers, want 1", len(all))
		}
		off = all[0]
		if off.Kind != "series" || off.Round != 1 || off.Status != "open" {
			t.Errorf("offer %+v, want kind series, round 1, open", off)
		}
		if !off.OpenedAt.Equal(t0) || !off.ExpiresAt.Equal(t0.Add(window)) {
			t.Errorf("offer opened %v expires %v, want %v / %v", off.OpenedAt, off.ExpiresAt, t0, t0.Add(window))
		}
	})

	t.Run("recipients are the four auto drivers", func(t *testing.T) {
		off = w.openOffer(t, id)
		w.expectRecipients(t, off.ID, autoDrivers...)
		_, bikeID := w.bikeDriver(t)
		if _, ok := w.recipients(t, off.ID)[bikeID]; ok {
			t.Error("the bike driver is a recipient")
		}
	})

	t.Run("events written", func(t *testing.T) {
		if w.eventsFor(t, id) == 0 {
			t.Error("no events row for the series")
		}
	})

	t.Run("one push each", func(t *testing.T) {
		// Pushes go through notification_outbox; the sweep's send_outbox
		// step delivers them.
		if n := w.count(t, `SELECT count(*) FROM notification_outbox WHERE user_id = ANY($1::uuid[])`,
			[]string{w.uid("Ravi"), w.uid("Meena"), w.uid("Anil"), w.uid("Suresh")}); n != 4 {
			t.Errorf("%d outbox rows for the auto drivers, want 4", n)
		}
		w.Sweep(t)
		for _, d := range autoDrivers {
			if got := len(w.Push.To(d)); got != 1 {
				t.Errorf("%s got %d pushes, want 1", d, got)
			}
			w.expectPushes(t, d, pushNewRequest, 1)
		}
		bike, _ := w.bikeDriver(t)
		if got := len(w.Push.To(bike)); got != 0 {
			t.Errorf("bike driver %s got %d pushes, want 0", bike, got)
		}
		if got := len(w.Push.To("Priya")); got != 0 {
			t.Errorf("Priya got %d pushes at create, want 0", got)
		}
		// Sweeping before expires_at must not close the round.
		if o := w.openOffer(t, id); o.Round != 1 {
			t.Errorf("offer round %d after an early sweep, want 1", o.Round)
		}
	})
}

// R1-I3 Pick with a clash first: Meena and Ravi interested; clock +15 min;
// RunOnce (shuffle: Meena, Ravi) -> Meena clashed; Ravi picked; every slot =
// Ravi; series active; rides 20 Nov–3 Dec assigned (RULING; the spec row said
// 23 Nov–3 Dec); pushes to Priya, Ravi,
// Meena.
func TestR1_I3_PickWithClashFirst(t *testing.T) {
	w := start(t, testkit.WithShuffle("Meena", "Ravi"))
	id, _ := w.createSeries(t, "Priya", priyaReq())
	off := w.openOffer(t, id)
	w.Sweep(t) // deliver round-1 pushes
	w.Push.Reset()
	w.markInterested(t, "Meena", off.ID)
	w.markInterested(t, "Ravi", off.ID)
	versionBefore := w.series(t, id).Version

	w.closeWindow(t)

	t.Run("recipient results", func(t *testing.T) {
		if got := w.result(t, off.ID, "Meena"); got != "clashed" {
			t.Errorf("Meena result %q, want clashed", got)
		}
		if got := w.result(t, off.ID, "Ravi"); got != "picked" {
			t.Errorf("Ravi result %q, want picked", got)
		}
		for _, d := range []string{"Anil", "Suresh"} {
			if got := w.result(t, off.ID, d); got == "picked" {
				t.Errorf("%s result picked", d)
			}
		}
	})

	t.Run("offer closed as assigned", func(t *testing.T) {
		all := w.offers(t, id)
		if len(all) != 1 {
			t.Fatalf("%d offers, want 1 (no second round)", len(all))
		}
		if all[0].Status != "closed" || all[0].Outcome != "assigned" {
			t.Errorf("offer %+v, want closed/assigned", all[0])
		}
	})

	t.Run("series active, every slot Ravi", func(t *testing.T) {
		s := w.series(t, id)
		if s.Status != "active" {
			t.Errorf("series %s, want active", s.Status)
		}
		if s.Version <= versionBefore {
			t.Errorf("version %d not raised from %d", s.Version, versionBefore)
		}
		for _, sl := range w.slots(t, id) {
			if sl.DriverID != w.uid("Ravi") {
				t.Errorf("slot %+v driver is not Ravi", sl)
			}
		}
	})

	// RULING: 14 days from the start date, 20 Nov - 3 Dec.
	t.Run("rides 20 Nov - 3 Dec assigned to Ravi", func(t *testing.T) {
		rides := w.rides(t, id)
		var dates []string
		for _, r := range rides {
			dates = append(dates, r.LocalDate)
			if r.Status != "assigned" || r.DriverID != w.uid("Ravi") {
				t.Errorf("ride %+v, want assigned to Ravi", r)
			}
			wantPickup, _ := time.ParseInLocation("2006-01-02 15:04", r.LocalDate+" 08:30", ist)
			if !r.PickupAt.Equal(wantPickup) {
				t.Errorf("ride %s pickup_at %v, want %v", r.LocalDate, r.PickupAt, wantPickup)
			}
			// busy_until = estimated drop-off + 10 = pickup + 51 + 10 min.
			if want := wantPickup.Add(61 * time.Minute); !r.BusyUntil.Equal(want) {
				t.Errorf("ride %s busy_until %v, want %v", r.LocalDate, r.BusyUntil, want)
			}
		}
		if !equalStrings(dates, priyaRideDates) {
			t.Errorf("ride dates %v, want %v", dates, priyaRideDates)
		}
	})

	t.Run("series.get shows Ravi on the slots", func(t *testing.T) {
		r := w.as(t, "Priya").Do(t, http.MethodGet, pathSeriesID(id), nil)
		expectStatus(t, r, http.StatusOK)
		if st := seriesStatusOf(t, r); st != "active" {
			t.Errorf("series.get status %q, want active", st)
		}
		if !containsString(r.Body, w.uid("Ravi")) {
			t.Errorf("series.get does not name Ravi's id: %s", r.Body)
		}
	})

	t.Run("pushes", func(t *testing.T) {
		w.Sweep(t) // flush anything still queued; must not double-send
		w.expectPushes(t, "Priya", pushRiderWin, 1)
		w.expectPushes(t, "Ravi", pushDriverWin, 1)
		w.expectPushes(t, "Meena", pushNotPicked, 1)
		for _, d := range []string{"Anil", "Suresh"} {
			if n := len(w.Push.To(d)); n != 0 {
				t.Errorf("%s (not interested) got %d pushes, want 0", d, n)
			}
		}
		w.expectPushes(t, "Ravi", pushNotPicked, 0)
	})
}

// R1-I4 Three empty rounds: nobody interested; three windows -> rounds 2 and
// 3 push non-responders only; series no_driver_found; Priya's push.
func TestR1_I4_ThreeEmptyRounds(t *testing.T) {
	w := start(t)
	id, _ := w.createSeries(t, "Priya", priyaReq())
	w.Sweep(t)

	for round := 1; round <= 3; round++ {
		off := w.openOffer(t, id)
		if off.Round != round {
			t.Fatalf("open offer round %d, want %d", off.Round, round)
		}
		wantOpen := t0.Add(time.Duration(round-1) * window)
		if !off.OpenedAt.Equal(wantOpen) || !off.ExpiresAt.Equal(wantOpen.Add(window)) {
			t.Errorf("round %d opened %v expires %v, want %v + 15 min", round, off.OpenedAt, off.ExpiresAt, wantOpen)
		}
		w.expectRecipients(t, off.ID, autoDrivers...)
		w.closeWindow(t)
	}

	t.Run("no_driver_found", func(t *testing.T) {
		if st := w.seriesStatus(t, id); st != "no_driver_found" {
			t.Errorf("series %s, want no_driver_found", st)
		}
		all := w.offers(t, id)
		if len(all) != 3 {
			t.Fatalf("%d offers, want 3", len(all))
		}
		for i, o := range all {
			if o.Round != i+1 || o.Status != "closed" || o.Outcome != "no_fit" {
				t.Errorf("offer %d = %+v, want round %d closed/no_fit", i, o, i+1)
			}
		}
		if n := len(w.rides(t, id)); n != 0 {
			t.Errorf("%d rides, want 0", n)
		}
		for _, sl := range w.slots(t, id) {
			if sl.DriverID != "" {
				t.Errorf("slot %+v has a driver", sl)
			}
		}
	})

	t.Run("pushes", func(t *testing.T) {
		w.Sweep(t)
		for _, d := range autoDrivers {
			w.expectPushes(t, d, pushNewRequest, 1)
			w.expectPushes(t, d, pushStillOpen, 2)
		}
		w.expectPushes(t, "Priya", pushNoDriver, 1)
	})

	t.Run("further sweeps change nothing", func(t *testing.T) {
		w.closeWindow(t)
		if n := len(w.offers(t, id)); n != 3 {
			t.Errorf("%d offers after an extra window, want 3", n)
		}
		w.expectPushes(t, "Priya", pushNoDriver, 1)
	})

	t.Run("home reads status (phone was off)", func(t *testing.T) {
		r := w.as(t, "Priya").Do(t, http.MethodGet, pathSeriesID(id), nil)
		expectStatus(t, r, http.StatusOK)
		if st := seriesStatusOf(t, r); st != "no_driver_found" {
			t.Errorf("series.get status %q, want no_driver_found", st)
		}
	})
}

// R1-I5 Nobody eligible: only bike drivers exist; create an auto series ->
// straight to no_driver_found; no rounds.
func TestR1_I5_NobodyEligible(t *testing.T) {
	w := start(t)
	// Make every auto driver ineligible (plan lapsed), so only the bike
	// driver is eligible for anything.
	w.exec(t, `UPDATE drivers SET subscription_until = DATE '2026-11-18' WHERE vehicle_type::text = 'auto'`)

	id, r := w.createSeries(t, "Priya", priyaReq())
	if st := seriesStatusOf(t, r); st != "no_driver_found" {
		t.Errorf("create response status %q, want no_driver_found", st)
	}
	if st := w.seriesStatus(t, id); st != "no_driver_found" {
		t.Errorf("series %s, want no_driver_found", st)
	}
	if n := len(w.offers(t, id)); n != 0 {
		t.Errorf("%d offers, want none (no rounds spent)", n)
	}
	if n := w.count(t, `SELECT count(*) FROM offer_recipients`); n != 0 {
		t.Errorf("%d offer recipients, want 0", n)
	}
	w.Sweep(t)
	for _, d := range autoDrivers {
		if n := len(w.Push.To(d)); n != 0 {
			t.Errorf("%s got %d pushes, want 0", d, n)
		}
	}
	bike, _ := w.bikeDriver(t)
	if n := len(w.Push.To(bike)); n != 0 {
		t.Errorf("bike driver got %d pushes, want 0", n)
	}
}

// R1-I6 Withdrawn interest is ignored: Ravi interested, then withdraws;
// close -> Ravi withdrew; not picked.
func TestR1_I6_WithdrawnInterestIgnored(t *testing.T) {
	// Ravi first in the shuffle: if his withdrawn interest were counted he
	// would be picked.
	w := start(t, testkit.WithShuffle("Ravi", "Anil"))
	id, _ := w.createSeries(t, "Priya", priyaReq())
	off := w.openOffer(t, id)
	w.markInterested(t, "Ravi", off.ID)
	w.markInterested(t, "Anil", off.ID)
	w.Advance(t, 5*time.Minute)
	w.withdraw(t, "Ravi", off.ID)

	rr := w.recipients(t, off.ID)[w.uid("Ravi")]
	if !rr.Withdrawn {
		t.Error("Ravi's withdrawn_at not set")
	}

	w.Advance(t, window-5*time.Minute)
	w.Sweep(t)

	if got := w.result(t, off.ID, "Ravi"); got != "withdrew" {
		t.Errorf("Ravi result %q, want withdrew", got)
	}
	if got := w.result(t, off.ID, "Anil"); got != "picked" {
		t.Errorf("Anil result %q, want picked", got)
	}
	for _, sl := range w.slots(t, id) {
		if sl.DriverID != w.uid("Anil") {
			t.Errorf("slot %+v not Anil", sl)
		}
	}
	for _, r := range w.rides(t, id) {
		if r.DriverID == w.uid("Ravi") {
			t.Errorf("ride %s given to Ravi", r.LocalDate)
		}
	}
	w.Sweep(t)
	w.expectPushes(t, "Ravi", pushDriverWin, 0)
	// SPEC-AMBIGUITY: the spec doesn't say whether a driver who withdrew is
	// told "went to another driver"; not asserted.
}

// R1-I7 Not for me: Anil dismisses in round 1; round 2 opens -> Anil not a
// recipient of round 2.
func TestR1_I7_NotForMe(t *testing.T) {
	w := start(t)
	id, _ := w.createSeries(t, "Priya", priyaReq())
	off1 := w.openOffer(t, id)
	w.Sweep(t)
	w.dismiss(t, "Anil", off1.ID)

	if !w.recipients(t, off1.ID)[w.uid("Anil")].Dismissed {
		t.Error("Anil's dismissed_at not set")
	}
	t.Run("hidden from offers.list now", func(t *testing.T) {
		r := w.as(t, "Anil").Do(t, http.MethodGet, pathOffers, nil)
		expectStatus(t, r, http.StatusOK)
		if findObj(anyJSON(t, r), off1.ID) != nil {
			t.Errorf("dismissed offer still listed for Anil: %s", r.Body)
		}
	})

	w.closeWindow(t)
	off2 := w.openOffer(t, id)
	if off2.Round != 2 {
		t.Fatalf("open round %d, want 2", off2.Round)
	}

	t.Run("round 2 recipients", func(t *testing.T) {
		w.expectRecipients(t, off2.ID, "Ravi", "Meena", "Suresh")
	})
	t.Run("no Still open push to Anil", func(t *testing.T) {
		w.Sweep(t)
		w.expectPushes(t, "Anil", pushStillOpen, 0)
		w.expectPushes(t, "Ravi", pushStillOpen, 1)
	})
	t.Run("hidden from offers.list in round 2", func(t *testing.T) {
		r := w.as(t, "Anil").Do(t, http.MethodGet, pathOffers, nil)
		expectStatus(t, r, http.StatusOK)
		v := anyJSON(t, r)
		if findObj(v, off2.ID) != nil || findObj(v, off1.ID) != nil {
			t.Errorf("Anil still sees the request: %s", r.Body)
		}
	})
	t.Run("not in round 3 either", func(t *testing.T) {
		w.closeWindow(t)
		off3 := w.openOffer(t, id)
		w.expectRecipients(t, off3.ID, "Ravi", "Meena", "Suresh")
	})
}

// R1-I8 Cancel before the pick: cancel during round 1 -> series ended;
// offer cancelled; interested drivers told.
func TestR1_I8_CancelBeforePick(t *testing.T) {
	w := start(t)
	id, _ := w.createSeries(t, "Priya", priyaReq())
	off := w.openOffer(t, id)
	w.Sweep(t)
	w.Push.Reset()
	w.markInterested(t, "Ravi", off.ID)
	w.markInterested(t, "Meena", off.ID)
	w.Advance(t, 5*time.Minute)

	r := w.as(t, "Priya").Do(t, http.MethodPost, pathCancelRequest(id), nil)
	expectStatus(t, r, http.StatusNoContent)

	t.Run("series ended, offer cancelled", func(t *testing.T) {
		if st := w.seriesStatus(t, id); st != "ended" {
			t.Errorf("series %s, want ended", st)
		}
		all := w.offers(t, id)
		if len(all) != 1 || all[0].Status != "closed" || all[0].Outcome != "cancelled" {
			t.Errorf("offers %+v, want one closed/cancelled", all)
		}
		if w.eventsFor(t, id) < 2 {
			t.Error("cancel wrote no events row")
		}
	})

	t.Run("interested drivers told", func(t *testing.T) {
		w.Sweep(t)
		w.expectPushes(t, "Ravi", pushWithdrew, 1)
		w.expectPushes(t, "Meena", pushWithdrew, 1)
		for _, d := range []string{"Anil", "Suresh"} {
			if n := len(w.Push.To(d)); n != 0 {
				t.Errorf("%s (not interested) got %d pushes, want 0", d, n)
			}
		}
	})

	t.Run("late interest refused", func(t *testing.T) {
		rr := w.as(t, "Anil").Do(t, http.MethodPost, pathMarkInterested(off.ID), nil)
		expectErrorWithState(t, rr, http.StatusConflict, "offer_closed")
	})

	t.Run("window end does nothing", func(t *testing.T) {
		w.closeWindow(t)
		if st := w.seriesStatus(t, id); st != "ended" {
			t.Errorf("series %s after window, want ended", st)
		}
		if n := len(w.offers(t, id)); n != 1 {
			t.Errorf("%d offers, want 1", n)
		}
		if n := len(w.rides(t, id)); n != 0 {
			t.Errorf("%d rides, want 0", n)
		}
		for _, sl := range w.slots(t, id) {
			if sl.DriverID != "" {
				t.Errorf("slot %+v got a driver", sl)
			}
		}
	})

	t.Run("series.get shows ended", func(t *testing.T) {
		rr := w.as(t, "Priya").Do(t, http.MethodGet, pathSeriesID(id), nil)
		expectStatus(t, rr, http.StatusOK)
		if st := seriesStatusOf(t, rr); st != "ended" {
			t.Errorf("series.get status %q, want ended", st)
		}
	})
}

// ---------------------------------------------------------------- small ----

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsString(body []byte, s string) bool {
	return s != "" && strings.Contains(string(body), s)
}
