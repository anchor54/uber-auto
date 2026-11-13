package domain

// Flow R2 ride lifecycle, as pure functions: no database, no clock of its own.
//
//	scheduled → on_the_way → arrived → in_ride → completed
//	exits: no_show (from arrived), skipped (from scheduled),
//	       cancelled (from scheduled or on_the_way); stale rides are closed.
//
// The endpoints, the worker and the simulator all go through Transition,
// so the rules live in one place. Anything it refuses is an *ApiError.

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"
)

var RideStates = []string{"scheduled", "on_the_way", "arrived", "in_ride", "completed", "no_show", "skipped", "cancelled"}

var Moves = []string{"depart", "arrive", "start", "end", "no_show", "skip", "cancel", "close_stale"}

// OpenStates are the states of a ride under way.
var OpenStates = []string{"on_the_way", "arrived", "in_ride"}

// RideFacts are the fields of a ride the rules read.
type RideFacts struct {
	State          string
	PickupAt       time.Time
	WindowStart    time.Time
	WindowEnd      time.Time
	ArrivedAt      *time.Time
	WaitEndsAt     *time.Time
	RiderLateUsed  bool
	Pin            string
	PinTries       int
	LastActivityAt time.Time
}

// MoveContext is what the caller looked up so the rules can stay pure.
type MoveContext struct {
	Now   time.Time
	Actor string // rider | driver | system | ops
	// depart: the driver is on duty.
	DriverOnDuty bool
	// depart: the driver already has another ride on the way, arrived or in ride.
	DriverBusy bool
	// arrive: metres from the driver's last position to the pickup; nil if none sent.
	MetresToPickup *float64
	// start: the PIN the driver typed.
	Pin string
	// end: metres from where the driver ended to the drop-off; nil if unknown.
	MetresToDrop *float64
	// end: why the driver ended away from the drop-off.
	EndReason string
	// no_show: the driver called or alerted the rider at least once.
	ContactLogged bool
}

// MoveSet is the columns a move changes; nil fields are left alone.
type MoveSet struct {
	DepartedAt *time.Time
	ArrivedAt  *time.Time
	StartedAt  *time.Time
	EndedAt    *time.Time
	ClosedAt   *time.Time
	WaitEndsAt *time.Time
	EndReason  *string
}

// Columns lists the set columns and values, in a fixed order.
func (s MoveSet) Columns() ([]string, []any) {
	var cols []string
	var vals []any
	add := func(col string, t *time.Time) {
		if t != nil {
			cols = append(cols, col)
			vals = append(vals, *t)
		}
	}
	add("departed_at", s.DepartedAt)
	add("arrived_at", s.ArrivedAt)
	add("started_at", s.StartedAt)
	add("ended_at", s.EndedAt)
	add("closed_at", s.ClosedAt)
	add("wait_ends_at", s.WaitEndsAt)
	if s.EndReason != nil {
		cols = append(cols, "end_reason")
		vals = append(vals, *s.EndReason)
	}
	return cols, vals
}

// MoveResult is the changes a move makes. The caller writes them and a
// ride_events row.
type MoveResult struct {
	To  string
	Set MoveSet
}

const (
	PinTries               = 3
	WindowMinutes          = 5
	WaitMinutes            = 5
	RiderLateExtraMinutes  = 5
	DepartBeforeMinutes    = 90
	ArriveWithinMetres     = 300
	EndWithinMetres        = 500
	StaleAfterHours        = 3
	LateAfterWindowMinutes = 5 // ETA this far past the window's end flags the driver as late
)

var allowedActors = map[string][]string{
	"depart":      {"driver"},
	"arrive":      {"driver"},
	"start":       {"driver"},
	"end":         {"driver"},
	"no_show":     {"driver"},
	"skip":        {"rider", "system"},
	"cancel":      {"ops", "system"},
	"close_stale": {"system"},
}

var fromStates = map[string][]string{
	"depart":      {"scheduled"},
	"arrive":      {"on_the_way"},
	"start":       {"arrived"},
	"end":         {"in_ride"},
	"no_show":     {"arrived"},
	"skip":        {"scheduled"},
	"cancel":      {"scheduled", "on_the_way"},
	"close_stale": {"on_the_way", "arrived", "in_ride"},
}

func InvalidTransition(state, move string) *ApiError {
	return NewApiError(409, "invalid_transition",
		fmt.Sprintf("Can't %s a ride that is %s", strings.Replace(move, "_", " ", 1), strings.ReplaceAll(state, "_", " ")))
}

// WaitEndsAt is the end of the rider's wait: 5 minutes after the later of
// arrival and the window opening (an early driver doesn't start it early),
// 10 if the rider said they're running late.
func WaitEndsAt(arrivedAt, windowStart time.Time, riderLateUsed bool) time.Time {
	from := arrivedAt
	if windowStart.After(from) {
		from = windowStart
	}
	m := WaitMinutes
	if riderLateUsed {
		m += RiderLateExtraMinutes
	}
	return from.Add(time.Duration(m) * time.Minute)
}

// WrongPin is the error for a wrong PIN; the caller records the try first.
func WrongPin(triesLeft int) *ApiError {
	if triesLeft > 0 {
		word := "tries"
		if triesLeft == 1 {
			word = "try"
		}
		return NewApiError(422, "wrong_pin", fmt.Sprintf("That PIN isn't right. %d %s left.", triesLeft, word))
	}
	return NewApiError(423, "pin_locked", "Too many wrong PINs. Call support to start this ride.")
}

// IsWrongPin tells whether [err] came from a wrong or locked PIN.
func IsWrongPin(err error) bool {
	e, ok := err.(*ApiError)
	return ok && (e.Code == "wrong_pin" || e.Code == "pin_locked")
}

// Transition applies [move] to [ride], or returns why it isn't allowed.
func Transition(ride RideFacts, move string, c MoveContext) (MoveResult, error) {
	if !slices.Contains(allowedActors[move], c.Actor) {
		return MoveResult{}, NewApiError(403, "not_allowed", fmt.Sprintf("A %s can't %s a ride", c.Actor, strings.Replace(move, "_", " ", 1)))
	}
	if !slices.Contains(fromStates[move], ride.State) {
		return MoveResult{}, InvalidTransition(ride.State, move)
	}
	now := c.Now
	switch move {
	case "depart":
		if !c.DriverOnDuty {
			return MoveResult{}, NewApiError(409, "off_duty", "Go on duty before starting navigation")
		}
		if c.DriverBusy {
			return MoveResult{}, NewApiError(409, "driver_busy", "Finish your current ride first")
		}
		if now.Before(ride.PickupAt.Add(-DepartBeforeMinutes * time.Minute)) {
			return MoveResult{}, NewApiError(409, "too_early", fmt.Sprintf("You can start navigation %d minutes before pickup", DepartBeforeMinutes))
		}
		return MoveResult{To: "on_the_way", Set: MoveSet{DepartedAt: &now}}, nil
	case "arrive":
		if c.MetresToPickup == nil || *c.MetresToPickup > ArriveWithinMetres {
			return MoveResult{}, NewApiError(422, "too_far", fmt.Sprintf("Mark arrived within %d m of the pickup point", ArriveWithinMetres))
		}
		wait := WaitEndsAt(now, ride.WindowStart, ride.RiderLateUsed)
		return MoveResult{To: "arrived", Set: MoveSet{ArrivedAt: &now, WaitEndsAt: &wait}}, nil
	case "start":
		if ride.PinTries >= PinTries {
			return MoveResult{}, WrongPin(0)
		}
		if c.Pin != ride.Pin {
			return MoveResult{}, WrongPin(PinTries - ride.PinTries - 1)
		}
		return MoveResult{To: "in_ride", Set: MoveSet{StartedAt: &now}}, nil
	case "end":
		far := c.MetresToDrop == nil || *c.MetresToDrop > EndWithinMetres
		reason := strings.TrimSpace(c.EndReason)
		if far && reason == "" {
			return MoveResult{}, NewApiError(422, "too_far", fmt.Sprintf("You're more than %d m from the drop-off. Say why you're ending here.", EndWithinMetres))
		}
		set := MoveSet{EndedAt: &now}
		if far {
			set.EndReason = &reason
		}
		return MoveResult{To: "completed", Set: set}, nil
	case "no_show":
		if ride.WaitEndsAt == nil || now.Before(*ride.WaitEndsAt) {
			return MoveResult{}, NewApiError(425, "wait_not_over", "The rider still has time. Wait until the countdown ends.")
		}
		if !c.ContactLogged {
			return MoveResult{}, NewApiError(409, "no_contact_logged", "Call or alert the rider before marking a no-show")
		}
		return MoveResult{To: "no_show", Set: MoveSet{ClosedAt: &now}}, nil
	case "skip":
		return MoveResult{To: "skipped", Set: MoveSet{ClosedAt: &now}}, nil
	case "cancel":
		return MoveResult{To: "cancelled", Set: MoveSet{ClosedAt: &now}}, nil
	case "close_stale":
		if now.Sub(ride.LastActivityAt) < StaleAfterHours*time.Hour {
			return MoveResult{}, NewApiError(409, "not_stale", "The ride is still active")
		}
		// A ride that started is assumed done; one that never started didn't happen.
		if ride.State == "in_ride" {
			reason := "closed_stale"
			return MoveResult{To: "completed", Set: MoveSet{EndedAt: &now, ClosedAt: &now, EndReason: &reason}}, nil
		}
		return MoveResult{To: "cancelled", Set: MoveSet{ClosedAt: &now}}, nil
	}
	return MoveResult{}, fmt.Errorf("unknown move %q", move)
}

// ---- payment status, worked out from the two marks and never stored ----

// PaymentStatus is paid if either side says so, unless the driver says not
// paid yet. Empty strings mean unmarked.
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

// ---- ETA and lateness ----

// Straight line × 1.85 at 22 km/h, for live ETAs without a road route.
const (
	EtaRoadFactor = 1.85
	EtaKmh        = 22
)

func EtaMinutes(straightLineKm float64) int {
	return max(1, int(JSRound(straightLineKm*EtaRoadFactor/EtaKmh*60)))
}

// IsLate is true when now + ETA lands more than 5 minutes after the window ends.
func IsLate(now time.Time, etaMins int, windowEnd time.Time) bool {
	return now.Add(time.Duration(etaMins) * time.Minute).After(windowEnd.Add(LateAfterWindowMinutes * time.Minute))
}

// NewPin is four random digits from a secure source.
func NewPin() string {
	n, err := rand.Int(rand.Reader, big.NewInt(10_000))
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%04d", n.Int64())
}

// MetresBetween is the straight-line distance in metres.
func MetresBetween(a, b LatLng) float64 {
	return HaversineKm(a.Lat, a.Lng, b.Lat, b.Lng) * 1000
}
