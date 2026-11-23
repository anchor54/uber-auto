package lifecycle

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

var pickupAt = time.Date(2026, 10, 22, 3, 10, 0, 0, time.UTC) // Thu 8:40 IST

func eq[T any](t *testing.T, got, want T, what ...any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v: got %#v, want %#v", what, got, want)
	}
}

func at(minutesFromPickup int) time.Time {
	return pickupAt.Add(time.Duration(minutesFromPickup) * time.Minute)
}

func ptr[T any](v T) *T { return &v }

func ride(state string, f func(*RideFacts)) RideFacts {
	r := RideFacts{State: state, PickupAt: pickupAt, WindowStart: at(-5), WindowEnd: at(5), Pin: "4821", LastActivityAt: at(0)}
	if f != nil {
		f(&r)
	}
	return r
}

// okContext is a context in which [move] is allowed from its usual state.
func okContext(move string) MoveContext {
	switch move {
	case "depart":
		return MoveContext{Now: at(-20), Actor: "driver", DriverOnDuty: true}
	case "arrive":
		return MoveContext{Now: at(0), Actor: "driver", MetresToPickup: ptr(120.0)}
	case "start":
		return MoveContext{Now: at(0), Actor: "driver", Pin: "4821"}
	case "end":
		return MoveContext{Now: at(0), Actor: "driver", MetresToDrop: ptr(80.0)}
	case "no_show":
		return MoveContext{Now: at(10), Actor: "driver", ContactLogged: true}
	case "skip":
		return MoveContext{Now: at(-600), Actor: "rider"}
	case "cancel":
		return MoveContext{Now: at(0), Actor: "ops"}
	case "close_stale":
		return MoveContext{Now: at(200), Actor: "system"}
	}
	panic(move)
}

func with(move string, f func(*MoveContext)) MoveContext {
	c := okContext(move)
	f(&c)
	return c
}

func code(_ MoveResult, err error) string {
	var e *ApiError
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestTransitionTable(t *testing.T) {
	expected := map[string]struct {
		from []string
		to   string
	}{
		"depart":      {[]string{"scheduled"}, "on_the_way"},
		"arrive":      {[]string{"on_the_way"}, "arrived"},
		"start":       {[]string{"arrived"}, "in_ride"},
		"end":         {[]string{"in_ride"}, "completed"},
		"no_show":     {[]string{"arrived"}, "no_show"},
		"skip":        {[]string{"scheduled"}, "skipped"},
		"cancel":      {[]string{"scheduled", "on_the_way"}, "cancelled"},
		"close_stale": {[]string{"on_the_way", "arrived", "in_ride"}, "completed"},
	}
	for _, move := range Moves {
		for _, state := range RideStates {
			allowed := false
			for _, s := range expected[move].from {
				allowed = allowed || s == state
			}
			t.Run(fmt.Sprintf("%s from %s", move, state), func(t *testing.T) {
				facts := ride(state, func(r *RideFacts) { r.WaitEndsAt = ptr(at(5)) })
				r, err := Transition(facts, move, okContext(move))
				if allowed {
					want := expected[move].to
					if move == "close_stale" && state != "in_ride" {
						want = "cancelled"
					}
					eq(t, err, nil)
					eq(t, r.To, want)
				} else {
					eq(t, code(r, err), "invalid_transition")
				}
			})
		}
	}
}

func TestOnlyTheRightActor(t *testing.T) {
	eq(t, code(Transition(ride("scheduled", nil), "depart", with("depart", func(c *MoveContext) { c.Actor = "rider" }))), "not_allowed")
	eq(t, code(Transition(ride("scheduled", nil), "skip", with("skip", func(c *MoveContext) { c.Actor = "driver" }))), "not_allowed")
	eq(t, code(Transition(ride("on_the_way", nil), "close_stale", with("close_stale", func(c *MoveContext) { c.Actor = "driver" }))), "not_allowed")
}

func TestDepart(t *testing.T) {
	eq(t, code(Transition(ride("scheduled", nil), "depart", with("depart", func(c *MoveContext) { c.DriverOnDuty = false }))), "off_duty")
	eq(t, code(Transition(ride("scheduled", nil), "depart", with("depart", func(c *MoveContext) { c.DriverBusy = true }))), "driver_busy")
	eq(t, code(Transition(ride("scheduled", nil), "depart", with("depart", func(c *MoveContext) { c.Now = at(-91) }))), "too_early")
	r, _ := Transition(ride("scheduled", nil), "depart", with("depart", func(c *MoveContext) { c.Now = at(-90) }))
	eq(t, r.To, "on_the_way")
}

func TestArriveAndTheWait(t *testing.T) {
	eq(t, code(Transition(ride("on_the_way", nil), "arrive", with("arrive", func(c *MoveContext) { c.MetresToPickup = ptr(301.0) }))), "too_far")
	eq(t, code(Transition(ride("on_the_way", nil), "arrive", with("arrive", func(c *MoveContext) { c.MetresToPickup = nil }))), "too_far")
	// Arrives 8:28; the window opens 8:35, so the wait ends 8:40.
	r, _ := Transition(ride("on_the_way", nil), "arrive", with("arrive", func(c *MoveContext) { c.Now = at(-12) }))
	eq(t, *r.Set.WaitEndsAt, at(0))
	eq(t, WaitEndsAt(at(-2), at(-5), false), at(3))
	eq(t, WaitEndsAt(at(12), at(-5), false), at(17))
	eq(t, WaitEndsAt(at(-2), at(-5), true), at(8))
	r, _ = Transition(ride("on_the_way", func(r *RideFacts) { r.RiderLateUsed = true }), "arrive", with("arrive", func(c *MoveContext) { c.Now = at(-2) }))
	eq(t, *r.Set.WaitEndsAt, at(8))
}

func TestStartWithThePin(t *testing.T) {
	wrong := with("start", func(c *MoveContext) { c.Pin = "0000" })
	tries := func(n int) RideFacts { return ride("arrived", func(r *RideFacts) { r.PinTries = n }) }
	eq(t, code(Transition(tries(0), "start", wrong)), "wrong_pin")
	eq(t, code(Transition(tries(1), "start", wrong)), "wrong_pin")
	eq(t, code(Transition(tries(2), "start", wrong)), "pin_locked")
	eq(t, code(Transition(tries(3), "start", okContext("start"))), "pin_locked")
}

func TestEndAwayNeedsReason(t *testing.T) {
	eq(t, code(Transition(ride("in_ride", nil), "end", with("end", func(c *MoveContext) { c.MetresToDrop = ptr(900.0) }))), "too_far")
	r, err := Transition(ride("in_ride", nil), "end", with("end", func(c *MoveContext) {
		c.MetresToDrop = ptr(900.0)
		c.EndReason = "Rider asked to stop early"
	}))
	eq(t, err, nil)
	eq(t, r.To, "completed")
	eq(t, *r.Set.EndReason, "Rider asked to stop early")
}

func TestNoShow(t *testing.T) {
	waiting := ride("arrived", func(r *RideFacts) { r.WaitEndsAt = ptr(at(3)) })
	eq(t, code(Transition(waiting, "no_show", with("no_show", func(c *MoveContext) { c.Now = at(2) }))), "wait_not_over")
	eq(t, code(Transition(waiting, "no_show", with("no_show", func(c *MoveContext) { c.Now = at(3); c.ContactLogged = false }))), "no_contact_logged")
	r, _ := Transition(waiting, "no_show", with("no_show", func(c *MoveContext) { c.Now = at(3) }))
	eq(t, r.To, "no_show")
}

func TestCloseStale(t *testing.T) {
	eq(t, code(Transition(ride("on_the_way", nil), "close_stale", with("close_stale", func(c *MoveContext) { c.Now = at(179) }))), "not_stale")
	r, _ := Transition(ride("in_ride", nil), "close_stale", okContext("close_stale"))
	eq(t, r.To, "completed")
	r, _ = Transition(ride("arrived", nil), "close_stale", okContext("close_stale"))
	eq(t, r.To, "cancelled")
}

func TestPaymentStatus(t *testing.T) {
	for _, c := range [][3]string{
		{"upi", "cash", "paid"},
		{"cash", "cash", "paid"},
		{"", "upi", "paid"},
		{"upi", "", "paid_by_rider"},
		{"", "not_paid", "unpaid"},
		{"cash", "not_paid", "disputed"},
		{"", "", "unmarked"},
	} {
		eq(t, PaymentStatus(c[0], c[1]), c[2], c)
	}
}

func TestEtaAndLateFlag(t *testing.T) {
	eq(t, EtaMinutes(1), 5) // 1.85 km at 22 km/h ≈ 5 min
	eq(t, EtaMinutes(0), 1)
	// The doc example: at 8:36 an ETA of 15 minutes means late.
	eq(t, IsLate(at(-4), 15, at(5)), true)
	eq(t, IsLate(at(-4), 14, at(5)), false)
}

func TestNewPin(t *testing.T) {
	for range 50 {
		p := NewPin()
		if len(p) != 4 {
			t.Fatalf("pin %q", p)
		}
	}
}
