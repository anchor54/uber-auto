package domain

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// Flow D1: the pure rules behind driver registration and the subscription.

// ---- amount due now ----

// Worked out here independently of the code: the monthly price for the days
// left in the month (today included), rounded up to the rupee.
func TestAmountDueNowEveryDayOfShortNormalAndLongMonths(t *testing.T) {
	for _, month := range []time.Time{
		time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),  // 28 days
		time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), // 30 days
		time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), // 31 days
	} {
		last := month.AddDate(0, 1, -1)
		days := last.Day()
		for _, price := range []int{499, 799, 1499, 1999} {
			for day := 1; day <= days; day++ {
				today := month.AddDate(0, 0, day-1).Format("2006-01-02")
				left := days - day + 1
				want := (price*left + days - 1) / days
				amount, start, end := AmountDueNow(price, today)
				eq(t, []any{amount, start, end}, []any{want, today, last.Format("2006-01-02")}, price, " on ", today)
			}
		}
	}
}

func TestAmountDueNowExamples(t *testing.T) {
	// Ravi, auto, signs up Sat 21 Nov: 10 of 30 days of ₹799 is 266.33, rounded up.
	amount, start, end := AmountDueNow(799, "2026-11-21")
	eq(t, []any{amount, start, end}, []any{267, "2026-11-21", "2026-11-30"})
	// The last day of the month is one day's worth.
	amount, start, end = AmountDueNow(799, "2026-11-30")
	eq(t, []any{amount, start, end}, []any{27, "2026-11-30", "2026-11-30"})
	// The first is the whole month.
	amount, _, end = AmountDueNow(799, "2026-12-01")
	eq(t, []any{amount, end}, []any{799, "2026-12-31"})
	// A leap February has 29 days.
	_, _, end = AmountDueNow(499, "2028-02-10")
	eq(t, end, "2028-02-29")
}

// ---- reference ----

func TestNewReferenceIsDRVAndFiveDigits(t *testing.T) {
	format := regexp.MustCompile(`^DRV-[0-9]{5}$`)
	seen := map[string]bool{}
	for range 2000 {
		ref := NewReference()
		if !format.MatchString(ref) {
			t.Fatalf("reference %q", ref)
		}
		seen[ref] = true
	}
	// 2000 draws from ~100,000 values collide about 20 times by chance; the
	// database's unique index catches those. A constant or narrow generator
	// collides far more.
	if len(seen) < 1950 {
		t.Fatalf("only %d distinct references in 2000", len(seen))
	}
}

// ---- completeness ----

var d1Kinds = []string{"profile_photo", "id_proof", "licence", "rc", "insurance", "permit", "upi_id",
	"vehicle_front", "vehicle_back", "vehicle_interior"}

func TestItemKindsInOrder(t *testing.T) {
	eq(t, DriverItemKinds, d1Kinds)
	files := []string{}
	for _, k := range d1Kinds {
		if k != "upi_id" {
			files = append(files, k)
		}
	}
	eq(t, FileItemKinds, files)
}

func TestMissingForSubmit(t *testing.T) {
	// Nothing done: every item, then the vehicle, then the day off.
	eq(t, MissingForSubmit(map[string]string{}, false, false), append(append([]string{}, d1Kinds...), "vehicle", "day_off"))

	all := map[string]string{}
	for _, k := range d1Kinds {
		all[k] = "uploaded"
	}
	if got := MissingForSubmit(all, true, true); len(got) != 0 {
		t.Fatalf("complete profile misses %v", got)
	}

	// Verified items count as present (resubmitting after changes).
	all["licence"] = "verified"
	if got := MissingForSubmit(all, true, true); len(got) != 0 {
		t.Fatalf("verified item counted missing: %v", got)
	}

	// Two documents left out, kept in the order of the list.
	some := map[string]string{}
	for k, v := range all {
		some[k] = v
	}
	delete(some, "permit")
	delete(some, "insurance")
	eq(t, MissingForSubmit(some, true, true), []string{"insurance", "permit"})
	eq(t, MissingForSubmit(some, false, true), []string{"insurance", "permit", "vehicle"})
	eq(t, MissingForSubmit(all, true, false), []string{"day_off"})
	delete(some, "vehicle_back")
	eq(t, MissingForSubmit(some, false, false), []string{"insurance", "permit", "vehicle_back", "vehicle", "day_off"})
}

// ---- UPI ----

func TestValidUPI(t *testing.T) {
	for _, ok := range []string{"ravi.kumar@okbank", "ab@ybl", "ravi-k_1@okaxis", "9876500001@paytm", "R.K@OkHdfc",
		strings.Repeat("a", 256) + "@ybl", "ravi@o" + strings.Repeat("k", 63)} {
		if !ValidUPI(ok) {
			t.Fatalf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "ravi", "@okbank", "a@okbank", "ravi@", "ravi@1bank", "ravi@o",
		"ravi kumar@okbank", "ravi@ok.bank", "ravi@@okbank", "ravi@ok_bank", "ravi+1@okbank",
		strings.Repeat("a", 257) + "@ybl", "ravi@o" + strings.Repeat("k", 64)} {
		if ValidUPI(bad) {
			t.Fatalf("%q accepted", bad)
		}
	}
}

// ---- number plates ----

func TestNormalizePlate(t *testing.T) {
	for in, want := range map[string]string{
		"KA 05 AB 1234":         "KA 05 AB 1234",
		"ka 05 ab 1234":         "KA 05 AB 1234",
		"  Ka  05   ab 1234   ": "KA 05 AB 1234",
		"ka05ab1234":            "KA05AB1234",
	} {
		eq(t, NormalizePlate(in), want, in)
	}
}

func TestPlateKeyIgnoresCaseSpacesAndHyphens(t *testing.T) {
	for _, in := range []string{"KA 05 AB 1234", "ka05ab1234", "KA-05-AB-1234", " ka 05-ab  1234 "} {
		eq(t, PlateKey(in), "KA05AB1234", in)
	}
	if PlateKey("KA 05 AB 1234") == PlateKey("KA 05 AB 1243") {
		t.Fatal("different plates share a key")
	}
}
