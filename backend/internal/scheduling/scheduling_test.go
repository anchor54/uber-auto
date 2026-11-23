package scheduling

import (
	"testing"
	"time"

	"commute-backend/internal/travel"
)

var ist = time.FixedZone("IST", 330*60)

func date(s string) time.Time {
	d, err := ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

func TestParseAndFormatClock(t *testing.T) {
	for in, want := range map[string]int{"08:30": 510, "8:30": 510, "8:40 AM": 520, "6:30 PM": 1110, "12:05 AM": 5, "18:30:00": 1110} {
		got, err := ParseClock(in)
		if err != nil || got != want {
			t.Errorf("ParseClock(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "25:00", "8", "08:60", "13:00 PM"} {
		if _, err := ParseClock(bad); err == nil {
			t.Errorf("ParseClock(%q): want an error", bad)
		}
	}
	if got := FormatClock(510 + 51); got != "09:21" {
		t.Errorf("FormatClock = %s", got)
	}
}

func TestParseWeekday(t *testing.T) {
	for in, want := range map[string]int{"1": 1, "MON": 1, "wed": 3, "Sunday": 7} {
		if got, err := ParseWeekday(in); err != nil || got != want {
			t.Errorf("ParseWeekday(%q) = %d, %v", in, got, err)
		}
	}
	if _, err := ParseWeekday("8"); err == nil {
		t.Error("8 is not a weekday")
	}
}

func TestArriveByRoundsDownToFive(t *testing.T) {
	// 09:30 − 51 − 10 = 08:29 → 08:25.
	if p, ok := PickupForArrival(570, 51); !ok || FormatClock(p) != "08:25" {
		t.Errorf("got %s %v", FormatClock(p), ok)
	}
	// Exactly on a 5: 09:30 − 50 − 10 = 08:30.
	if p, _ := PickupForArrival(570, 50); FormatClock(p) != "08:30" {
		t.Errorf("got %s", FormatClock(p))
	}
	if _, ok := PickupForArrival(30, 51); ok {
		t.Error("a pickup before midnight should be refused")
	}
}

func TestStartDateIsFirstChosenWeekdayFromTomorrow(t *testing.T) {
	thu := date("2026-11-19")
	cases := []struct {
		days []int
		want string
	}{
		{[]int{1, 3, 5}, "2026-11-20"}, // Fri, tomorrow
		{[]int{1, 3}, "2026-11-23"},    // Mon
		{[]int{4}, "2026-11-26"},       // Thursday, but never today
		{[]int{6}, "2026-11-21"},       // weekly: Saturdays only
	}
	for _, c := range cases {
		if got := FormatDate(StartDate(thu, c.days, nil)); got != c.want {
			t.Errorf("StartDate(%v) = %s, want %s", c.days, got, c.want)
		}
	}
	later := date("2026-12-01")
	if got := FormatDate(StartDate(thu, []int{1, 3, 5}, &later)); got != "2026-12-02" {
		t.Errorf("with an earliest date: %s", got)
	}
}

func TestDateOfIsLocal(t *testing.T) {
	// 19:00 UTC on the 19th is 00:30 IST on the 20th.
	got := DateOf(time.Date(2026, 11, 19, 19, 0, 0, 0, time.UTC), ist)
	if FormatDate(got) != "2026-11-20" {
		t.Errorf("got %s", FormatDate(got))
	}
	p := LocalInstant(date("2026-11-20"), 510, ist)
	if !p.Equal(time.Date(2026, 11, 20, 8, 30, 0, 0, ist)) {
		t.Errorf("LocalInstant = %v", p)
	}
}

func TestRideDates(t *testing.T) {
	got := RideDates(3, date("2026-11-20"), date("2026-12-03"))
	if len(got) != 2 || FormatDate(got[0]) != "2026-11-25" || FormatDate(got[1]) != "2026-12-02" {
		t.Errorf("Wednesdays: %v", got)
	}
}

// The clash check, with the spec's example: Priya's 08:30 Koramangala →
// Whitefield (51 min) fits Ravi's 09:50 pickup 14 minutes' drive from her
// drop-off with 5 minutes to spare; at 09:30 it doesn't.
func TestClashLeavesTravelTimePlusTenMinutes(t *testing.T) {
	kor := travel.Point{Lat: 12.9352, Lng: 77.6245}
	wf := travel.Point{Lat: 12.9590, Lng: 77.7210}
	hopeFarm := travel.Point{Lat: 12.9780, Lng: 77.7380}
	tf := func(a, b travel.Point) int { return travel.MinutesBetween(a, b, 18) }
	if m := tf(kor, wf); m != 51 {
		t.Fatalf("Koramangala → Whitefield %d min, want 51", m)
	}
	if m := tf(wf, hopeFarm); m != 14 {
		t.Fatalf("Whitefield → Hope Farm %d min, want 14", m)
	}
	day := date("2026-11-23")
	at := func(hhmm string, mins int, from, to travel.Point) Trip {
		m, _ := ParseClock(hhmm)
		p := LocalInstant(day, m, ist)
		return Trip{Pickup: p, Drop: p.Add(time.Duration(mins) * time.Minute), From: from, To: to}
	}
	ankit := at("09:50", 50, hopeFarm, kor)
	// 09:21 drop + 14 travel + 10 = 09:45 ≤ 09:50.
	if Clash(at("08:30", 51, kor, wf), ankit, tf) {
		t.Error("08:30 should fit with 5 minutes to spare")
	}
	if !Clash(at("08:36", 51, kor, wf), ankit, tf) {
		t.Error("08:36 leaves 9 minutes, not 10: clash")
	}
	if !Clash(at("09:30", 51, kor, wf), ankit, tf) {
		t.Error("09:30 overlaps Ankit's ride: clash")
	}
	// Order doesn't matter.
	if !Clash(ankit, at("09:30", 51, kor, wf), tf) {
		t.Error("Clash must be symmetric")
	}
	// After Ankit's drop-off (10:40 at Koramangala) + 0 travel + 10 = 10:50.
	if Clash(ankit, at("10:50", 51, kor, wf), tf) {
		t.Error("10:50 from Koramangala should fit")
	}
	if !Clash(ankit, at("10:45", 51, kor, wf), tf) {
		t.Error("10:45 should clash")
	}
}

func TestFirstClash(t *testing.T) {
	p := travel.Point{Lat: 12.9, Lng: 77.6}
	tf := func(a, b travel.Point) int { return 0 }
	base := time.Date(2026, 11, 23, 3, 0, 0, 0, time.UTC)
	trip := func(min, dur int) Trip {
		s := base.Add(time.Duration(min) * time.Minute)
		return Trip{Pickup: s, Drop: s.Add(time.Duration(dur) * time.Minute), From: p, To: p}
	}
	existing := []Trip{trip(0, 30), trip(24*60, 30)}
	if _, _, ok := FirstClash(existing, []Trip{trip(40, 30), trip(24*60+40, 30)}, tf); ok {
		t.Error("10-minute gaps fit")
	}
	if _, _, ok := FirstClash(existing, []Trip{trip(40, 30), trip(24*60+35, 30)}, tf); !ok {
		t.Error("a 5-minute gap on day 2 clashes")
	}
	// Two candidate trips (outbound and return) that overlap each other.
	if _, _, ok := FirstClash(nil, []Trip{trip(100, 60), trip(150, 30)}, tf); !ok {
		t.Error("the series' own trips must not overlap")
	}
}
