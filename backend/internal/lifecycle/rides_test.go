package lifecycle

import (
	"regexp"
	"testing"
	"time"
)

var ist = time.FixedZone("IST", 330*60)

func istAt(day, h, m int) time.Time { return time.Date(2026, 11, day, h, m, 0, 0, ist) }

func TestAllowedMoves(t *testing.T) {
	cases := []struct {
		state, move, to string
		ok              bool
	}{
		{Assigned, "on_my_way", OnTheWay, true},
		{NeedsDriver, "on_my_way", "", false},
		{Assigned, "give_up", NeedsDriver, true},
		{OnTheWay, "give_up", "", false},
		{OnTheWay, "complete", Completed, true},
		{Assigned, "complete", "", false},
		{OnTheWay, "no_show", RiderNoShow, true},
		{Completed, "no_show", "", false},
		{Assigned, "outage", Failed, true},
		{NeedsDriver, "picked", Assigned, true},
		{Assigned, "nonsense", "", false},
	}
	for _, c := range cases {
		to, ok := Allowed(c.state, c.move)
		if to != c.to || ok != c.ok {
			t.Errorf("%s from %s: got %q %v, want %q %v", c.move, c.state, to, ok, c.to, c.ok)
		}
	}
}

func TestNoShowOpening(t *testing.T) {
	pickup := istAt(18, 8, 30)
	late := istAt(18, 8, 33)
	if got := NoShowOpensAt(pickup, &late); !got.Equal(istAt(18, 8, 43)) {
		t.Errorf("late arrival: %v", got)
	}
	early := istAt(18, 8, 20)
	if got := NoShowOpensAt(pickup, &early); !got.Equal(istAt(18, 8, 40)) {
		t.Errorf("early arrival: %v", got)
	}
	if got := NoShowOpensAt(pickup, nil); !got.Equal(istAt(18, 8, 40)) {
		t.Errorf("no arrival: %v", got)
	}
}

func TestOnMyWayAndCheckTimes(t *testing.T) {
	pickup := istAt(18, 8, 30)
	if !OnMyWayOpensAt(pickup).Equal(istAt(18, 7, 30)) {
		t.Error("on my way opens at 07:30")
	}
	if !CheckAt(pickup).Equal(istAt(18, 8, 15)) {
		t.Error("check at 08:15")
	}
	if CheckIsTooLate(pickup, istAt(18, 8, 29)) || !CheckIsTooLate(pickup, istAt(18, 8, 30)) {
		t.Error("too late from pickup on")
	}
	if !StaleAt(pickup).Equal(istAt(18, 12, 30)) {
		t.Error("stale at 12:30")
	}
}

func TestReminderTimesAcrossMidnight(t *testing.T) {
	// Ride on Wed 18 Nov: reminder Tue 17 Nov 20:00, too late from 23:00.
	date := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	if got := ReminderDueAt(date, ist); !got.Equal(istAt(17, 20, 0)) {
		t.Errorf("due %v", got)
	}
	if got := ReminderTooLateAt(date, ist); !got.Equal(istAt(17, 23, 0)) {
		t.Errorf("too late %v", got)
	}
	// A ride on 1 Dec: reminder on 30 Nov.
	dec := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	if got := ReminderDueAt(dec, ist); !got.Equal(istAt(30, 20, 0)) {
		t.Errorf("month boundary %v", got)
	}
	if got := PaymentMarksCloseAt(date, ist); !got.Equal(istAt(19, 0, 0)) {
		t.Errorf("marks close %v", got)
	}
}

func TestEta(t *testing.T) {
	if got := EtaMinutes(0, 18); got != 1 {
		t.Errorf("zero distance: %d", got)
	}
	// 3 km straight → 4.2 km road at 18 km/h = 14 min.
	if got := EtaMinutes(3, 18); got != 14 {
		t.Errorf("3 km: %d", got)
	}
	km := HaversineKm(12.9352, 77.6245, 12.9698, 77.7178)
	if km < 10.7 || km > 10.9 {
		t.Errorf("Koramangala → Whitefield %.2f km", km)
	}
	if got := EtaMinutes(km, 18); got != 51 {
		t.Errorf("Koramangala → Whitefield ETA %d", got)
	}
}

func TestPin(t *testing.T) {
	re := regexp.MustCompile(`^[0-9]{4}$`)
	seen := map[string]bool{}
	for range 200 {
		p := NewPin()
		if !re.MatchString(p) {
			t.Fatalf("bad pin %q", p)
		}
		seen[p] = true
	}
	if len(seen) < 100 {
		t.Errorf("pins not random enough: %d distinct of 200", len(seen))
	}
}

func TestBusyUntil(t *testing.T) {
	if got := BusyUntil(istAt(18, 8, 30), 51); !got.Equal(istAt(18, 9, 31)) {
		t.Errorf("busy until %v", got)
	}
}

func TestPaymentStatus(t *testing.T) {
	cases := [][3]string{{"", "", "unmarked"}, {"upi", "", "paid_by_rider"}, {"", "cash", "paid"}, {"", "not_paid", "unpaid"}, {"upi", "not_paid", "disputed"}}
	for _, c := range cases {
		if got := PaymentStatus(c[0], c[1]); got != c[2] {
			t.Errorf("%v: %s", c, got)
		}
	}
}
