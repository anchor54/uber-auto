package app

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"commute-backend/internal/domain"
)

// Flow R2 against real Postgres: ride records, the driver's moves, live
// view, payment, ratings, worker jobs.

var bg = context.Background()

// thu is Thursday 22 Oct 2026, the first ride day, at [hhmm] IST.
func thu(hhmm string) time.Time {
	t, err := time.Parse("2006-01-02 15:04 -0700", "2026-10-22 "+hhmm+" +0530")
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func autoAccept(c *Config) { c.AutoAcceptCommutes = true }

func (ta *testApp) driverToken(phone string) string {
	ta.t.Helper()
	if phone == "" {
		phone = raviPhone
	}
	ta.post("/v1/driver/auth/otp", map[string]string{"phone": phone}, "")
	return ta.post("/v1/driver/auth/verify", map[string]string{"phone": phone, "code": demoOTP}, "").str("token")
}

type rideRef struct {
	ID     string
	Pickup domain.LatLng
	Drop   domain.LatLng
}

type commuterResult struct {
	rider   string
	commute *resp
	rides   []any
	first   rideRef
}

func refOf(v any) rideRef {
	return rideRef{
		ID:     pathIn(v, "id").(string),
		Pickup: domain.LatLng{Lat: pathIn(v, "pickup.lat").(float64), Lng: pathIn(v, "pickup.lng").(float64)},
		Drop:   domain.LatLng{Lat: pathIn(v, "drop.lat").(float64), Lng: pathIn(v, "drop.lng").(float64)},
	}
}

// commuter is a rider with an active Mon–Fri commute with Ravi (auto-accept), so rides exist.
func (ta *testApp) commuter(phone string, overrides map[string]any) commuterResult {
	ta.t.Helper()
	rider := ta.signIn(phone)
	proposals := ta.propose(rider, overrides).list("proposals")
	i := slices.IndexFunc(proposals, func(p any) bool { return pathIn(p, "driver.id") == "d_ravi" })
	if i < 0 {
		ta.t.Fatal("Ravi is not offered")
	}
	commute := ta.confirm(rider, pathIn(proposals[i], "id").(string), "upi")
	rides := ta.get("/v1/rides/upcoming?days=7", rider).list("rides")
	out := commuterResult{rider: rider, commute: commute, rides: rides}
	if len(rides) > 0 {
		out.first = refOf(rides[0])
	}
	return out
}

func (ta *testApp) point(lat, lng float64, when time.Time) map[string]any {
	return map[string]any{"lat": lat, "lng": lng, "heading": 90, "speed": 8, "at": iso(when)}
}

func (ta *testApp) sendLocation(driver string, lat, lng float64) {
	ta.t.Helper()
	status(ta.t, ta.post("/v1/driver/location", map[string]any{"points": []any{ta.point(lat, lng, ta.now)}}, driver), 204)
}

// departed departs [rideID] at 8:20 with the driver ~2 km away.
func (ta *testApp) departed(rideID string, pickup domain.LatLng) string {
	ta.t.Helper()
	ta.now = thu("08:20")
	driver := ta.driverToken("")
	status(ta.t, ta.put("/v1/driver/duty", map[string]bool{"onDuty": true}, driver), 204)
	ta.sendLocation(driver, pickup.Lat+0.018, pickup.Lng)
	status(ta.t, ta.post("/v1/driver/rides/"+rideID+"/depart", nil, driver), 200)
	return driver
}

func (ta *testApp) arrivedAt(rideID string, pickup domain.LatLng, when string) string {
	ta.t.Helper()
	driver := ta.departed(rideID, pickup)
	ta.now = thu(when)
	ta.sendLocation(driver, pickup.Lat, pickup.Lng)
	status(ta.t, ta.post("/v1/driver/rides/"+rideID+"/arrive", nil, driver), 200)
	return driver
}

func must[T any](v T, err error) func(t *testing.T) T {
	return func(t *testing.T) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

// ---- ride records ----

func TestConfirmingCreatesRides14DaysAhead(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c := ta.commuter("", nil)
	eq(t, ta.count("select count(*)::int from rides"), 20) // Thu 22 Oct to Wed 4 Nov: 10 weekdays × 2
	var legs []string
	for _, r := range c.rides {
		legs = append(legs, pathIn(r, "date").(string)+" "+pathIn(r, "direction").(string))
	}
	eq(t, legs, []string{
		"2026-10-22 outbound", "2026-10-22 return", "2026-10-23 outbound", "2026-10-23 return",
		"2026-10-26 outbound", "2026-10-26 return", "2026-10-27 outbound", "2026-10-27 return",
	})
	matches(t, c.rides[0], map[string]any{"state": "scheduled", "fare": 250, "pin": nil, "pickupAt": "2026-10-22T03:10:00.000Z"})
	eq(t, pathIn(c.rides[1], "pickup.id"), "etv") // the return leg swaps pickup and drop
	eq(t, ta.get("/v1/home", c.rider).str("nextRide.id"), c.first.ID)
}

func TestCreatingAgainDoesNothingNightlyOnce(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	ta.commuter("", nil)
	eq(t, must(ta.app.CreateRides(bg, ""))(t), 0)
	ta.now = time.Date(2026, 10, 22, 18, 45, 0, 0, time.UTC) // Fri 00:15 IST: too early
	eq(t, must(ta.app.CreateRidesNightly(bg))(t), 0)
	ta.now = time.Date(2026, 10, 22, 19, 5, 0, 0, time.UTC) // Fri 23 Oct 00:35 IST: Thu 5 and Fri 6 Nov come into range
	eq(t, must(ta.app.CreateRidesNightly(bg))(t), 4)
	eq(t, must(ta.app.CreateRidesNightly(bg))(t), 0) // already ran tonight
}

func TestRidesAreCreatedWhenDriverAccepts(t *testing.T) {
	ta := startTestApp(t)
	c := ta.commuter("", nil)
	eq(t, c.rides, []any{})
	ta.post("/v1/driver/commutes/"+c.commute.str("id")+"/accept", nil, ta.driverToken(""))
	eq(t, ta.count("select count(*)::int from rides"), 20)
}

func TestCannotSeeOrMoveOthersRides(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c := ta.commuter("", nil)
	other := ta.signIn("9886054322")
	status(t, ta.get("/v1/rides/"+c.first.ID, other), 403)
	eq(t, ta.get("/v1/rides/"+c.first.ID+"/live", other).errCode(), "not_your_ride")
	suresh := ta.driverToken(sureshPhone)
	ta.put("/v1/driver/duty", map[string]bool{"onDuty": true}, suresh)
	ta.now = thu("08:20")
	status(t, ta.post("/v1/driver/rides/"+c.first.ID+"/depart", nil, suresh), 403)
}

// ---- a normal leg ----

func TestNormalLeg(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c := ta.commuter("", nil)
	id, pickup, drop := c.first.ID, c.first.Pickup, c.first.Drop
	driver := ta.departed(id, pickup)

	// The PIN shows from the moment the driver departs.
	ride := ta.get("/v1/rides/"+id, c.rider)
	eq(t, ride.str("state"), "on_the_way")
	pin := ride.str("pin")
	if !regexp.MustCompile(`^\d{4}$`).MatchString(pin) {
		t.Fatalf("pin %q", pin)
	}
	matches(t, ride.at("driver"), map[string]any{"name": "Ravi Kumar", "phone": raviPhone, "upiId": "ravi.kumar@okbank"})

	far := ta.get("/v1/rides/"+id+"/live", c.rider)
	matches(t, far.json(), map[string]any{"state": "on_the_way", "late": false, "serverTime": iso(thu("08:20"))})
	eq(t, far.num("etaMinutes"), 10.0) // 2 km × 1.85 at 22 km/h
	ta.now = thu("08:26")
	ta.sendLocation(driver, pickup.Lat+0.006, pickup.Lng)
	if near := ta.get("/v1/rides/"+id+"/live", c.rider).num("etaMinutes"); near >= far.num("etaMinutes") {
		t.Fatalf("ETA %v did not fall from %v", near, far.num("etaMinutes"))
	}

	// Too far to arrive; then at the pickup.
	eq(t, ta.post("/v1/driver/rides/"+id+"/arrive", nil, driver).errCode(), "too_far")
	ta.now = thu("08:38")
	ta.sendLocation(driver, pickup.Lat, pickup.Lng)
	eq(t, ta.post("/v1/driver/rides/"+id+"/arrive", nil, driver).str("waitEndsAt"), iso(thu("08:43")))
	matches(t, ta.get("/v1/rides/"+id+"/live", c.rider).json(), map[string]any{"state": "arrived", "etaMinutes": 0})

	eq(t, ta.post("/v1/driver/rides/"+id+"/start", map[string]string{"pin": pin}, driver).str("state"), "in_ride")
	eq(t, ta.get("/v1/rides/"+id, c.rider).at("pin"), nil, "PIN hidden once riding")

	ta.now = thu("09:20")
	end := ta.post("/v1/driver/rides/"+id+"/end", map[string]float64{"lat": drop.Lat, "lng": drop.Lng}, driver)
	matches(t, end.json(), map[string]any{"state": "completed", "paymentStatus": "unmarked"})

	eq(t, ta.strings("select from_state || '→' || to_state from ride_events where ride_id = $1 order by id", id), []string{
		"scheduled→on_the_way", "on_the_way→arrived", "arrived→in_ride", "in_ride→completed",
	})
	eq(t, ta.strings("select kind from notifications where ride_id = $1 order by id", id), []string{"driver_on_the_way", "driver_arrived"})
}

func TestThreeWrongPinsLockTheStart(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c := ta.commuter("", nil)
	driver := ta.arrivedAt(c.first.ID, c.first.Pickup, "08:38")
	pin := ta.pin(c.first.ID)
	wrong := "0000"
	if pin == wrong {
		wrong = "1111"
	}
	start := func(p string) *resp {
		return ta.post("/v1/driver/rides/"+c.first.ID+"/start", map[string]string{"pin": p}, driver)
	}
	a := start(wrong)
	eq(t, []any{a.status, a.errCode(), a.str("message")}, []any{422, "wrong_pin", "That PIN isn't right. 2 tries left."})
	if !strings.Contains(start(wrong).str("message"), "1 try left") {
		t.Fatal("second wrong PIN should leave 1 try")
	}
	status(t, start(wrong), 423)
	locked := start(pin)
	eq(t, []any{locked.status, locked.errCode()}, []any{423, "pin_locked"})
}

func TestEndingAwayNeedsAReason(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c := ta.commuter("", nil)
	driver := ta.arrivedAt(c.first.ID, c.first.Pickup, "08:38")
	ta.post("/v1/driver/rides/"+c.first.ID+"/start", map[string]string{"pin": ta.pin(c.first.ID)}, driver)
	far := map[string]any{"lat": c.first.Pickup.Lat, "lng": c.first.Pickup.Lng}
	status(t, ta.post("/v1/driver/rides/"+c.first.ID+"/end", far, driver), 422)
	far["reason"] = "Rider asked to stop"
	eq(t, ta.post("/v1/driver/rides/"+c.first.ID+"/end", far, driver).str("state"), "completed")
}

func TestBusyDriverCannotDepartAnotherOrGoOffDuty(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	a := ta.commuter("9886054321", nil)
	// 10:00 and 20:00: clear of Ravi's 8:40 and 18:30 legs plus the 20-minute gap.
	b := ta.commuter("9886054322", map[string]any{"time": 600, "returnTime": 1200})
	driver := ta.departed(a.first.ID, a.first.Pickup)
	second := ta.post("/v1/driver/rides/"+b.first.ID+"/depart", nil, driver)
	eq(t, []any{second.status, second.errCode()}, []any{409, "driver_busy"})
	eq(t, ta.put("/v1/driver/duty", map[string]bool{"onDuty": false}, driver).errCode(), "rides_in_progress")
}

func TestOffDutyOrTooEarlyCannotDepart(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c := ta.commuter("", nil)
	driver := ta.driverToken("")
	ta.now = thu("08:20")
	eq(t, ta.post("/v1/driver/rides/"+c.first.ID+"/depart", nil, driver).errCode(), "off_duty")
	ta.put("/v1/driver/duty", map[string]bool{"onDuty": true}, driver)
	ta.now = thu("07:05")
	eq(t, ta.post("/v1/driver/rides/"+c.first.ID+"/depart", nil, driver).errCode(), "too_early")
}

func TestDisallowedMoveSaysTheState(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c := ta.commuter("", nil)
	res := ta.post("/v1/driver/rides/"+c.first.ID+"/arrive", nil, ta.driverToken(""))
	eq(t, []any{res.status, res.errCode()}, []any{409, "invalid_transition"})
	if !strings.Contains(res.str("message"), "scheduled") {
		t.Fatalf("message %q", res.str("message"))
	}
}

// ---- the driver's road route to the pickup ----

func TestApproachRouteAskedOnceKeptRefreshed(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c := ta.commuter("", nil)
	pickup := c.first.Pickup
	maps := &fakeMaps{estimate: func(from, _ domain.LatLng) domain.TripEstimate {
		// A road due south from wherever the driver is to the pickup.
		return domain.TripEstimate{DistanceKm: 3, Minutes: 12, Polyline: domain.EncodePolyline([]domain.LatLng{from, {Lat: pickup.Lat, Lng: from.Lng}, pickup})}
	}}
	ta.app.Maps = maps
	driver := ta.departed(c.first.ID, pickup) // starts ~2 km north
	eq(t, len(maps.asked), 0)

	ta.now = thu("08:21")
	start := domain.LatLng{Lat: pickup.Lat + 0.018, Lng: pickup.Lng + 0.001}
	ta.sendLocation(driver, start.Lat, start.Lng)
	eq(t, len(maps.asked), 1)
	live := ta.get("/v1/rides/"+c.first.ID+"/live", c.rider)
	if live.str("approachRoute") == "" {
		t.Fatal("no approach route")
	}
	eq(t, live.num("etaMinutes"), 12.0) // the road drive time, not the straight-line guess

	// Further down the same road: the route is kept, and the ETA falls with distance.
	ta.now = thu("08:24")
	ta.sendLocation(driver, pickup.Lat+0.009, start.Lng)
	eq(t, len(maps.asked), 1)
	eq(t, ta.get("/v1/rides/"+c.first.ID+"/live", c.rider).num("etaMinutes"), 6.0)

	// Off the route by ~1 km: asked again...
	ta.now = thu("08:26")
	ta.sendLocation(driver, pickup.Lat+0.009, start.Lng+0.01)
	eq(t, len(maps.asked), 2)
	// ...but not again within a minute, wherever the driver goes.
	ta.now = thu("08:26").Add(30 * time.Second)
	ta.sendLocation(driver, pickup.Lat+0.009, start.Lng-0.01)
	eq(t, len(maps.asked), 2)
}

func TestApproachRouteOnlyWhileOnTheWay(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c := ta.commuter("", nil)
	ta.arrivedAt(c.first.ID, c.first.Pickup, "08:38")
	eq(t, ta.get("/v1/rides/"+c.first.ID+"/live", c.rider).at("approachRoute"), nil)
}

// sleepByClock moves the test clock instead of sleeping: each simulated
// tick at speed 600 moves it 5 s.
func (ta *testApp) sleepByClock(d time.Duration) { ta.now = ta.now.Add(d * 600) }

func TestSimulatorDrivesTheApproachRoute(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c := ta.commuter("", nil)
	pickup := c.first.Pickup
	bend := domain.LatLng{Lat: pickup.Lat + 0.009, Lng: pickup.Lng + 0.02} // well east of the straight line
	ta.app.Maps = &fakeMaps{estimate: func(from, _ domain.LatLng) domain.TripEstimate {
		return domain.TripEstimate{DistanceKm: 4, Minutes: 10, Polyline: domain.EncodePolyline([]domain.LatLng{from, bend, pickup})}
	}}
	ta.now = thu("08:25")
	if err := ta.app.SimPlay(bg, c.first.ID, PlayOptions{Speed: 600, Sleep: ta.sleepByClock}); err != nil {
		t.Fatal(err)
	}
	lngs := ta.strings(`select lng::text from ride_track where ride_id = $1 and at < (select arrived_at from rides where id = $1)`, c.first.ID)
	if maxFloat(lngs) <= pickup.Lng+0.015 {
		t.Fatalf("approach never went east: %v", lngs)
	}
}

// ---- in-ride ETA ----

func TestInRideEtaScalesTheBookedTripTime(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c := ta.commuter("", nil)
	p, d := c.first.Pickup, c.first.Drop
	driver := ta.arrivedAt(c.first.ID, p, "08:38")
	ta.post("/v1/driver/rides/"+c.first.ID+"/start", map[string]string{"pin": ta.pin(c.first.ID)}, driver)
	live := func() float64 { return ta.get("/v1/rides/"+c.first.ID+"/live", c.rider).num("etaMinutes") }
	eq(t, live(), 38.0) // at the pickup: the whole trip is ahead
	ta.now = thu("08:58")
	ta.sendLocation(driver, (p.Lat+d.Lat)/2, (p.Lng+d.Lng)/2)
	eq(t, live(), 19.0) // halfway in straight-line terms: half the trip time
	ta.now = thu("09:15")
	ta.sendLocation(driver, d.Lat, d.Lng)
	eq(t, live(), 1.0)
}

// ---- location ----

func TestOldOrFuturePointsAreRefused(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	driver := ta.driverToken("")
	old := ta.post("/v1/driver/location", map[string]any{"points": []any{ta.point(12.9, 77.6, ta.now.Add(-3*time.Minute))}}, driver)
	eq(t, []any{old.status, old.errCode()}, []any{400, "stale_location"})
	status(t, ta.post("/v1/driver/location", map[string]any{"points": []any{ta.point(12.9, 77.6, ta.now.Add(5*time.Minute))}}, driver), 400)
}

func TestOneTrackPointEvery30Seconds(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c := ta.commuter("", nil)
	driver := ta.departed(c.first.ID, c.first.Pickup)
	for s := 5; s <= 65; s += 5 {
		ta.now = thu("08:20").Add(time.Duration(s) * time.Second)
		ta.sendLocation(driver, c.first.Pickup.Lat+0.01, c.first.Pickup.Lng)
	}
	eq(t, ta.count("select count(*)::int from ride_track where ride_id = $1", c.first.ID), 3) // 8:20:05, 8:20:35, 8:21:05
}

// ---- waiting and no-shows ----

func TestNoShowRules(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c := ta.commuter("", nil)
	id := c.first.ID
	driver := ta.arrivedAt(id, c.first.Pickup, "08:38")
	ta.now = thu("08:42")
	status(t, ta.post("/v1/driver/rides/"+id+"/no-show", nil, driver), 425)
	ta.now = thu("08:43")
	eq(t, ta.post("/v1/driver/rides/"+id+"/no-show", nil, driver).errCode(), "no_contact_logged")
	status(t, ta.post("/v1/driver/rides/"+id+"/contact", map[string]string{"kind": "call"}, driver), 204)
	eq(t, ta.post("/v1/driver/rides/"+id+"/no-show", nil, driver).str("state"), "no_show")
	eq(t, ta.get("/v1/rides/"+pathIn(c.rides[1], "id").(string), c.rider).str("state"), "scheduled")
}

func TestRunningLateExtendsTheWaitOnce(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c := ta.commuter("", nil)
	id := c.first.ID
	driver := ta.arrivedAt(id, c.first.Pickup, "08:38")
	late := ta.post("/v1/rides/"+id+"/running-late", nil, c.rider)
	matches(t, late.json(), map[string]any{"riderLateUsed": true, "waitEndsAt": iso(thu("08:48"))})
	eq(t, ta.post("/v1/rides/"+id+"/running-late", nil, c.rider).errCode(), "already_used")
	ta.post("/v1/driver/rides/"+id+"/contact", map[string]string{"kind": "alert"}, driver)
	ta.now = thu("08:44")
	status(t, ta.post("/v1/driver/rides/"+id+"/no-show", nil, driver), 425)
	kinds := ta.strings("select kind || ' ' || recipient_type from notifications where ride_id = $1", id)
	for _, want := range []string{"rider_running_late driver", "driver_alert rider"} {
		if !slices.Contains(kinds, want) {
			t.Fatalf("%q not in %v", want, kinds)
		}
	}
}

func TestRunningLateBeforeArrivalAppliesOnArrival(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c := ta.commuter("", nil)
	ta.post("/v1/rides/"+c.first.ID+"/running-late", nil, c.rider)
	ta.arrivedAt(c.first.ID, c.first.Pickup, "08:38")
	eq(t, ta.get("/v1/rides/"+c.first.ID, c.rider).str("waitEndsAt"), iso(thu("08:48")))
}

// completedRide completes the first ride.
func (ta *testApp) completedRide() (commuterResult, string) {
	ta.t.Helper()
	c := ta.commuter("", nil)
	driver := ta.arrivedAt(c.first.ID, c.first.Pickup, "08:38")
	ta.post("/v1/driver/rides/"+c.first.ID+"/start", map[string]string{"pin": ta.pin(c.first.ID)}, driver)
	ta.now = thu("09:20")
	ta.post("/v1/driver/rides/"+c.first.ID+"/end", map[string]float64{"lat": c.first.Drop.Lat, "lng": c.first.Drop.Lng}, driver)
	return c, driver
}

// ---- payment marks and ratings ----

func TestEachSideMarksPayment(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c, driver := ta.completedRide()
	id := c.first.ID
	eq(t, ta.post("/v1/rides/"+id+"/payment", map[string]string{"method": "upi"}, c.rider).str("paymentStatus"), "paid_by_rider")
	eq(t, ta.post("/v1/driver/rides/"+id+"/payment", map[string]string{"mark": "not_paid"}, driver).str("paymentStatus"), "disputed")
	eq(t, ta.post("/v1/driver/rides/"+id+"/payment", map[string]string{"mark": "cash"}, driver).str("paymentStatus"), "paid")
}

func TestCannotMarkOrRateUnfinishedRide(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c := ta.commuter("", nil)
	status(t, ta.post("/v1/rides/"+c.first.ID+"/payment", map[string]string{"method": "cash"}, c.rider), 409)
	status(t, ta.post("/v1/rides/"+c.first.ID+"/rating", map[string]int{"stars": 5}, c.rider), 409)
}

func TestUnsettledListsUnmarkedUnratedRides(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c, _ := ta.completedRide()
	eq(t, pluck(ta.get("/v1/rides/unsettled", c.rider).list("rides"), "id"), []any{c.first.ID})
	status(t, ta.post("/v1/rides/"+c.first.ID+"/rating", map[string]any{"stars": 4, "tags": []string{"Clean car"}}, c.rider), 204)
	eq(t, ta.get("/v1/rides/unsettled", c.rider).list("rides"), []any{})
}

func TestRatingCanChangeFor7DaysAndDontMatch(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c, _ := ta.completedRide()
	id := c.first.ID
	ta.post("/v1/rides/"+id+"/rating", map[string]int{"stars": 5}, c.rider)
	ta.post("/v1/rides/"+id+"/rating", map[string]any{"stars": 2, "tags": []string{"Late"}, "note": "Twenty minutes late", "dontMatch": true}, c.rider)
	eq(t, ta.get("/v1/rides/"+id, c.rider).at("rating"), map[string]any{"stars": 2.0, "tags": []any{"Late"}, "note": "Twenty minutes late", "dontMatch": true})
	again := pluck(ta.propose(c.rider, map[string]any{"time": 600, "returnTime": 1200}).list("proposals"), "driver.id")
	if slices.Contains(again, any("d_ravi")) {
		t.Fatal("Ravi matched after don't-match")
	}
	ta.now = thu("09:20").Add(8 * 24 * time.Hour)
	eq(t, ta.post("/v1/rides/"+id+"/rating", map[string]int{"stars": 3}, c.rider).errCode(), "rating_closed")
}

// ---- worker jobs ----

func TestLegIsOnQueuedOnce(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c := ta.commuter("", nil)
	ta.now = thu("08:05")
	eq(t, must(ta.app.QueueLegIsOn(bg))(t), 0)
	ta.now = thu("08:11")
	eq(t, must(ta.app.QueueLegIsOn(bg))(t), 1)
	eq(t, must(ta.app.QueueLegIsOn(bg))(t), 0)
	var payload map[string]any
	if err := shared.QueryRow(bg, `select payload from notifications where kind = 'leg_is_on' and ride_id = $1`, c.first.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	eq(t, payload, map[string]any{"driverFirstName": "Ravi", "pickupTime": "8:40 AM"})
}

func TestFlagsLateDriverOnce(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c := ta.commuter("", nil)
	driver := ta.departed(c.first.ID, c.first.Pickup)
	ta.now = thu("08:36")
	ta.sendLocation(driver, c.first.Pickup.Lat+0.03, c.first.Pickup.Lng) // ~3.3 km: 16 min ETA → 8:52
	eq(t, must(ta.app.FlagLateDrivers(bg))(t), 1)
	var flagged time.Time
	shared.QueryRow(bg, "select late_flagged_at from rides where id = $1", c.first.ID).Scan(&flagged)
	eq(t, flagged.UTC(), thu("08:36"))
	eq(t, ta.get("/v1/rides/"+c.first.ID+"/live", c.rider).at("late"), true)
	eq(t, ta.count(`select count(*)::int from notifications where kind = 'driver_running_late'`), 1)
}

func TestDriverWhoNeverDepartsIsLate(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	ta.commuter("", nil)
	ta.now = thu("08:50")
	eq(t, must(ta.app.FlagLateDrivers(bg))(t), 0)
	ta.now = thu("08:51")
	eq(t, must(ta.app.FlagLateDrivers(bg))(t), 1)
}

func TestPaymentRemindersTwoHoursAfterDrop(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c, _ := ta.completedRide()
	ta.post("/v1/rides/"+c.first.ID+"/payment", map[string]string{"method": "cash"}, c.rider)
	ta.now = thu("11:19")
	eq(t, must(ta.app.QueuePaymentReminders(bg))(t), 0)
	ta.now = thu("11:21")
	eq(t, must(ta.app.QueuePaymentReminders(bg))(t), 1)
	eq(t, ta.strings(`select recipient_type from notifications where kind = 'payment_reminder'`), []string{"driver"})
}

func TestClosesRidesStaleFor3Hours(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c := ta.commuter("", nil)
	ta.departed(c.first.ID, c.first.Pickup)
	ta.now = thu("08:20").Add(3*time.Hour + time.Minute)
	eq(t, must(ta.app.CloseStaleRides(bg))(t), 1)
	eq(t, ta.strings("select state from rides where id = $1", c.first.ID), []string{"cancelled"})
}

func TestSendsEachNotificationOnceRetryingFailures(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c := ta.commuter("", nil)
	status(t, ta.put("/v1/devices", map[string]string{"pushToken": "token-phone-0001", "platform": "android"}, c.rider), 204)
	ta.departed(c.first.ID, c.first.Pickup)
	type sentPush struct {
		token string
		m     PushMessage
	}
	var sent []sentPush
	fail := true
	flaky := PushSenderFunc(func(_ context.Context, token string, m PushMessage) error {
		if fail {
			return errors.New("FCM unavailable")
		}
		sent = append(sent, sentPush{token, m})
		return nil
	})
	eq(t, must(ta.app.SendDueNotifications(bg, flaky))(t), 0)
	fail = false
	eq(t, must(ta.app.SendDueNotifications(bg, flaky))(t), 0) // backing off for a minute
	ta.later(time.Minute)
	eq(t, must(ta.app.SendDueNotifications(bg, flaky))(t), 1)
	eq(t, must(ta.app.SendDueNotifications(bg, flaky))(t), 0)
	eq(t, sent, []sentPush{{"token-phone-0001", PushMessage{"Ravi is on the way", "Track your ride live.", PushData{"driver_on_the_way", c.first.ID}}}})
}

// ---- simulator ----

func TestSimulatorPlaysAWholeRide(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c := ta.commuter("", nil)
	ta.now = thu("08:25")
	if err := ta.app.SimPlay(bg, c.first.ID, PlayOptions{Speed: 600, Sleep: ta.sleepByClock}); err != nil {
		t.Fatal(err)
	}
	eq(t, ta.get("/v1/rides/"+c.first.ID, c.rider).str("state"), "completed")
	if n := ta.count("select count(*)::int from ride_track where ride_id = $1", c.first.ID); n <= 10 {
		t.Fatalf("only %d track points", n)
	}
}

func TestSimulatorDrivesTheRoadRoute(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c := ta.commuter("", nil)
	// A road route that bends far east of the straight line home → office.
	route := domain.EncodePolyline([]domain.LatLng{c.first.Pickup, {Lat: 12.92, Lng: 77.75}, c.first.Drop})
	ta.exec("update rides set route = $2 where id = $1", c.first.ID, route)
	ta.now = thu("08:25")
	if err := ta.app.SimPlay(bg, c.first.ID, PlayOptions{Speed: 600, Sleep: ta.sleepByClock}); err != nil {
		t.Fatal(err)
	}
	// The straight line never goes east of the office (77.6964); the road does.
	if m := maxFloat(ta.strings("select lng::text from ride_track where ride_id = $1", c.first.ID)); m <= 77.72 {
		t.Fatalf("easternmost point %v", m)
	}
}

func TestDevRoutesStepARideAndMoveTheClock(t *testing.T) {
	ta := startTestApp(t, autoAccept, func(c *Config) { c.EnableDriverSim = true })
	c := ta.commuter("", nil)
	eq(t, ta.post("/dev/sim/clock", map[string]string{"now": iso(thu("08:20"))}, "").str("now"), iso(thu("08:20")))
	eq(t, ta.get("/dev/sim/rides?riderPhone=9886054321", "").str("rides.0.id"), c.first.ID)
	for _, move := range []string{"depart", "arrive", "start", "end"} {
		status(t, ta.post("/dev/sim/rides/"+c.first.ID+"/step", map[string]string{"move": move}, ""), 200)
	}
	eq(t, ta.get("/v1/rides/"+c.first.ID, c.rider).str("state"), "completed")
}

func maxFloat(values []string) float64 {
	best := -1e9
	for _, v := range values {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > best {
			best = f
		}
	}
	return best
}
