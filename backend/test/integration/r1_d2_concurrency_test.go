package integration

// R1-I9, I10, I11 (concurrency) and R1-I14 (overlap backstop).

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"commute-backend/internal/testkit"
)

// R1-I9 Cancel during the pick: cancel and RunOnce in two goroutines, 50
// times -> each run ends either ended with no driver and no rides, or active
// with Ravi and the cancel 409 already_assigned. Never both.
func TestR1_I9_CancelDuringPick(t *testing.T) {
	runs := 50
	if testing.Short() {
		runs = 5
	}
	var cancelWon, pickWon int
	for i := 0; i < runs; i++ {
		t.Run(fmt.Sprintf("run%02d", i), func(t *testing.T) {
			w := start(t, testkit.WithShuffle("Ravi"))
			id, _ := w.createSeries(t, "Priya", priyaReq())
			off := w.openOffer(t, id)
			w.markInterested(t, "Ravi", off.ID)
			priya := w.as(t, "Priya")
			w.Sweep(t)
			w.Push.Reset()
			w.Advance(t, window)

			var resp *testkit.Resp
			var wg sync.WaitGroup
			startGate := make(chan struct{})
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-startGate
				w.Sweep(t)
			}()
			go func() {
				defer wg.Done()
				<-startGate
				resp = priya.Do(t, http.MethodPost, pathCancelRequest(id), nil)
			}()
			close(startGate)
			wg.Wait()

			status := w.seriesStatus(t, id)
			rides := w.rides(t, id)
			slots := w.slots(t, id)
			switch resp.Status {
			case http.StatusNoContent:
				cancelWon++
				if status != "ended" {
					t.Errorf("cancel 204 but series %s", status)
				}
				if len(rides) != 0 {
					t.Errorf("cancel 204 but %d rides", len(rides))
				}
				for _, s := range slots {
					if s.DriverID != "" {
						t.Errorf("cancel 204 but slot %+v has a driver", s)
					}
				}
				if r := w.result(t, off.ID, "Ravi"); r == "picked" {
					t.Error("cancel 204 but Ravi recorded picked")
				}
				w.Sweep(t)
				w.expectPushes(t, "Priya", pushRiderWin, 0)
				w.expectPushes(t, "Ravi", pushDriverWin, 0)
			case http.StatusConflict:
				pickWon++
				m := expectErrorWithState(t, resp, http.StatusConflict, "already_assigned")
				if b, _ := m["state"]; !strings.Contains(fmt.Sprint(b), w.uid("Ravi")) &&
					!strings.Contains(fmt.Sprint(b), "Ravi") {
					t.Errorf("already_assigned state does not name Ravi: %s", resp.Body)
				}
				if status != "active" {
					t.Errorf("cancel 409 but series %s", status)
				}
				if len(rides) != len(priyaRideDates) {
					t.Errorf("cancel 409 but %d rides, want %d", len(rides), len(priyaRideDates))
				}
				for _, s := range slots {
					if s.DriverID != w.uid("Ravi") {
						t.Errorf("cancel 409 but slot %+v not Ravi", s)
					}
				}
				for _, r := range rides {
					if r.DriverID != w.uid("Ravi") || r.Status != "assigned" {
						t.Errorf("ride %+v not assigned to Ravi", r)
					}
				}
			default:
				t.Fatalf("cancel returned %d: %s", resp.Status, resp.Body)
			}
		})
	}
	t.Logf("cancel won %d, pick won %d of %d runs", cancelWon, pickWon, runs)
}

// R1-I10 Two sweeps close one offer: RunOnce from two goroutines at close
// time -> one pick; one set of pushes.
func TestR1_I10_TwoSweepsOneOffer(t *testing.T) {
	w := start(t, testkit.WithShuffle("Ravi", "Anil"))
	id, _ := w.createSeries(t, "Priya", priyaReq())
	off := w.openOffer(t, id)
	w.markInterested(t, "Ravi", off.ID)
	w.markInterested(t, "Anil", off.ID)
	w.Sweep(t)
	w.Push.Reset()
	w.Advance(t, window)

	var wg sync.WaitGroup
	gate := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			w.Sweep(t)
		}()
	}
	close(gate)
	wg.Wait()
	w.Sweep(t) // flush the outbox; must not resend

	t.Run("one pick", func(t *testing.T) {
		if st := w.seriesStatus(t, id); st != "active" {
			t.Errorf("series %s, want active", st)
		}
		picked := 0
		for _, r := range w.recipients(t, off.ID) {
			if r.Result == "picked" {
				picked++
			}
		}
		if picked != 1 {
			t.Errorf("%d recipients picked, want 1", picked)
		}
		if got := w.result(t, off.ID, "Ravi"); got != "picked" {
			t.Errorf("Ravi result %q, want picked (first in shuffle)", got)
		}
		if got := w.result(t, off.ID, "Anil"); got != "not_picked" {
			t.Errorf("Anil result %q, want not_picked", got)
		}
		if n := len(w.offers(t, id)); n != 1 {
			t.Errorf("%d offers, want 1", n)
		}
		if n := len(w.rides(t, id)); n != len(priyaRideDates) {
			t.Errorf("%d rides, want %d (generated once)", n, len(priyaRideDates))
		}
	})

	t.Run("one set of pushes", func(t *testing.T) {
		w.expectPushes(t, "Priya", pushRiderWin, 1)
		w.expectPushes(t, "Ravi", pushDriverWin, 1)
		w.expectPushes(t, "Anil", pushNotPicked, 1)
		if n := len(w.Push.To("Priya")); n != 1 {
			t.Errorf("Priya got %d pushes, want 1", n)
		}
		if n := w.count(t, `SELECT count(*) FROM notification_outbox WHERE sent_at IS NULL`); n != 0 {
			t.Errorf("%d outbox rows unsent", n)
		}
	})

	t.Run("sweeper runs recorded", func(t *testing.T) {
		if n := w.count(t, `SELECT count(*) FROM sweeper_runs`); n < 3 {
			t.Errorf("%d sweeper_runs rows, want at least 3", n)
		}
	})
}

// R1-I11 One driver, two offers in the same minute: Ravi interested in
// Priya's and another rider's overlapping series; both close in one run ->
// Ravi wins at most one; the other records him clashed.
func TestR1_I11_OneDriverTwoOffers(t *testing.T) {
	w := start(t, testkit.WithShuffle("Ravi"))
	other := w.SignUp(t, "9876543210")

	priyaID, _ := w.createSeries(t, "Priya", priyaReq())
	r := other.Do(t, http.MethodPost, pathSeries, priyaReq())
	expectStatus(t, r, http.StatusOK, http.StatusCreated)
	otherID := seriesIDOf(t, r)

	offP := w.openOffer(t, priyaID)
	offO := w.openOffer(t, otherID)
	if !offP.ExpiresAt.Equal(offO.ExpiresAt) {
		t.Fatalf("offers expire at %v and %v, want the same minute", offP.ExpiresAt, offO.ExpiresAt)
	}
	w.markInterested(t, "Ravi", offP.ID)
	w.markInterested(t, "Ravi", offO.ID)

	w.Advance(t, window)
	w.Sweep(t)

	resP := w.result(t, offP.ID, "Ravi")
	resO := w.result(t, offO.ID, "Ravi")

	t.Run("Ravi wins exactly one", func(t *testing.T) {
		wins := 0
		for _, x := range []string{resP, resO} {
			if x == "picked" {
				wins++
			}
		}
		if wins != 1 {
			t.Fatalf("Ravi picked %d times (Priya %q, other %q), want 1", wins, resP, resO)
		}
	})

	t.Run("the other records him clashed", func(t *testing.T) {
		won, lost, lostOffer := priyaID, otherID, offO.ID
		if resO == "picked" {
			won, lost, lostOffer = otherID, priyaID, offP.ID
		}
		if got := w.result(t, lostOffer, "Ravi"); got != "clashed" {
			t.Errorf("losing offer records Ravi %q, want clashed", got)
		}
		if st := w.seriesStatus(t, won); st != "active" {
			t.Errorf("winning series %s, want active", st)
		}
		if st := w.seriesStatus(t, lost); st == "active" {
			t.Errorf("losing series is active")
		}
		if n := len(w.rides(t, lost)); n != 0 {
			t.Errorf("losing series has %d rides", n)
		}
		for _, s := range w.slots(t, lost) {
			if s.DriverID != "" {
				t.Errorf("losing slot %+v has a driver", s)
			}
		}
		// Nobody fitted the losing series: its next round opened.
		if o := w.openOffer(t, lost); o.Round != 2 {
			t.Errorf("losing series open round %d, want 2", o.Round)
		}
	})

	t.Run("no overlapping rides for Ravi", func(t *testing.T) {
		n := w.count(t, `
			SELECT count(*) FROM rides a JOIN rides b
			  ON a.driver_id = b.driver_id AND a.id < b.id
			 AND tstzrange(a.pickup_at, a.busy_until) && tstzrange(b.pickup_at, b.busy_until)
			 WHERE a.driver_id = $1
			   AND a.status::text IN ('assigned','on_the_way') AND b.status::text IN ('assigned','on_the_way')`,
			w.uid("Ravi"))
		if n != 0 {
			t.Errorf("Ravi has %d overlapping ride pairs", n)
		}
	})
}

// R1-I14 Overlap backstop: insert a second overlapping assigned ride for Ravi
// directly -> the database refuses it.
func TestR1_I14_OverlapBackstop(t *testing.T) {
	w := start(t)
	id := w.assignRavi(t)
	rides := w.rides(t, id)
	if len(rides) == 0 {
		t.Fatal("no rides after the pick")
	}
	first := rides[0] // Fri 20 Nov 08:30 (RULING: start is Fri 20 Nov)

	// Another slot of the same series, so unique (slot_id, local_date) can't
	// be what refuses the insert.
	var otherSlot string
	w.one(t, `SELECT id::text FROM series_slots WHERE series_id = $1 AND id::text <> $2 LIMIT 1`,
		[]any{id, first.SlotID}, &otherSlot)

	// Copy the ride row column by column, changing id, slot, times, status.
	var cols []string
	rows, err := w.DB.Query(ctx(), `
		SELECT column_name FROM information_schema.columns
		 WHERE table_schema = current_schema() AND table_name = 'rides'
		 ORDER BY ordinal_position`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, c)
	}
	rows.Close()
	if len(cols) == 0 {
		t.Fatal("no columns found for rides")
	}
	insertCopy := func(status string) error {
		var sel []string
		for _, c := range cols {
			switch c {
			case "id":
				sel = append(sel, "gen_random_uuid()")
			case "slot_id":
				sel = append(sel, "$2::uuid")
			case "pickup_at":
				sel = append(sel, "pickup_at + interval '5 minutes'")
			case "busy_until":
				sel = append(sel, "busy_until + interval '5 minutes'")
			case "status":
				sel = append(sel, fmt.Sprintf("'%s'", status))
			default:
				sel = append(sel, `"`+c+`"`)
			}
		}
		quoted := make([]string, len(cols))
		for i, c := range cols {
			quoted[i] = `"` + c + `"`
		}
		q := fmt.Sprintf(`INSERT INTO rides (%s) SELECT %s FROM rides WHERE id = $1`,
			strings.Join(quoted, ", "), strings.Join(sel, ", "))
		tx, err := w.DB.Begin(ctx())
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx())
		_, err = tx.Exec(ctx(), q, first.ID, otherSlot)
		return err
	}

	t.Run("control: a cancelled copy is accepted", func(t *testing.T) {
		if err := insertCopy("cancelled"); err != nil {
			t.Fatalf("cancelled overlapping copy refused: %v (the insert itself is wrong)", err)
		}
	})
	t.Run("an assigned overlapping ride is refused", func(t *testing.T) {
		err := insertCopy("assigned")
		if err == nil {
			t.Fatal("database accepted a second overlapping assigned ride for Ravi")
		}
		if code := pgCode(err); code != "23P01" {
			t.Errorf("error %v (SQLSTATE %s), want exclusion_violation 23P01", err, code)
		}
	})
	t.Run("an on_the_way overlapping ride is refused", func(t *testing.T) {
		if err := insertCopy("on_the_way"); pgCode(err) != "23P01" {
			t.Errorf("on_the_way copy: %v, want 23P01", err)
		}
	})
}
