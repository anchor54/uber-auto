package integration

// Edge cases table rows that can be checked through the API, the endpoint
// errors from the Endpoints table, the D2 driver endpoints, and R1.2's
// per-day / arrive-by / return-leg preview rules.
//
// Edge-case rows covered elsewhere: two requests closing in the same minute
// (TestR1_I11), cancel while picking (TestR1_I9), two sweeps (TestR1_I10),
// nobody eligible (TestR1_I5), retry with changed times (TestR1_I12), phone
// off when the result arrives (TestR1_I4 "home reads status").

import (
	"net/http"
	"testing"
	"time"

	"commute-backend/internal/testkit"
)

const missingID = "00000000-0000-4000-8000-000000000000"

// ---------------------------------------------------------------- edges ----

// "A sweep crashes mid-pick: nothing committed ...; the closing offer is
// retried after 5 minutes and stops at the series status check."
func TestEdge_CrashedSweepRetriedAfter5Min(t *testing.T) {
	w := start(t)
	id, _ := w.createSeries(t, "Priya", priyaReq())
	off := w.openOffer(t, id)
	w.markInterested(t, "Ravi", off.ID)
	w.Advance(t, window)

	// Simulate a sweep that set the offer closing and then died.
	w.exec(t, `UPDATE offers SET status = 'closing', closing_at = $2 WHERE id = $1`,
		off.ID, t0.Add(window))

	t.Run("not retried within 5 minutes", func(t *testing.T) {
		w.Advance(t, 4*time.Minute)
		w.Sweep(t)
		if st := w.seriesStatus(t, id); st != "finding_driver" {
			t.Errorf("series %s, want finding_driver", st)
		}
		var st string
		w.one(t, `SELECT status::text FROM offers WHERE id = $1`, []any{off.ID}, &st)
		if st != "closing" {
			t.Errorf("offer %s, want still closing", st)
		}
	})

	t.Run("retried after 5 minutes", func(t *testing.T) {
		w.Advance(t, time.Minute+time.Second)
		w.Sweep(t)
		if st := w.seriesStatus(t, id); st != "active" {
			t.Errorf("series %s, want active", st)
		}
		if got := w.result(t, off.ID, "Ravi"); got != "picked" {
			t.Errorf("Ravi %q, want picked", got)
		}
		if n := len(w.rides(t, id)); n == 0 {
			t.Error("no rides generated")
		}
	})

	t.Run("a stale closing offer whose series is already active stops", func(t *testing.T) {
		before := len(w.rides(t, id))
		// The crash happened after the assignment committed but the offer was
		// left closing (e.g. a crash between two steps).
		w.exec(t, `UPDATE offers SET status = 'closing', closing_at = $2 WHERE id = $1`,
			off.ID, t0.Add(window))
		w.Advance(t, 6*time.Minute)
		w.Sweep(t)
		if st := w.seriesStatus(t, id); st != "active" {
			t.Errorf("series %s, want active", st)
		}
		if n := len(w.rides(t, id)); n != before {
			t.Errorf("rides %d -> %d; the retried pick assigned again", before, n)
		}
		picked := 0
		for _, r := range w.recipients(t, off.ID) {
			if r.Result == "picked" {
				picked++
			}
		}
		if picked != 1 {
			t.Errorf("%d picked recipients, want 1", picked)
		}
		if n := len(w.offers(t, id)); n != 1 {
			t.Errorf("%d offers, want 1 (no new round)", n)
		}
	})
}

// "The interested driver's plan lapses before the window closes: eligibility
// is checked again at the pick; that driver is skipped."
func TestEdge_PlanLapsesBeforeClose(t *testing.T) {
	w := start(t, testkit.WithShuffle("Ravi", "Anil"))
	id, _ := w.createSeries(t, "Priya", priyaReq())
	off := w.openOffer(t, id)
	w.markInterested(t, "Ravi", off.ID)
	w.markInterested(t, "Anil", off.ID)
	w.exec(t, `UPDATE drivers SET subscription_until = DATE '2026-11-18' WHERE user_id = $1`, w.uid("Ravi"))
	w.closeWindow(t)

	// SPEC-AMBIGUITY: the spec says the driver "is skipped"; the result enum
	// has "ineligible", which is asserted.
	if got := w.result(t, off.ID, "Ravi"); got != "ineligible" {
		t.Errorf("Ravi %q, want ineligible", got)
	}
	if got := w.result(t, off.ID, "Anil"); got != "picked" {
		t.Errorf("Anil %q, want picked", got)
	}
	for _, s := range w.slots(t, id) {
		if s.DriverID != w.uid("Anil") {
			t.Errorf("slot %+v not Anil", s)
		}
	}
}

// "The series includes the driver's day off: clash."
func TestEdge_DayOffIsClash(t *testing.T) {
	w := start(t, testkit.WithShuffle("Ravi", "Anil"))
	// SPEC-AMBIGUITY: drivers.day_off is "weekday or null"; stored like
	// series_slots.weekday (ISO 1=Mon .. 7=Sun), so 3 = Wednesday.
	w.exec(t, `UPDATE drivers SET day_off = 3 WHERE user_id = $1`, w.uid("Ravi"))
	id, _ := w.createSeries(t, "Priya", priyaReq())
	off := w.openOffer(t, id)
	w.markInterested(t, "Ravi", off.ID)
	w.markInterested(t, "Anil", off.ID)
	w.closeWindow(t)

	if got := w.result(t, off.ID, "Ravi"); got != "clashed" {
		t.Errorf("Ravi %q, want clashed (Wednesday is his day off)", got)
	}
	if got := w.result(t, off.ID, "Anil"); got != "picked" {
		t.Errorf("Anil %q, want picked", got)
	}
}

// "The driver has leave on one date in the first 14 days: not a clash; that
// ride is created as needs_driver."
func TestEdge_LeaveIsNotClash(t *testing.T) {
	// SPEC-AMBIGUITY: R1+D2's first migration has no leave table (leave comes
	// with R4+D4), so leave can't be seeded through this feature's schema.
	t.Skip("no leave table in the R1+D2 migration; covered by R4+D4")
}

// "The request is made at 23:50 for tomorrow 07:00: allowed if the start date
// is tomorrow; rounds may run until about 00:40."
func TestEdge_LateNightRequestForTomorrow(t *testing.T) {
	w := start(t)
	w.Advance(t, 13*time.Hour+50*time.Minute) // Thu 23:50 IST
	now := t0.Add(13*time.Hour + 50*time.Minute)

	req := priyaReq()
	req.Slots = slotsAt("07:00", 5) // Fridays only; tomorrow is Fri 20 Nov

	// RULING (orchestrator): the start date is the first chosen weekday from
	// tomorrow on, consistent with R1-I1 (now Fri 20 Nov).
	id, _ := w.createSeries(t, "Priya", req)
	s := w.series(t, id)
	if s.StartDate != "2026-11-20" {
		t.Errorf("start_date %s, want 2026-11-20 (tomorrow)", s.StartDate)
	}
	if s.Status != "finding_driver" {
		t.Errorf("status %s, want finding_driver", s.Status)
	}
	o := w.openOffer(t, id)
	if !o.ExpiresAt.Equal(now.Add(window)) {
		t.Errorf("expires_at %v, want %v (past midnight)", o.ExpiresAt, now.Add(window))
	}
	w.markInterested(t, "Anil", o.ID)
	w.closeWindow(t)
	if st := w.seriesStatus(t, id); st != "active" {
		t.Fatalf("series %s, want active", st)
	}
	rides := w.rides(t, id)
	if len(rides) == 0 || rides[0].LocalDate != "2026-11-20" {
		t.Errorf("first ride %+v, want 2026-11-20", rides)
	}
}

// R1.2 / Travel time: per-day times, arrive-by conversion (arrival − trip −
// 10 min, rounded down to 5 min) and a return leg with its own slots.
func TestR1_PreviewPerDayArriveByAndReturn(t *testing.T) {
	w := start(t)
	req := priyaReq()
	req.Slots = []slotReq{
		{Weekday: 1, Direction: "outbound", Timing: "pickup_at", Time: "08:30"},
		{Weekday: 3, Direction: "outbound", Timing: "pickup_at", Time: "08:30"},
		// 09:30 − 51 − 10 = 08:29 -> 08:25.
		{Weekday: 5, Direction: "outbound", Timing: "arrive_by", Time: "09:30"},
		{Weekday: 1, Direction: "return", Timing: "pickup_at", Time: "18:30"},
		{Weekday: 3, Direction: "return", Timing: "pickup_at", Time: "18:30"},
		{Weekday: 5, Direction: "return", Timing: "pickup_at", Time: "19:00"},
	}
	r := w.as(t, "Priya").Do(t, http.MethodPost, pathPreview, req)
	expectStatus(t, r, http.StatusOK)
	body := anyJSON(t, r)
	for _, c := range []struct {
		wd       int
		dir, hhm string
	}{{1, "outbound", "08:30"}, {3, "outbound", "08:30"}, {5, "outbound", "08:25"},
		{1, "return", "18:30"}, {3, "return", "18:30"}, {5, "return", "19:00"}} {
		if !previewHasSlot(body, c.wd, c.dir, c.hhm) {
			t.Errorf("preview has no %s slot weekday %d at %s: %s", c.dir, c.wd, c.hhm, r.Body)
		}
	}

	t.Run("create stores six slots", func(t *testing.T) {
		id, _ := w.createSeries(t, "Priya", req)
		sl := w.slots(t, id)
		if len(sl) != 6 {
			t.Fatalf("%d slots, want 6", len(sl))
		}
		got := map[[2]any]string{}
		for _, s := range sl {
			got[[2]any{s.Weekday, s.Direction}] = s.PickupTime
		}
		if got[[2]any{5, "outbound"}] != "08:25" {
			t.Errorf("Fri outbound pickup %q, want 08:25", got[[2]any{5, "outbound"}])
		}
		if got[[2]any{5, "return"}] != "19:00" {
			t.Errorf("Fri return pickup %q, want 19:00", got[[2]any{5, "return"}])
		}
		var timing string
		w.one(t, `SELECT timing::text FROM series_slots WHERE series_id = $1 AND weekday = 5 AND direction::text = 'outbound'`,
			[]any{id}, &timing)
		if timing != "arrive_by" {
			t.Errorf("Fri outbound timing %q, want arrive_by", timing)
		}
	})
}

// ---------------------------------------------------------------- API errors

func TestAPI_SeriesGetErrors(t *testing.T) {
	w := start(t)
	id, _ := w.createSeries(t, "Priya", priyaReq())

	t.Run("own series 200", func(t *testing.T) {
		r := w.as(t, "Priya").Do(t, http.MethodGet, pathSeriesID(id), nil)
		expectStatus(t, r, http.StatusOK)
		if st := seriesStatusOf(t, r); st != "finding_driver" {
			t.Errorf("status %q, want finding_driver", st)
		}
		if _, ok := findKey(anyJSON(t, r), "slots"); !ok {
			t.Errorf("no slots in series.get: %s", r.Body)
		}
	})
	t.Run("another rider 403", func(t *testing.T) {
		other := w.SignUp(t, "9876543211")
		r := other.Do(t, http.MethodGet, pathSeriesID(id), nil)
		expectStatus(t, r, http.StatusForbidden)
	})
	t.Run("missing 404", func(t *testing.T) {
		r := w.as(t, "Priya").Do(t, http.MethodGet, pathSeriesID(missingID), nil)
		expectStatus(t, r, http.StatusNotFound)
	})
}

func TestAPI_CancelAlreadyAssigned(t *testing.T) {
	w := start(t)
	id := w.assignRavi(t)
	r := w.as(t, "Priya").Do(t, http.MethodPost, pathCancelRequest(id), nil)
	m := expectErrorWithState(t, r, http.StatusConflict, "already_assigned")
	if !containsString(r.Body, w.uid("Ravi")) && !containsString(r.Body, "Ravi") {
		t.Errorf("already_assigned does not carry the driver: %v", m)
	}
	if st := w.seriesStatus(t, id); st != "active" {
		t.Errorf("series %s, want still active", st)
	}
}

func TestAPI_RetryOnlyFromNoDriverFound(t *testing.T) {
	w := start(t)
	id, _ := w.createSeries(t, "Priya", priyaReq())
	t.Run("finding_driver 409", func(t *testing.T) {
		r := w.as(t, "Priya").Do(t, http.MethodPost, pathRetry(id), retryReq{})
		expectStatus(t, r, http.StatusConflict)
		if n := len(w.offers(t, id)); n != 1 {
			t.Errorf("%d offers after a refused retry, want 1", n)
		}
	})
	t.Run("retry without changes", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			w.closeWindow(t)
		}
		if st := w.seriesStatus(t, id); st != "no_driver_found" {
			t.Fatalf("series %s, want no_driver_found", st)
		}
		r := w.as(t, "Priya").Do(t, http.MethodPost, pathRetry(id), retryReq{})
		expectStatus(t, r, http.StatusOK)
		if o := w.openOffer(t, id); o.Round != 1 {
			t.Errorf("round %d, want 1", o.Round)
		}
		for _, s := range w.slots(t, id) {
			if s.PickupTime != "08:30" {
				t.Errorf("slot %+v changed without new times", s)
			}
		}
	})
	t.Run("active 409", func(t *testing.T) {
		o := w.openOffer(t, id)
		w.markInterested(t, "Anil", o.ID)
		w.closeWindow(t)
		r := w.as(t, "Priya").Do(t, http.MethodPost, pathRetry(id), retryReq{})
		expectStatus(t, r, http.StatusConflict)
	})
}

func TestAPI_RideTypeNotAllowed(t *testing.T) {
	w := start(t)
	req := priyaReq()
	req.VehicleType = "hovercraft"
	t.Run("preview", func(t *testing.T) {
		r := w.as(t, "Priya").Do(t, http.MethodPost, pathPreview, req)
		expectError(t, r, http.StatusUnprocessableEntity, "ride_type_not_allowed")
	})
	t.Run("create", func(t *testing.T) {
		r := w.as(t, "Priya").Do(t, http.MethodPost, pathSeries, req)
		expectError(t, r, http.StatusUnprocessableEntity, "ride_type_not_allowed")
		if n := w.count(t, `SELECT count(*) FROM series WHERE rider_id = $1`, w.uid("Priya")); n != 0 {
			t.Errorf("%d series created", n)
		}
	})
}

func TestAPI_OfferNotFound(t *testing.T) {
	w := start(t)
	ravi := w.as(t, "Ravi")
	expectStatus(t, ravi.Do(t, http.MethodGet, pathOffer(missingID), nil), http.StatusNotFound)
	expectStatus(t, ravi.Do(t, http.MethodPost, pathDismiss(missingID), nil), http.StatusNotFound)
}

func TestAPI_OffersListNotEligible(t *testing.T) {
	w := start(t)
	w.exec(t, `UPDATE drivers SET subscription_until = DATE '2026-11-18' WHERE user_id = $1`, w.uid("Ravi"))
	r := w.as(t, "Ravi").Do(t, http.MethodGet, pathOffers, nil)
	expectError(t, r, http.StatusForbidden, "not_eligible")
}

// ---------------------------------------------------------------- D2 -------

// D2.1 / D2.2: offers.list shows only offers the driver received, with their
// interest; offer.get gives the detail; mark and withdraw flip the interest.
func TestD2_OffersListDetailAndInterest(t *testing.T) {
	w := start(t)
	id, _ := w.createSeries(t, "Priya", priyaReq())
	off := w.openOffer(t, id)

	// SPEC-AMBIGUITY: offers.list's "with their interest" is read as a
	// boolean "interested" on each offer object.
	interested := func(t *testing.T, driver string) (listed bool, val bool) {
		t.Helper()
		r := w.as(t, driver).Do(t, http.MethodGet, pathOffers, nil)
		expectStatus(t, r, http.StatusOK)
		o := findObj(anyJSON(t, r), off.ID)
		if o == nil {
			return false, false
		}
		b, _ := o["interested"].(bool)
		return true, b
	}

	t.Run("recipient sees it, not interested", func(t *testing.T) {
		listed, in := interested(t, "Ravi")
		if !listed {
			t.Fatal("Ravi's offers.list lacks the offer")
		}
		if in {
			t.Error("shown as interested before marking")
		}
	})
	t.Run("bike driver does not see it", func(t *testing.T) {
		bike, _ := w.bikeDriver(t)
		if listed, _ := interested(t, bike); listed {
			t.Error("bike driver sees an auto request")
		}
		// SPEC-AMBIGUITY: offer.get for a driver who isn't a recipient is
		// read as 404 (the only error listed for offer.get).
		r := w.as(t, bike).Do(t, http.MethodGet, pathOffer(off.ID), nil)
		expectStatus(t, r, http.StatusNotFound)
	})
	t.Run("offer.get detail", func(t *testing.T) {
		r := w.as(t, "Ravi").Do(t, http.MethodGet, pathOffer(off.ID), nil)
		expectStatus(t, r, http.StatusOK)
		body := anyJSON(t, r)
		if _, ok := findKey(body, "expires_at"); !ok {
			t.Errorf("offer.get has no expires_at: %s", r.Body)
		}
		if !containsString(r.Body, "Priya") {
			t.Errorf("offer.get does not show the rider's first name: %s", r.Body)
		}
	})
	t.Run("mark interested", func(t *testing.T) {
		w.markInterested(t, "Ravi", off.ID)
		if _, in := interested(t, "Ravi"); !in {
			t.Error("not shown as interested after marking")
		}
		if !w.recipients(t, off.ID)[w.uid("Ravi")].Interested {
			t.Error("interested_at not set")
		}
		if w.eventsFor(t, off.ID) == 0 && w.count(t, `SELECT count(*) FROM events WHERE actor_id = $1`, w.uid("Ravi")) == 0 {
			t.Error("no events row for the interest")
		}
	})
	t.Run("withdraw interest", func(t *testing.T) {
		w.withdraw(t, "Ravi", off.ID)
		if _, in := interested(t, "Ravi"); in {
			t.Error("still shown as interested after withdrawing")
		}
		if !w.recipients(t, off.ID)[w.uid("Ravi")].Withdrawn {
			t.Error("withdrawn_at not set")
		}
	})
	t.Run("non-recipient cannot mark interest", func(t *testing.T) {
		bike, _ := w.bikeDriver(t)
		r := w.as(t, bike).Do(t, http.MethodPost, pathMarkInterested(off.ID), nil)
		if r.Status == http.StatusNoContent {
			t.Error("bike driver could mark interest in an auto request")
		}
	})
}

// D2.3 / rides.week: after a win the driver's week has the new rides,
// flagged new.
func TestD2_RidesWeekAfterWin(t *testing.T) {
	w := start(t)
	id := w.assignRavi(t)
	rides := w.rides(t, id)

	r := w.as(t, "Ravi").Do(t, http.MethodGet, pathRidesWeek("2026-11-23"), nil)
	expectStatus(t, r, http.StatusOK)
	body := anyJSON(t, r)
	// RULING: the Fri 20 Nov ride is in the previous week and must not be listed.
	for _, ride := range rides {
		inWeek := ride.LocalDate >= "2026-11-23" && ride.LocalDate <= "2026-11-29"
		o := findObj(body, ride.ID)
		if !inWeek {
			if o != nil {
				t.Errorf("ride %s outside the week listed", ride.LocalDate)
			}
			continue
		}
		if o == nil {
			t.Errorf("ride %s (%s) missing from rides.week: %s", ride.ID, ride.LocalDate, r.Body)
			continue
		}
		// SPEC-AMBIGUITY: "new ones flagged" read as a boolean "new" (or
		// "is_new") on the ride.
		flag, ok := o["new"].(bool)
		if !ok {
			flag, _ = o["is_new"].(bool)
		}
		if !flag {
			t.Errorf("ride %s not flagged new: %v", ride.LocalDate, o)
		}
	}

	t.Run("not Priya's", func(t *testing.T) {
		r := w.as(t, "Meena").Do(t, http.MethodGet, pathRidesWeek("2026-11-23"), nil)
		expectStatus(t, r, http.StatusOK)
		for _, ride := range rides {
			if findObj(anyJSON(t, r), ride.ID) != nil {
				t.Errorf("Meena's week lists Ravi's ride %s", ride.LocalDate)
			}
		}
	})
}
