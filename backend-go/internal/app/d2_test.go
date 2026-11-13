package app

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// Flow D2: the driver accepts or declines a commute the rider confirmed.

const (
	raviPhone   = "9000000010" // d_ravi in the seed
	sureshPhone = "9000000011" // d_suresh
)

func (ta *testApp) driverSignIn(phone string) string {
	ta.t.Helper()
	status(ta.t, ta.post("/v1/driver/auth/otp", map[string]string{"phone": phone}, ""), 204)
	res := ta.post("/v1/driver/auth/verify", map[string]string{"phone": phone, "code": demoOTP}, "")
	status(ta.t, res, 200)
	return res.str("token")
}

// confirmRavi has a rider confirm Ravi for the standard weekday commute.
// Returns the rider's token and the commute.
func (ta *testApp) confirmRavi(riderPhone string, overrides map[string]any) (string, *resp) {
	ta.t.Helper()
	token := ta.signIn(riderPhone)
	proposals := ta.propose(token, overrides).list("proposals")
	i := slices.IndexFunc(proposals, func(p any) bool { return pathIn(p, "driver.id") == "d_ravi" })
	if i < 0 {
		ta.t.Fatal("Ravi is not offered")
	}
	res := ta.confirm(token, pathIn(proposals[i], "id").(string), "upi")
	status(ta.t, res, 201)
	return token, res
}

func TestRegisteredDriverSignsIn(t *testing.T) {
	ta := startTestApp(t)
	status(t, ta.post("/v1/driver/auth/otp", map[string]string{"phone": raviPhone}, ""), 204)
	v := ta.post("/v1/driver/auth/verify", map[string]string{"phone": raviPhone, "code": demoOTP}, "")
	matches(t, v.at("driver"), map[string]any{"id": "d_ravi", "name": "Ravi Kumar"})
}

func TestUnknownNumberGetsNoCode(t *testing.T) {
	ta := startTestApp(t)
	res := ta.post("/v1/driver/auth/otp", map[string]string{"phone": "9886054321"}, "")
	status(t, res, 403)
	eq(t, res.errCode(), "not_a_driver")
	eq(t, len(ta.sentCodes), 0)
}

func TestSessionsOnlyOpenTheirOwnRoutes(t *testing.T) {
	ta := startTestApp(t)
	rider, driver := ta.signIn(""), ta.driverSignIn(raviPhone)
	status(t, ta.get("/v1/driver/requests", rider), 403)
	status(t, ta.get("/v1/home", driver), 403)
}

func TestDriverCanAlsoBeRider(t *testing.T) {
	ta := startTestApp(t)
	ta.driverSignIn(raviPhone)
	eq(t, ta.get("/v1/home", ta.signIn(raviPhone)).str("rider.phone"), raviPhone)
}

func TestConfirmedCommuteWaitsUntilRespondBy(t *testing.T) {
	ta := startTestApp(t)
	_, c := ta.confirmRavi("", nil)
	eq(t, c.str("status"), "awaiting_driver")
	// Now is Wed 9:00 IST; first pickup Thu 8:40. 12 hours (Wed 21:00 IST)
	// comes before "an hour before pickup" (Thu 7:40), so 21:00 wins.
	eq(t, c.str("respondBy"), "2026-10-21T15:30:00.000Z")
	eq(t, c.at("driver.phone"), nil, "phone shared only after accepting")
}

func TestRespondByIsAnHourBeforePickupWhenSooner(t *testing.T) {
	ta := startTestApp(t)
	ta.now = time.Date(2026, 10, 21, 15, 30, 0, 0, time.UTC) // Wed 21:00 IST
	_, c := ta.confirmRavi("", nil)
	eq(t, c.str("respondBy"), "2026-10-22T02:10:00.000Z") // Thu 7:40 IST
}

func TestDriverSeesTheRequestOthersDoNot(t *testing.T) {
	ta := startTestApp(t)
	_, c := ta.confirmRavi("", nil)
	ravi, suresh := ta.driverSignIn(raviPhone), ta.driverSignIn(sureshPhone)
	mine := ta.get("/v1/driver/requests", ravi).list("requests")
	eq(t, len(mine), 1)
	matches(t, mine[0], map[string]any{"id": c.str("id"), "riderName": "New rider", "fare": 250, "pickupTime": 520, "startDate": "2026-10-22"})
	eq(t, ta.get("/v1/driver/requests", suresh).list("requests"), []any{})
}

func TestAwaitingCommuteHoldsTheSlot(t *testing.T) {
	ta := startTestApp(t)
	ta.confirmRavi("9886054321", nil)
	ids := pluck(ta.propose(ta.signIn("9886054322"), nil).list("proposals"), "driver.id")
	if slices.Contains(ids, any("d_ravi")) {
		t.Fatal("Ravi offered while holding the slot")
	}
}

func TestDriverAcceptsHomeShowsActive(t *testing.T) {
	ta := startTestApp(t)
	token, c := ta.confirmRavi("", nil)
	ravi := ta.driverSignIn(raviPhone)
	eq(t, ta.post("/v1/driver/commutes/"+c.str("id")+"/accept", nil, ravi).json(), map[string]any{"id": c.str("id"), "status": "active"})
	home := ta.get("/v1/home", token)
	eq(t, home.str("commute.status"), "active")
	eq(t, home.str("commute.driver.phone"), raviPhone)
	eq(t, ta.get("/v1/driver/requests", ravi).list("requests"), []any{})
}

func TestAnsweringTwiceIsConflict(t *testing.T) {
	ta := startTestApp(t)
	_, c := ta.confirmRavi("", nil)
	ravi := ta.driverSignIn(raviPhone)
	ta.post("/v1/driver/commutes/"+c.str("id")+"/accept", nil, ravi)
	again := ta.post("/v1/driver/commutes/"+c.str("id")+"/decline", nil, ravi)
	status(t, again, 409)
	eq(t, again.errCode(), "already_answered")
}

func TestCannotAnswerAnotherDriversRequest(t *testing.T) {
	ta := startTestApp(t)
	_, c := ta.confirmRavi("", nil)
	status(t, ta.post("/v1/driver/commutes/"+c.str("id")+"/accept", nil, ta.driverSignIn(sureshPhone)), 404)
}

func TestDeclineShowsOnHomeAndDriverDropsOut(t *testing.T) {
	ta := startTestApp(t)
	token, c := ta.confirmRavi("", nil)
	eq(t, ta.post("/v1/driver/commutes/"+c.str("id")+"/decline", nil, ta.driverSignIn(raviPhone)).str("status"), "declined")
	matches(t, ta.get("/v1/home", token).at("commute"), map[string]any{"status": "declined", "declineReason": "declined"})
	again := pluck(ta.propose(token, nil).list("proposals"), "driver.id")
	if len(again) == 0 || slices.Contains(again, any("d_ravi")) {
		t.Fatalf("proposals after decline: %v", again)
	}
}

func TestDeclineFreesTheSlot(t *testing.T) {
	ta := startTestApp(t)
	_, c := ta.confirmRavi("9886054321", nil)
	ta.post("/v1/driver/commutes/"+c.str("id")+"/decline", nil, ta.driverSignIn(raviPhone))
	eq(t, ta.propose(ta.signIn("9886054322"), nil).str("proposals.0.driver.id"), "d_ravi")
}

func TestNewDriverAfterDeclineIsTheCommuteOnHome(t *testing.T) {
	ta := startTestApp(t)
	token, c := ta.confirmRavi("", nil)
	ta.post("/v1/driver/commutes/"+c.str("id")+"/decline", nil, ta.driverSignIn(raviPhone))
	res := ta.confirm(token, ta.propose(token, nil).str("proposals.0.id"), "upi")
	status(t, res, 201)
	home := ta.get("/v1/home", token)
	eq(t, home.str("commute.id"), res.str("id"))
	eq(t, home.str("commute.status"), "awaiting_driver")
}

func TestRequestLapsesAfterRespondBy(t *testing.T) {
	ta := startTestApp(t)
	token, c := ta.confirmRavi("", nil)
	ta.now = time.Date(2026, 10, 21, 15, 30, 1, 0, time.UTC) // a second after respondBy
	matches(t, ta.get("/v1/home", token).at("commute"), map[string]any{"status": "declined", "declineReason": "no_answer"})
	res := ta.post("/v1/driver/commutes/"+c.str("id")+"/accept", nil, ta.driverSignIn(raviPhone))
	status(t, res, 410)
	eq(t, res.errCode(), "request_expired")
}

func TestJustBeforeRespondByDriverCanAccept(t *testing.T) {
	ta := startTestApp(t)
	_, c := ta.confirmRavi("", nil)
	ta.now = time.Date(2026, 10, 21, 15, 29, 59, 0, time.UTC)
	eq(t, ta.post("/v1/driver/commutes/"+c.str("id")+"/accept", nil, ta.driverSignIn(raviPhone)).str("status"), "active")
}

func TestSimulatorIsOffUnlessSwitchedOn(t *testing.T) {
	ta := startTestApp(t)
	status(t, ta.get("/dev/sim/requests", ""), 404)
}

func TestSimulatorActsAsTheAskedDriver(t *testing.T) {
	ta := startTestApp(t, func(c *Config) { c.EnableDriverSim = true })
	aTok, a := ta.confirmRavi("9886054321", nil)
	bTok, b := ta.confirmRavi("9886054322", map[string]any{"time": 600, "returnTime": 1170})
	cTok, c := ta.confirmRavi("9886054323", map[string]any{"time": 680, "returnTime": 1230})
	eq(t, pluck(ta.get("/dev/sim/requests", "").list("requests"), "id"), []any{a.str("id"), b.str("id"), c.str("id")})

	eq(t, ta.post("/dev/sim/commutes/"+a.str("id")+"/accept", nil, "").str("status"), "active")
	eq(t, ta.post("/dev/sim/commutes/"+b.str("id")+"/decline", nil, "").str("status"), "declined")
	eq(t, ta.post("/dev/sim/commutes/"+c.str("id")+"/expire", nil, "").str("status"), "declined")

	eq(t, ta.get("/v1/home", aTok).str("commute.status"), "active")
	eq(t, ta.get("/v1/home", bTok).str("commute.declineReason"), "declined")
	eq(t, ta.get("/v1/home", cTok).str("commute.declineReason"), "no_answer")
}

// ---- production guard ----

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func configErr(vars map[string]string) string {
	if _, err := ConfigFromEnv(env(vars)); err != nil {
		return err.Error()
	}
	return ""
}

func TestRefusesDemoSwitchesInProduction(t *testing.T) {
	prod := func(extra map[string]string) map[string]string {
		m := map[string]string{"APP_ENV": "production", "OTP_SECRET": strings.Repeat("x", 32)}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	for name, vars := range map[string]map[string]string{
		"FIXED_OTP":            {"FIXED_OTP": "123456"},
		"AUTO_ACCEPT_COMMUTES": {"AUTO_ACCEPT_COMMUTES": "true"},
		"ENABLE_DRIVER_SIM":    {"ENABLE_DRIVER_SIM": "true"},
	} {
		if !strings.Contains(configErr(prod(vars)), name) {
			t.Fatalf("%s accepted in production", name)
		}
	}
	c, err := ConfigFromEnv(env(prod(nil)))
	eq(t, err, nil)
	eq(t, []bool{c.AutoAcceptCommutes, c.EnableDriverSim}, []bool{false, false})
	// The Node backend's NODE_ENV still counts.
	if !strings.Contains(configErr(map[string]string{"NODE_ENV": "production", "OTP_SECRET": strings.Repeat("x", 32), "FIXED_OTP": "1"}), "FIXED_OTP") {
		t.Fatal("NODE_ENV=production ignored")
	}
}

func TestRefusesProductionWithoutStrongOTPSecret(t *testing.T) {
	if !strings.Contains(configErr(map[string]string{"APP_ENV": "production"}), "OTP_SECRET") {
		t.Fatal("no secret accepted")
	}
	if !strings.Contains(configErr(map[string]string{"APP_ENV": "production", "OTP_SECRET": "short"}), "OTP_SECRET") {
		t.Fatal("short secret accepted")
	}
}

func TestAutoAcceptIsOffUnlessAskedFor(t *testing.T) {
	c, _ := ConfigFromEnv(env(nil))
	eq(t, c.AutoAcceptCommutes, false)
	c, _ = ConfigFromEnv(env(map[string]string{"AUTO_ACCEPT_COMMUTES": "true"}))
	eq(t, c.AutoAcceptCommutes, true)
}
