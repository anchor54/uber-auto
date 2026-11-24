package drivers

import (
	"testing"
	"time"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// Amount due now on every day of a 28-, 30- and 31-day month.
func TestAmountDueEveryDay(t *testing.T) {
	for _, mon := range []struct {
		y    int
		m    time.Month
		days int
	}{{2026, time.February, 28}, {2026, time.November, 30}, {2026, time.December, 31}, {2028, time.February, 29}} {
		for _, price := range []int{1199, 999, 1, 3000} {
			for d := 1; d <= mon.days; d++ {
				left := mon.days - d + 1
				want := (price*left + mon.days - 1) / mon.days
				got := AmountDue(price, day(mon.y, mon.m, d))
				if got != want {
					t.Fatalf("%d-%02d-%02d price %d: %d, want %d", mon.y, mon.m, d, price, got, want)
				}
				// Rounded up: never less than the exact share, less than a rupee over.
				exact := float64(price) * float64(left) / float64(mon.days)
				if float64(got) < exact || float64(got)-exact >= 1 {
					t.Fatalf("%d-%02d-%02d: %d is not %v rounded up", mon.y, mon.m, d, got, exact)
				}
			}
			if got := AmountDue(price, day(mon.y, mon.m, 1)); got != price {
				t.Errorf("first of the month: %d, want the full %d", got, price)
			}
		}
	}
	// The spec's examples: 21 Nov = 10 of 30 days; 30 Nov = 1 day.
	if got := AmountDue(1199, day(2026, 11, 21)); got != 400 {
		t.Errorf("21 Nov: %d, want 400", got)
	}
	if got := AmountDue(1199, day(2026, 11, 30)); got != 40 {
		t.Errorf("30 Nov: %d, want 40", got)
	}
}

func TestPeriod(t *testing.T) {
	s, e := Period(day(2026, 11, 21))
	if s != day(2026, 11, 21) || e != day(2026, 11, 30) {
		t.Errorf("period %v..%v", s, e)
	}
	s, e = Period(day(2026, 12, 31))
	if s != e || e != day(2026, 12, 31) {
		t.Errorf("last day: %v..%v", s, e)
	}
	if _, e = Period(day(2028, 2, 3)); e != day(2028, 2, 29) {
		t.Errorf("leap February ends %v", e)
	}
}

func TestReference(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 2000; i++ {
		r := NewReference()
		if !ValidReference(r) {
			t.Fatalf("%q is not DRV + 5 digits", r)
		}
		seen[r] = true
	}
	// 2000 draws from 90000: nearly all distinct (the database enforces uniqueness).
	if len(seen) < 1900 {
		t.Errorf("only %d distinct references in 2000", len(seen))
	}
	for _, bad := range []string{"DRV-1234", "DRV-123456", "drv-12345", "DRV12345"} {
		if ValidReference(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestMissing(t *testing.T) {
	all := map[string]string{}
	for _, k := range ReviewKinds {
		all[k] = StateUploaded
	}
	if m := Missing(all); len(m) != 0 {
		t.Errorf("complete: missing %v", m)
	}
	delete(all, KindInsurance)
	all[KindPermit] = StateMissing
	all[KindLicence] = StateRejected
	m := Missing(all)
	want := []string{KindLicence, KindInsurance, KindPermit}
	if len(m) != len(want) {
		t.Fatalf("missing %v, want %v", m, want)
	}
	for i := range want {
		if m[i] != want[i] {
			t.Errorf("missing %v, want %v", m, want)
		}
	}
	if m := Missing(map[string]string{}); len(m) != len(ReviewKinds) {
		t.Errorf("empty: %d missing, want %d", len(m), len(ReviewKinds))
	}
	// Verified counts as present; AllVerified needs every item.
	for _, k := range ReviewKinds {
		all[k] = StateVerified
	}
	if !AllVerified(all) {
		t.Error("all verified not seen")
	}
	all[KindVehicleInterior] = StateUploaded
	if AllVerified(all) || len(Unverified(all)) != 1 {
		t.Errorf("unverified %v", Unverified(all))
	}
}

func TestUPI(t *testing.T) {
	for _, ok := range []string{"ravi@okaxis", "ravi.kumar-1@ybl", "9876543210@paytm", "r_k@oksbi"} {
		if !ValidUPI(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "ravi", "@okaxis", "ravi@", "ravi@@okaxis", "ravi kumar@okaxis", "ravi@ok axis", "ravi@1bank"} {
		if ValidUPI(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestPlates(t *testing.T) {
	cases := map[string]string{
		"ka 05 ab 1234":     "KA 05 AB 1234",
		"  KA  05 ab 1234 ": "KA 05 AB 1234",
		"ka05ab1234":        "KA05AB1234",
		"ka-05-ab-1234":     "KA-05-AB-1234",
	}
	for in, want := range cases {
		if got := NormalizePlate(in); got != want {
			t.Errorf("NormalizePlate(%q) = %q, want %q", in, got, want)
		}
	}
	if PlateKey("ka 05 ab 1234") != PlateKey("KA05AB1234") || PlateKey("KA-05-AB-1234") != "KA05AB1234" {
		t.Error("plate keys differ for the same plate")
	}
	if ValidPlate("KA") || ValidPlate("") || !ValidPlate("KA 05 AB 1234") {
		t.Error("ValidPlate")
	}
}

func TestEditable(t *testing.T) {
	cases := []struct {
		status, state string
		want          bool
	}{
		{StatusSigningUp, StateMissing, true},
		{StatusSigningUp, StateUploaded, true},
		{StatusSubmitted, StateUploaded, false},
		{StatusChangesNeeded, StateRejected, true},
		{StatusChangesNeeded, StateUploaded, true}, // replaced since, before resubmitting
		{StatusChangesNeeded, StateVerified, false},
		{StatusApproved, StateVerified, false},
		{StatusLive, StateVerified, false},
		{StatusRejected, StateRejected, false},
	}
	for _, c := range cases {
		if got := editable(c.status, c.state); got != c.want {
			t.Errorf("editable(%s, %s) = %v, want %v", c.status, c.state, got, c.want)
		}
	}
}

func TestKindsAndWeekdays(t *testing.T) {
	if NormalizeKind("upi_id") != KindUPI || NormalizeKind("Driving-Licence") != KindLicence || NormalizeKind("vehicle-front") != KindVehicleFront {
		t.Error("NormalizeKind")
	}
	if len(DocumentKinds) != 7 || len(FileKinds) != 9 || len(ReviewKinds) != 11 {
		t.Error("kind lists")
	}
	for in, want := range map[string]int{"3": 3, "WED": 3, "wednesday": 3, "Sun": 7} {
		if got, ok := ParseWeekday(in); !ok || got != want {
			t.Errorf("ParseWeekday(%q) = %d", in, got)
		}
	}
	if _, ok := ParseWeekday("8"); ok {
		t.Error("8 accepted")
	}
	if RideType("cab", "sedan") != "cab_sedan" || RideType("auto", "") != "auto" {
		t.Error("RideType")
	}
}
