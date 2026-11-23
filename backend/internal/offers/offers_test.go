package offers

import (
	"testing"

	"github.com/google/uuid"

	"commute-backend/internal/scheduling"
)

func dates(ws []int, start, today string) []string {
	st, _ := scheduling.ParseDate(start)
	td, _ := scheduling.ParseDate(today)
	var out []string
	for _, w := range ws {
		for _, x := range RideDatesFor(w, st, td) {
			out = append(out, scheduling.FormatDate(x))
		}
	}
	return out
}

// A win creates rides for 14 days counted from the start date.
func TestRideGenerationFrom(t *testing.T) {
	got := dates([]int{1, 3, 5}, "2026-11-20", "2026-11-19")
	want := []string{"2026-11-23", "2026-11-30", "2026-11-25", "2026-12-02", "2026-11-20", "2026-11-27"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	// Once started, the window moves with today: 14 days ahead.
	got = dates([]int{4}, "2026-11-20", "2026-12-01")
	if len(got) != 2 || got[0] != "2026-12-03" || got[1] != "2026-12-10" {
		t.Errorf("after start: %v", got)
	}
}

func TestWording(t *testing.T) {
	if got := dayList([]int{1, 3, 5}); got != "Mon, Wed and Fri" {
		t.Errorf("dayList = %q", got)
	}
	slots := []slotRow{
		{Weekday: 1, Direction: "outbound", Pickup: 510},
		{Weekday: 1, Direction: "return", Pickup: 1110},
		{Weekday: 3, Direction: "outbound", Pickup: 510},
		{Weekday: 5, Direction: "outbound", Pickup: 570},
	}
	if got := scheduleLine(slots); got != "Mon/Wed 08:30, Fri 09:30" {
		t.Errorf("scheduleLine = %q", got)
	}
	if got := timesLine(slots[:3]); got != "Mon, Wed at 08:30" {
		t.Errorf("timesLine = %q", got)
	}
	if nameAndInitial("Priya Sharma") != "Priya S." || FirstName("Ravi Kumar") != "Ravi" {
		t.Error("names")
	}
}

func TestDriverEligibility(t *testing.T) {
	today, _ := scheduling.ParseDate("2026-11-19")
	paid, lapsed := today, scheduling.AddDays(today, -1)
	dr := driverRow{ID: uuid.New(), Status: "live", VehicleType: "auto", City: "blr", SubscriptionUntil: &paid}
	if !dr.eligibleFor("auto", "blr", today) {
		t.Error("live, paid through today: eligible")
	}
	if dr.eligibleFor("bike", "blr", today) {
		t.Error("wrong vehicle type")
	}
	dr.SubscriptionUntil = &lapsed
	if dr.live(today) {
		t.Error("lapsed plan")
	}
	dr.SubscriptionUntil, dr.Status = &paid, "suspended"
	if dr.live(today) {
		t.Error("suspended")
	}
}
