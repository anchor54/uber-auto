package domain

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"
)

var (
	home = Place{ID: "home", Title: "Home", Detail: "14th Main, HSR Layout", Lat: 12.9121, Lng: 77.6446}
	etv  = Place{ID: "etv", Title: "Embassy Tech Village", Detail: "Gate 3", Lat: 12.9406, Lng: 77.6964}
	// Wednesday 21 Oct 2026, 9:00 AM IST.
	testNow = time.Date(2026, 10, 21, 3, 30, 0, 0, time.UTC)
)

func eq[T any](t *testing.T, got, want T, what ...any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v: got %#v, want %#v", what, got, want)
	}
}

// ---- trips and fares (must match the rider app) ----

func TestDemoCommuteIs11_9KmAnd38Minutes(t *testing.T) {
	eq(t, EstimateTrip(home.Point(), etv.Point()), TripEstimate{DistanceKm: 11.9, Minutes: 38})
}

func TestFaresMatchTheApp(t *testing.T) {
	want := map[string]int{"bike": 90, "auto": 160, "cab_hatchback": 215, "cab_sedan": 250, "cab_suv": 330}
	for typ, fare := range want {
		eq(t, SuggestedFare(DefaultFareRules[typ], 11.9), fare, typ)
	}
	eq(t, SuggestedFare(DefaultFareRules["cab_sedan"], 1), 110, "short trips pay the minimum")
}

// ---- schedule ----

func TestLocalDateIsIST(t *testing.T) {
	eq(t, LocalDateString(testNow, 330), "2026-10-21")
	// 11:30 PM IST on the 21st is still the 21st locally, though 18:00 UTC.
	eq(t, LocalDateString(time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC), 330), "2026-10-21")
	eq(t, LocalDateString(time.Date(2026, 10, 21, 18, 31, 0, 0, time.UTC), 330), "2026-10-22")
}

func TestFirstRideIsNextMatchingDayNeverToday(t *testing.T) {
	eq(t, FirstRideDate("2026-10-21", []int{1, 2, 3, 4, 5}), "2026-10-22")
	eq(t, FirstRideDate("2026-10-23", []int{1, 2, 3, 4, 5}), "2026-10-26")
	eq(t, FirstRideDate("2026-10-21", []int{6}), "2026-10-24")
	eq(t, FirstRideDate("2026-10-21", []int{3}), "2026-10-28")
}

func TestArriveByRoundsDownToFive(t *testing.T) {
	eq(t, PickupForArrival(570, 38), 520)
}

func TestLegsClash(t *testing.T) {
	a := Leg{1, 520, 558}
	eq(t, LegsClash(a, Leg{1, 560, 600}), true)
	eq(t, LegsClash(a, Leg{1, 578, 600}), false)
	eq(t, LegsClash(a, Leg{2, 520, 558}), false)
}

func TestWeekdayCommuteWithReturnIsTenLegs(t *testing.T) {
	eq(t, len(WeeklyLegs(WeeklyCommute{[]int{1, 2, 3, 4, 5}, 520, true, 1110, 38})), 10)
}

// ---- commute request validation ----

func demoRequest() CommuteRequest {
	return CommuteRequest{Pickup: home, Drop: etv, Repeat: "every_weekday", Weekdays: []int{1, 2, 3, 4, 5},
		Timing: "pickup_at", Time: 520, ReturnTrip: true, ReturnTime: 1110, RideType: "cab_sedan", Preferences: []string{"ac_on"}}
}

func TestCommuteRequestValidation(t *testing.T) {
	ok := func(f func(*CommuteRequest)) bool {
		r := demoRequest()
		f(&r)
		return ValidateCommuteRequest(&r) == nil
	}
	eq(t, ok(func(*CommuteRequest) {}), true, "demo request")
	eq(t, ok(func(r *CommuteRequest) { r.Drop = home }), false, "same pickup and drop")
	eq(t, ok(func(r *CommuteRequest) { r.Weekdays = []int{1, 2, 3} }), false, "every weekday not Mon-Fri")
	eq(t, ok(func(r *CommuteRequest) { r.Repeat = "chosen_days"; r.Weekdays = []int{1, 2, 3} }), true, "chosen days")
	eq(t, ok(func(r *CommuteRequest) { r.Repeat = "chosen_days"; r.Weekdays = []int{} }), false, "no days")
	eq(t, ok(func(r *CommuteRequest) { r.Repeat = "chosen_days"; r.Weekdays = []int{6, 6} }), false, "repeated days")

	r := demoRequest()
	r.Drop = home
	eq(t, ValidateCommuteRequest(&r).Error(), "Pickup and drop-off can't be the same place")
}

// ---- ride plan ----

var (
	planHome   = Place{ID: "home", Title: "Home", Lat: 12.9, Lng: 77.6}
	planOffice = Place{ID: "etv", Title: "Office", Lat: 12.94, Lng: 77.69}
	weekday    = CommuteForPlan{Pickup: planHome, Drop: planOffice, Weekdays: []int{1, 2, 3, 4, 5},
		PickupTime: 520, ReturnTrip: true, ReturnTime: 1110, StartDate: "2026-10-22"}
)

func planOpts(f func(*PlanOptions)) PlanOptions {
	o := PlanOptions{From: "2026-10-21", To: "2026-11-04", Now: testNow, UTCOffsetMinutes: 330, Holidays: map[string]bool{}}
	if f != nil {
		f(&o)
	}
	return o
}

func TestPlanTwoLegsPerWeekday(t *testing.T) {
	plan := PlanRides(weekday, planOpts(nil))
	// Thu 22 Oct to Wed 4 Nov: 10 weekdays × 2 legs.
	eq(t, len(plan), 20)
	eq(t, []any{plan[0].RideDate, plan[0].Direction, plan[0].Pickup, plan[0].Drop}, []any{"2026-10-22", "outbound", planHome, planOffice})
	eq(t, plan[0].PickupAt, time.Date(2026, 10, 22, 3, 10, 0, 0, time.UTC))
	eq(t, plan[0].WindowStart, time.Date(2026, 10, 22, 3, 5, 0, 0, time.UTC))
	eq(t, plan[0].WindowEnd, time.Date(2026, 10, 22, 3, 15, 0, 0, time.UTC))
	eq(t, []any{plan[1].RideDate, plan[1].Direction, plan[1].Pickup, plan[1].Drop}, []any{"2026-10-22", "return", planOffice, planHome})
	for _, p := range plan {
		if p.RideDate == "2026-10-24" {
			t.Fatal("a Saturday was planned")
		}
	}
}

func TestPlanStartsMidFortnight(t *testing.T) {
	c := weekday
	c.StartDate = "2026-10-29"
	plan := PlanRides(c, planOpts(nil))
	eq(t, plan[0].RideDate, "2026-10-29")
	eq(t, len(plan), 10)
}

func TestPlanHolidayIsSkipped(t *testing.T) {
	plan := PlanRides(weekday, planOpts(func(o *PlanOptions) { o.Holidays = map[string]bool{"2026-10-23": true} }))
	var skipped []string
	for _, p := range plan {
		if p.Skipped {
			skipped = append(skipped, p.RideDate+" "+p.Direction)
		}
	}
	eq(t, skipped, []string{"2026-10-23 outbound", "2026-10-23 return"})
}

func TestPlanLeavesOutPastLegs(t *testing.T) {
	// Thursday 12:00 IST: the 8:40 leg is gone, the 18:30 return remains.
	plan := PlanRides(weekday, planOpts(func(o *PlanOptions) {
		o.From = "2026-10-22"
		o.Now = time.Date(2026, 10, 22, 6, 30, 0, 0, time.UTC)
	}))
	eq(t, []string{plan[0].RideDate, plan[0].Direction}, []string{"2026-10-22", "return"})
}

func TestPlanOneWaySaturdays(t *testing.T) {
	c := weekday
	c.Weekdays = []int{6}
	c.ReturnTrip = false
	var dates []string
	for _, p := range PlanRides(c, planOpts(nil)) {
		dates = append(dates, p.RideDate)
	}
	eq(t, dates, []string{"2026-10-24", "2026-10-31"})
	eq(t, PlanRides(weekday, planOpts(nil)), PlanRides(weekday, planOpts(nil)), "same every run")
}

// ---- polylines ----

func near(t *testing.T, got, want float64, digits int, what string) {
	t.Helper()
	if math.Abs(got-want) >= math.Pow(10, -float64(digits))/2 {
		t.Fatalf("%s: got %v, want %v to %d places", what, got, want, digits)
	}
}

func TestDecodeGooglesExample(t *testing.T) {
	path, err := DecodePolyline("_p~iF~ps|U_ulLnnqC_mqNvxq`@")
	eq(t, err, nil)
	eq(t, path, []LatLng{{38.5, -120.2}, {40.7, -120.95}, {43.252, -126.453}})
	_, err = DecodePolyline("_p~iF~ps|U_")
	if err == nil {
		t.Fatal("a cut-off polyline should be an error")
	}
	eq(t, EncodePolyline(path), "_p~iF~ps|U_ulLnnqC_mqNvxq`@")
}

func TestAlongPathByDistance(t *testing.T) {
	at := AlongPath([]LatLng{{12.9, 77.6}, {12.909, 77.6}, {12.909, 77.6092}})
	eq(t, at(0), LatLng{12.9, 77.6})
	eq(t, at(1), LatLng{12.909, 77.6092})
	half := at(0.5) // at the corner, not cutting across
	near(t, half.Lat, 12.909, 3, "half lat")
	near(t, half.Lng, 77.6, 3, "half lng")
	eq(t, at(0.25).Lng, 77.6)
}

func TestDistanceToPath(t *testing.T) {
	path := []LatLng{{12.9, 77.6}, {12.91, 77.6}}
	near(t, DistanceToPathKm(LatLng{12.905, 77.6}, path), 0, 3, "on the route")
	// 0.003° of longitude at 12.9°N is about 325 m, beside the middle of the segment.
	near(t, DistanceToPathKm(LatLng{12.905, 77.603}, path), 0.325, 2, "beside the route")
}

// ---- the transition table ----

var pickupAt = time.Date(2026, 10, 22, 3, 10, 0, 0, time.UTC) // Thu 8:40 IST

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
