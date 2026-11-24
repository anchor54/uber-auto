// Package lifecycle is the daily ride's rules (R2 + D3), as pure functions:
// which moves each state allows, when each driver action opens, the
// sweeper's due times and too-late limits, the live ETA and the PIN. No
// database and no clock of its own; the offers service and the sweeper call
// these so the rules live in one place.
//
//	assigned ─ On my way ─▶ on_the_way ─ End ride ─▶ completed
//	   │                        └─ no-show ─▶ rider_no_show
//	   ├─ give up / no On my way by pickup − 15 min ─▶ needs_driver ─ pick ─▶ assigned
//	   ├─ check only runs after pickup (outage) ─▶ failed (missed_by_outage)
//	   └─ rider skips ─▶ skipped (R3)
//	needs_driver ─ nobody in time ─▶ failed (no_driver)
//
// "I've arrived" and "Start ride" are timestamps on an on_the_way ride, not
// states.
package lifecycle

import (
	"crypto/rand"
	"fmt"
	"math"
	"math/big"
	"slices"
	"time"
)

// Ride states.
const (
	Assigned    = "assigned"
	NeedsDriver = "needs_driver"
	OnTheWay    = "on_the_way"
	Completed   = "completed"
	RiderNoShow = "rider_no_show"
	Skipped     = "skipped"
	Failed      = "failed"
	Cancelled   = "cancelled"
)

// States is every ride state.
var States = []string{Assigned, NeedsDriver, OnTheWay, Completed, RiderNoShow, Skipped, Failed, Cancelled}

// Final states never change again.
var Final = []string{Completed, RiderNoShow, Failed, Skipped, Cancelled}

// moves: the states each move starts from, and where it goes.
var moves = map[string]struct {
	from []string
	to   string
}{
	"on_my_way": {[]string{Assigned}, OnTheWay},
	"give_up":   {[]string{Assigned}, NeedsDriver},
	"check":     {[]string{Assigned}, NeedsDriver},
	"outage":    {[]string{Assigned, NeedsDriver}, Failed},
	"no_driver": {[]string{NeedsDriver}, Failed},
	"picked":    {[]string{NeedsDriver}, Assigned},
	"no_show":   {[]string{OnTheWay}, RiderNoShow},
	"complete":  {[]string{OnTheWay}, Completed},
	"autoclose": {[]string{OnTheWay}, Completed},
	"skip":      {[]string{Assigned, NeedsDriver}, Skipped},
}

// Allowed tells whether [move] may start from [state], and where it goes.
func Allowed(state, move string) (to string, ok bool) {
	m, found := moves[move]
	if !found || !slices.Contains(m.from, state) {
		return "", false
	}
	return m.to, true
}

// Timing rules.
const (
	OnMyWayOpensBefore = 60 * time.Minute // On my way allowed from pickup − 60 min
	CheckBefore        = 15 * time.Minute // the sweeper's check at pickup − 15 min
	NoShowWait         = 10 * time.Minute // after the later of arrival and pickup
	StaleAfter         = 4 * time.Hour    // close_stale_rides: pickup + 4 h
	ReminderHour       = 20               // 20:00 local, the day before
	ReminderTooLateHr  = 23               // no push after 23:00 local
	TrackOpensBefore   = 60 * time.Minute // Track ride from pickup − 60 min
	PositionStaleAfter = time.Minute      // "Last seen {n} min ago"
	Buffer             = 10 * time.Minute // busy_until = drop + 10 min
)

// OnMyWayOpensAt is when On my way becomes tappable.
func OnMyWayOpensAt(pickup time.Time) time.Time { return pickup.Add(-OnMyWayOpensBefore) }

// CheckAt is when the sweeper hands the ride over if On my way wasn't tapped.
func CheckAt(pickup time.Time) time.Time { return pickup.Add(-CheckBefore) }

// CheckIsTooLate is the check's too-late limit: run at or after pickup (an
// outage), the ride fails instead of being offered.
func CheckIsTooLate(pickup, now time.Time) bool { return !now.Before(pickup) }

// NoShowOpensAt is when "Rider not here?" opens: 10 minutes after the later
// of arrival and pickup time. Without an arrival it counts from pickup.
func NoShowOpensAt(pickup time.Time, arrivedAt *time.Time) time.Time {
	from := pickup
	if arrivedAt != nil && arrivedAt.After(from) {
		from = *arrivedAt
	}
	return from.Add(NoShowWait)
}

// StaleAt is when an on-the-way ride is closed automatically.
func StaleAt(pickup time.Time) time.Time { return pickup.Add(StaleAfter) }

// BusyUntil is the end of the driver's hold on a ride: pickup + trip + 10 min.
func BusyUntil(pickup time.Time, travelMinutes int) time.Time {
	return pickup.Add(time.Duration(travelMinutes)*time.Minute + Buffer)
}

func midnight(date time.Time, loc *time.Location) time.Time {
	return time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
}

// ReminderDueAt is 20:00 local the day before the ride's local date.
func ReminderDueAt(localDate time.Time, loc *time.Location) time.Time {
	return midnight(localDate, loc).Add(-24*time.Hour + ReminderHour*time.Hour)
}

// ReminderTooLateAt is 23:00 local the day before: after it the push is
// skipped (the marker is still set).
func ReminderTooLateAt(localDate time.Time, loc *time.Location) time.Time {
	return midnight(localDate, loc).Add(-24*time.Hour + ReminderTooLateHr*time.Hour)
}

// PaymentMarksCloseAt is midnight at the end of the ride's local date: the
// driver's payment mark can be changed until then.
func PaymentMarksCloseAt(localDate time.Time, loc *time.Location) time.Time {
	return midnight(localDate, loc).Add(24 * time.Hour)
}

// Road distance factor and minimum for the live ETA.
const EtaRoadFactor = 1.4

// EtaMinutes is the live ETA: straight-line distance × 1.4 at the vehicle
// type's speed, rounded up, at least 1 minute.
func EtaMinutes(straightKm, speedKmh float64) int {
	if speedKmh <= 0 {
		speedKmh = 18
	}
	m := math.Round(straightKm*EtaRoadFactor/speedKmh*60*1e6) / 1e6
	return max(1, int(math.Ceil(m)))
}

// HaversineKm is the straight-line distance in km.
func HaversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	const r = 6371
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat := rad(lat2 - lat1)
	dLng := rad(lng2 - lng1)
	a := math.Pow(math.Sin(dLat/2), 2) + math.Cos(rad(lat1))*math.Cos(rad(lat2))*math.Pow(math.Sin(dLng/2), 2)
	return 2 * r * math.Asin(math.Sqrt(a))
}

// NewPin is four random digits from a secure source.
func NewPin() string {
	n, err := rand.Int(rand.Reader, big.NewInt(10_000))
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%04d", n.Int64())
}

// Payment marks.
var (
	DriverPaidMarks = []string{"upi", "cash", "not_paid"}
	RiderPaidMarks  = []string{"upi", "cash"}
	GiveUpReasons   = []string{"vehicle", "personal", "other"}
	RiderTags       = []string{"on_time", "smooth_driving", "clean_vehicle", "good_route"}
	DriverFlags     = []string{"all_good", "kept_me_waiting", "pickup_unclear"}
)

// PaymentStatus is worked out from the two marks and never stored: paid if
// either side says so, unless the driver says not paid yet.
func PaymentStatus(rider, driver string) string {
	switch {
	case driver == "not_paid" && rider != "":
		return "disputed"
	case driver == "not_paid":
		return "unpaid"
	case driver != "":
		return "paid"
	case rider != "":
		return "paid_by_rider"
	}
	return "unmarked"
}
