package app

import (
	"regexp"
	"testing"
	"time"
)

// Flow R1: sign-in, places, fares, matching, confirming and Home.

// ---- R1.1 sign in with OTP ----

func TestSendsCodeAndSignsNewRiderIn(t *testing.T) {
	ta := startTestApp(t)
	status(t, ta.post("/v1/auth/otp", map[string]string{"phone": "9886054321"}, ""), 204)
	eq(t, ta.sentCodes, []sentCode{{"9886054321", demoOTP}})
	res := ta.post("/v1/auth/verify", map[string]string{"phone": "9886054321", "code": demoOTP}, "")
	status(t, res, 200)
	matches(t, res.at("rider"), map[string]any{"phone": "9886054321", "name": nil})
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(res.str("token")) {
		t.Fatalf("token %q", res.str("token"))
	}
}

func TestRejectsNonIndianMobiles(t *testing.T) {
	ta := startTestApp(t)
	res := ta.post("/v1/auth/otp", map[string]string{"phone": "1234567890"}, "")
	status(t, res, 400)
	eq(t, res.str("message"), "Enter a 10-digit mobile number starting with 6, 7, 8 or 9")
}

func TestWrongCodeThenLocksAfterFive(t *testing.T) {
	ta := startTestApp(t)
	ta.post("/v1/auth/otp", map[string]string{"phone": "9886054321"}, "")
	for range 5 {
		res := ta.post("/v1/auth/verify", map[string]string{"phone": "9886054321", "code": "000000"}, "")
		status(t, res, 401)
		eq(t, res.errCode(), "invalid_code")
	}
	status(t, ta.post("/v1/auth/verify", map[string]string{"phone": "9886054321", "code": demoOTP}, ""), 429)
}

func TestCodesExpireAfterFiveMinutes(t *testing.T) {
	ta := startTestApp(t)
	ta.post("/v1/auth/otp", map[string]string{"phone": "9886054321"}, "")
	ta.later(6 * time.Minute)
	eq(t, ta.post("/v1/auth/verify", map[string]string{"phone": "9886054321", "code": demoOTP}, "").errCode(), "code_expired")
}

func TestSigningInAgainKeepsTheSameRider(t *testing.T) {
	ta := startTestApp(t)
	first, second := ta.signIn(""), ta.signIn("")
	eq(t, ta.get("/v1/home", first).str("rider.id"), ta.get("/v1/home", second).str("rider.id"))
}

func TestProtectedRoutesNeedASession(t *testing.T) {
	ta := startTestApp(t)
	status(t, ta.get("/v1/home", ""), 401)
	status(t, ta.get("/v1/home", "not-a-token"), 401)
}

// ---- R1.2 places, trip and fares ----

func TestSearchesPlacesByTitleOrArea(t *testing.T) {
	ta := startTestApp(t)
	token := ta.signIn("")
	eq(t, pluck(ta.get("/v1/places?q=hsr", token).list("places"), "id"), []any{"home"})
	eq(t, len(ta.get("/v1/places", token).list("places")), 7)
}

func TestEstimatesTheTrip(t *testing.T) {
	ta := startTestApp(t)
	token := ta.signIn("")
	res := ta.post("/v1/trips/estimate", map[string]any{"pickup": place("home"), "drop": place("etv")}, token)
	eq(t, res.json(), map[string]any{"distanceKm": 11.9, "minutes": 38.0})
}

// ---- R1.3 booking options follow the ride-type config ----

func bookingBody(overrides map[string]any) map[string]any {
	b := map[string]any{"flow": "recurring_weekday", "pickup": place("home"), "drop": place("etv"), "pickupTime": 520}
	for k, v := range overrides {
		b[k] = v
	}
	return b
}

func TestOffersEveryTypeWithFaresAndDrivers(t *testing.T) {
	ta := startTestApp(t)
	res := ta.post("/v1/booking-options", bookingBody(nil), ta.signIn(""))
	eq(t, res.str("defaultType"), "cab_sedan")
	eq(t, pluck(res.list("options"), "type"), []any{"bike", "auto", "cab_hatchback", "cab_sedan", "cab_suv"})
	eq(t, pluck(res.list("options"), "fare"), []any{90.0, 160.0, 215.0, 250.0, 330.0})
	for _, n := range pluck(res.list("options"), "driversOnRoute") {
		eq(t, n, 3.0)
	}
}

func TestConfigSwitchesRideTypesOffForOneFlow(t *testing.T) {
	ta := startTestApp(t)
	ta.exec(`update ride_type_config set allowed = '{auto,cab_sedan}', default_type = 'auto' where flow = 'recurring_weekly'`)
	token := ta.signIn("")
	weekly := ta.post("/v1/booking-options", bookingBody(map[string]any{"flow": "recurring_weekly"}), token)
	eq(t, pluck(weekly.list("options"), "type"), []any{"auto", "cab_sedan"})
	eq(t, weekly.str("defaultType"), "auto")
	eq(t, len(ta.post("/v1/booking-options", bookingBody(nil), token).list("options")), 5)
}

func TestNoDriversOutsideWorkingHours(t *testing.T) {
	ta := startTestApp(t)
	res := ta.post("/v1/booking-options", bookingBody(map[string]any{"pickupTime": 300}), ta.signIn(""))
	for _, n := range pluck(res.list("options"), "driversOnRoute") {
		eq(t, n, 0.0)
	}
}

// ---- R1.4 driver match ----

func (ta *testApp) propose(token string, overrides map[string]any) *resp {
	ta.t.Helper()
	return ta.post("/v1/commutes/proposals", map[string]any{"request": commuteRequest(overrides)}, token)
}

func TestProposesThreeSedanDriversNearestFirst(t *testing.T) {
	ta := startTestApp(t)
	res := ta.propose(ta.signIn(""), nil)
	status(t, res, 200)
	eq(t, res.str("startDate"), "2026-10-22")
	eq(t, res.num("pickupTime"), 520.0)
	eq(t, pluck(res.list("proposals"), "driver.name"), []any{"Ravi Kumar", "Suresh M", "Imran K"})
	eq(t, res.num("proposals.0.fare"), 250.0)
	eq(t, res.str("proposals.0.reasons.1"), "Already drives near HSR Layout at this time")
	eq(t, res.str("proposals.0.expiresAt"), "2026-10-21T03:45:00.000Z")
}

func TestArriveByWorksOutPickupOnServer(t *testing.T) {
	ta := startTestApp(t)
	eq(t, ta.propose(ta.signIn(""), map[string]any{"timing": "arrive_by", "time": 570}).num("pickupTime"), 520.0)
}

func TestWomanDriverPreference(t *testing.T) {
	ta := startTestApp(t)
	token := ta.signIn("")
	eq(t, ta.propose(token, map[string]any{"preferences": []string{"woman_driver"}}).list("proposals"), []any{})
	ta.exec(`update drivers set gender = 'female' where id = 'd_imran'`)
	eq(t, pluck(ta.propose(token, map[string]any{"preferences": []string{"woman_driver"}}).list("proposals"), "driver.id"), []any{"d_imran"})
}

func TestPickupsFarFromDriverBasesAreNotOffered(t *testing.T) {
	ta := startTestApp(t)
	res := ta.propose(ta.signIn(""), map[string]any{"pickup": place("airport"), "drop": place("manyata")})
	eq(t, res.list("proposals"), []any{})
}

func TestSwitchedOffRideTypeIsRefused(t *testing.T) {
	ta := startTestApp(t)
	ta.exec(`update ride_type_config set allowed = '{auto}' where flow = 'recurring_weekday'`)
	res := ta.propose(ta.signIn(""), nil)
	status(t, res, 422)
	eq(t, res.errCode(), "ride_type_not_allowed")
}

func TestInvalidRequestsGetReadableMessage(t *testing.T) {
	ta := startTestApp(t)
	res := ta.propose(ta.signIn(""), map[string]any{"drop": place("home")})
	status(t, res, 400)
	eq(t, res.errCode(), "invalid_request")
	eq(t, res.str("message"), "Pickup and drop-off can't be the same place")
}

// ---- R1.5 confirm and R1.6 Home ----

func (ta *testApp) confirm(token, proposalID, payment string) *resp {
	ta.t.Helper()
	return ta.post("/v1/commutes", map[string]string{"proposalId": proposalID, "usualPayment": payment}, token)
}

func TestHomeIsEmptyForNewRider(t *testing.T) {
	ta := startTestApp(t)
	home := ta.get("/v1/home", ta.signIn(""))
	eq(t, home.at("commute"), nil)
	eq(t, home.list("recentRides"), []any{})
}

func TestConfirmingCreatesTheCommute(t *testing.T) {
	ta := startTestApp(t)
	token := ta.signIn("")
	res := ta.confirm(token, ta.propose(token, nil).str("proposals.0.id"), "cash")
	status(t, res, 201)
	matches(t, res.json(), map[string]any{
		"status": "awaiting_driver", "startDate": "2026-10-22", "pickupTime": 520, "fare": 250, "usualPayment": "cash",
		"rideType": "cab_sedan", "weekdays": []int{1, 2, 3, 4, 5},
		"driver": map[string]any{"name": "Ravi Kumar", "plate": "KA 01 AB 1234", "upiId": "ravi.kumar@okbank"},
	})
	home := ta.get("/v1/home", token)
	eq(t, home.str("commute.driver.name"), "Ravi Kumar")
	eq(t, home.str("rider.usualPayment"), "cash")
}

func TestAutoAcceptMakesCommuteActive(t *testing.T) {
	ta := startTestApp(t, func(c *Config) { c.AutoAcceptCommutes = true })
	token := ta.signIn("")
	res := ta.confirm(token, ta.propose(token, nil).str("proposals.0.id"), "upi")
	eq(t, res.str("status"), "active")
	eq(t, res.at("respondBy"), nil)
	eq(t, res.str("driver.phone"), "9000000010")
}

func TestExpiredHoldMustBeSearchedAgain(t *testing.T) {
	ta := startTestApp(t)
	token := ta.signIn("")
	id := ta.propose(token, nil).str("proposals.0.id")
	ta.later(16 * time.Minute)
	status(t, ta.confirm(token, id, "upi"), 410)
}

func TestCannotConfirmSomeoneElsesProposal(t *testing.T) {
	ta := startTestApp(t)
	mine, theirs := ta.signIn("9886054321"), ta.signIn("9000000001")
	status(t, ta.confirm(theirs, ta.propose(mine, nil).str("proposals.0.id"), "upi"), 404)
}

func TestDriverSlotCannotBeTakenTwice(t *testing.T) {
	ta := startTestApp(t)
	a, b := ta.signIn("9886054321"), ta.signIn("9000000001")
	forA := ta.propose(a, nil).str("proposals.0.id")
	forB := ta.propose(b, nil).str("proposals.0.id")
	status(t, ta.confirm(a, forA, "upi"), 201)
	second := ta.confirm(b, forB, "upi")
	status(t, second, 409)
	eq(t, second.errCode(), "driver_unavailable")
	eq(t, pluck(ta.propose(b, nil).list("proposals"), "driver.name"), []any{"Suresh M", "Imran K"})
}

func TestCommuteAtDifferentTimeCanShareDriver(t *testing.T) {
	ta := startTestApp(t)
	a, b := ta.signIn("9886054321"), ta.signIn("9000000001")
	ta.confirm(a, ta.propose(a, nil).str("proposals.0.id"), "upi")
	// 7:30 AM pickup, 38 min trip, ends 8:08: 32 minutes before Ravi's 8:40 pickup.
	eq(t, ta.propose(b, map[string]any{"time": 450, "returnTrip": false}).str("proposals.0.driver.name"), "Ravi Kumar")
}

func TestSaturdayOnlyStartsThisSaturday(t *testing.T) {
	ta := startTestApp(t)
	eq(t, ta.propose(ta.signIn(""), map[string]any{"repeat": "chosen_days", "weekdays": []int{6}}).str("startDate"), "2026-10-24")
}

func TestWaitlistKeepsTheRequest(t *testing.T) {
	ta := startTestApp(t)
	res := ta.post("/v1/waitlist", map[string]any{"request": commuteRequest(map[string]any{"preferences": []string{"woman_driver"}})}, ta.signIn(""))
	status(t, res, 201)
	eq(t, ta.count("select count(*)::int from waitlist"), 1)
}
