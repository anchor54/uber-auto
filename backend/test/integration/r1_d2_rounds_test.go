package integration

// R1-I12 (retry), R1-I13 (late interest) and the round rules from "Offer
// rounds and the pick": rounds 2 and 3 go to the same set minus drivers who
// clashed or withdrew.

import (
	"net/http"
	"testing"
	"time"

	"commute-backend/internal/testkit"
)

// R1-I12 Retry after no driver: continue I4; series.retry with a new time ->
// slots rewritten; a fresh set of 3 rounds; drivers who clashed before are
// offered it again.
func TestR1_I12_RetryAfterNoDriver(t *testing.T) {
	w := start(t, testkit.WithShuffle("Meena"))
	id, _ := w.createSeries(t, "Priya", priyaReq())

	// I4, but with Meena interested in round 1 so there is a driver who
	// clashed before (her Wednesday 09:00 ride).
	off1 := w.openOffer(t, id)
	w.markInterested(t, "Meena", off1.ID)
	w.closeWindow(t)
	if got := w.result(t, off1.ID, "Meena"); got != "clashed" {
		t.Fatalf("Meena result %q in round 1, want clashed", got)
	}
	off2 := w.openOffer(t, id)
	w.expectRecipients(t, off2.ID, "Ravi", "Anil", "Suresh") // clashed Meena left out
	w.closeWindow(t)
	off3 := w.openOffer(t, id)
	w.expectRecipients(t, off3.ID, "Ravi", "Anil", "Suresh")
	w.closeWindow(t)
	if st := w.seriesStatus(t, id); st != "no_driver_found" {
		t.Fatalf("series %s after 3 rounds, want no_driver_found", st)
	}
	oldSlots := w.slots(t, id)
	retryAt := t0.Add(3 * window)

	// SPEC-AMBIGUITY: series.retry's "optional new times" is sent in the same
	// slot shape as series.create.
	r := w.as(t, "Priya").Do(t, http.MethodPost, pathRetry(id), retryReq{Slots: slotsAt("07:30", 1, 3, 5)})
	expectStatus(t, r, http.StatusOK)

	t.Run("back to finding_driver", func(t *testing.T) {
		if st := seriesStatusOf(t, r); st != "finding_driver" {
			t.Errorf("retry response status %q, want finding_driver", st)
		}
		if st := w.seriesStatus(t, id); st != "finding_driver" {
			t.Errorf("series %s, want finding_driver", st)
		}
	})

	t.Run("slots rewritten", func(t *testing.T) {
		sl := w.slots(t, id)
		if len(sl) != len(oldSlots) {
			t.Fatalf("%d slots, want %d", len(sl), len(oldSlots))
		}
		for i, wd := range []int{1, 3, 5} {
			if sl[i].Weekday != wd || sl[i].PickupTime != "07:30" || sl[i].DriverID != "" {
				t.Errorf("slot %+v, want weekday %d 07:30 no driver", sl[i], wd)
			}
		}
		if n := len(w.rides(t, id)); n != 0 {
			t.Errorf("%d rides, want 0", n)
		}
	})

	t.Run("fresh round 1", func(t *testing.T) {
		o := w.openOffer(t, id)
		if o.Round != 1 || o.Kind != "series" {
			t.Errorf("open offer %+v, want series round 1", o)
		}
		if !o.OpenedAt.Equal(retryAt) || !o.ExpiresAt.Equal(retryAt.Add(window)) {
			t.Errorf("open offer %v..%v, want %v + 15 min", o.OpenedAt, o.ExpiresAt, retryAt)
		}
		if n := len(w.offers(t, id)); n != 4 {
			t.Errorf("%d offers in all, want 4 (3 old + 1 new)", n)
		}
	})

	t.Run("clashed driver offered again", func(t *testing.T) {
		o := w.openOffer(t, id)
		w.expectRecipients(t, o.ID, autoDrivers...)
		w.Push.Reset()
		w.Sweep(t)
		w.expectPushes(t, "Meena", pushNewRequest, 1)
	})

	t.Run("a fresh set of 3 rounds", func(t *testing.T) {
		w.closeWindow(t)
		if o := w.openOffer(t, id); o.Round != 2 {
			t.Errorf("round %d, want 2", o.Round)
		}
		w.closeWindow(t)
		if o := w.openOffer(t, id); o.Round != 3 {
			t.Errorf("round %d, want 3", o.Round)
		}
		w.closeWindow(t)
		if st := w.seriesStatus(t, id); st != "no_driver_found" {
			t.Errorf("series %s after the second set, want no_driver_found", st)
		}
		if n := len(w.offers(t, id)); n != 6 {
			t.Errorf("%d offers, want 6", n)
		}
	})
}

// R1-I13 Late interest: mark_interested 1 second after expires_at -> 409
// offer_closed.
func TestR1_I13_LateInterest(t *testing.T) {
	w := start(t)
	id, _ := w.createSeries(t, "Priya", priyaReq())
	off := w.openOffer(t, id)

	t.Run("just before expires_at is accepted", func(t *testing.T) {
		w.Advance(t, window-time.Second)
		w.markInterested(t, "Anil", off.ID)
	})

	t.Run("1 second after, before any sweep", func(t *testing.T) {
		w.Advance(t, 2*time.Second) // expires_at + 1s
		r := w.as(t, "Ravi").Do(t, http.MethodPost, pathMarkInterested(off.ID), nil)
		expectErrorWithState(t, r, http.StatusConflict, "offer_closed")
		if w.recipients(t, off.ID)[w.uid("Ravi")].Interested {
			t.Error("late interest was recorded")
		}
	})

	t.Run("withdraw after expires_at", func(t *testing.T) {
		r := w.as(t, "Anil").Do(t, http.MethodPost, pathWithdrawInterest(off.ID), nil)
		expectErrorWithState(t, r, http.StatusConflict, "offer_closed")
		if w.recipients(t, off.ID)[w.uid("Anil")].Withdrawn {
			t.Error("late withdrawal was recorded")
		}
	})

	t.Run("after the sweep closed it", func(t *testing.T) {
		w.Sweep(t)
		r := w.as(t, "Ravi").Do(t, http.MethodPost, pathMarkInterested(off.ID), nil)
		expectErrorWithState(t, r, http.StatusConflict, "offer_closed")
		if got := w.result(t, off.ID, "Anil"); got != "picked" {
			t.Errorf("Anil result %q, want picked", got)
		}
	})
}

// Rounds 2 and 3: "The same set, worked out again, minus drivers who clashed
// or withdrew in an earlier round".
func TestR1_Rounds_ClashedAndWithdrawnNotReoffered(t *testing.T) {
	w := start(t, testkit.WithShuffle("Meena"))
	id, _ := w.createSeries(t, "Priya", priyaReq())
	off1 := w.openOffer(t, id)
	w.markInterested(t, "Meena", off1.ID)
	w.markInterested(t, "Ravi", off1.ID)
	w.withdraw(t, "Ravi", off1.ID)
	w.Sweep(t)
	w.Push.Reset()
	w.closeWindow(t)

	if got := w.result(t, off1.ID, "Meena"); got != "clashed" {
		t.Errorf("Meena %q, want clashed", got)
	}
	if got := w.result(t, off1.ID, "Ravi"); got != "withdrew" {
		t.Errorf("Ravi %q, want withdrew", got)
	}
	if st := w.seriesStatus(t, id); st != "finding_driver" {
		t.Fatalf("series %s, want finding_driver", st)
	}
	off2 := w.openOffer(t, id)
	if off2.Round != 2 {
		t.Fatalf("round %d, want 2", off2.Round)
	}
	w.expectRecipients(t, off2.ID, "Anil", "Suresh")
	if o := w.offers(t, id)[0]; o.Status != "closed" || o.Outcome != "no_fit" {
		t.Errorf("round 1 %+v, want closed/no_fit", o)
	}

	w.Sweep(t)
	w.expectPushes(t, "Anil", pushStillOpen, 1)
	w.expectPushes(t, "Suresh", pushStillOpen, 1)
	w.expectPushes(t, "Meena", pushStillOpen, 0)
	w.expectPushes(t, "Ravi", pushStillOpen, 0)
}

// An empty round 1 leads to round 2, whose interest is used by the pick.
func TestR1_Rounds_PickInRound2(t *testing.T) {
	w := start(t)
	id, _ := w.createSeries(t, "Priya", priyaReq())
	w.closeWindow(t) // round 1 empty
	off2 := w.openOffer(t, id)
	w.markInterested(t, "Suresh", off2.ID)
	w.closeWindow(t)
	if st := w.seriesStatus(t, id); st != "active" {
		t.Fatalf("series %s, want active", st)
	}
	if got := w.result(t, off2.ID, "Suresh"); got != "picked" {
		t.Errorf("Suresh %q, want picked", got)
	}
	if n := len(w.offers(t, id)); n != 2 {
		t.Errorf("%d offers, want 2", n)
	}
	for _, s := range w.slots(t, id) {
		if s.DriverID != w.uid("Suresh") {
			t.Errorf("slot %+v not Suresh", s)
		}
	}
}
