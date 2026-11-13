package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"commute-backend/internal/db"
	"commute-backend/internal/domain"
)

// Going live: SMS limits and delivery, the maps provider, CORS, the map style.

const phone = "9886054321"

// ---- sign-in code limits ----

func limitsApp(t *testing.T) *testApp {
	return startTestApp(t, func(c *Config) { c.OTPResendSeconds = 30; c.OTPMaxSendsPerHour = 5 })
}

func (ta *testApp) sendOTP() *resp {
	return ta.post("/v1/auth/otp", map[string]string{"phone": phone}, "")
}

func (ta *testApp) verifyOTP(code string) *resp {
	return ta.post("/v1/auth/verify", map[string]string{"phone": phone, "code": code}, "")
}

func TestOneCodeEvery30Seconds(t *testing.T) {
	ta := limitsApp(t)
	status(t, ta.sendOTP(), 204)
	again := ta.sendOTP()
	eq(t, []any{again.status, again.errCode(), again.str("message")}, []any{429, "resend_too_soon", "Wait 30 seconds before asking for another code."})
	ta.later(29 * time.Second)
	eq(t, ta.sendOTP().str("message"), "Wait 1 seconds before asking for another code.")
	ta.later(time.Second)
	status(t, ta.sendOTP(), 204)
}

func TestAtMostFiveCodesAnHour(t *testing.T) {
	ta := limitsApp(t)
	for range 5 {
		status(t, ta.sendOTP(), 204)
		ta.later(31 * time.Second)
	}
	eq(t, ta.sendOTP().errCode(), "too_many_codes")
	ta.later(time.Hour)
	status(t, ta.sendOTP(), 204)
	eq(t, len(ta.sentCodes), 6)
}

func TestWrongGuessesCountAcrossResends(t *testing.T) {
	ta := limitsApp(t)
	ta.sendOTP()
	for range 3 {
		eq(t, ta.verifyOTP("000000").errCode(), "invalid_code")
	}
	ta.later(31 * time.Second)
	ta.sendOTP()
	for range 2 {
		eq(t, ta.verifyOTP("000000").errCode(), "invalid_code")
	}
	locked := ta.verifyOTP(demoOTP)
	eq(t, []any{locked.status, locked.errCode()}, []any{429, "too_many_attempts"})
}

func TestCodesAreKeyedWithServerSecret(t *testing.T) {
	ta := limitsApp(t)
	ta.sendOTP()
	bare := sha256.Sum256([]byte(demoOTP))
	stored := ta.strings("select code_hash from otp_codes where phone = $1", phone)[0]
	if stored == hex.EncodeToString(bare[:]) {
		t.Fatal("code stored as a bare hash")
	}
	status(t, ta.verifyOTP(demoOTP), 200)
}

// ---- MSG91 ----

func TestMSG91SendsOurCodeWithTheTemplate(t *testing.T) {
	f := &fakeHTTP{reply: func(*http.Request) (int, any, error) {
		return 200, map[string]string{"type": "success", "request_id": "abc"}, nil
	}}
	err := (&MSG91{AuthKey: "key-1", TemplateID: "tmpl-1", Client: f.client()}).Send(bg, phone, "482913")
	eq(t, err, nil)
	r := f.calls[0].req
	eq(t, r.URL.Scheme+"://"+r.URL.Host+r.URL.Path, "https://control.msg91.com/api/v5/otp")
	q := r.URL.Query()
	eq(t, map[string]string{"template_id": q.Get("template_id"), "mobile": q.Get("mobile"), "otp": q.Get("otp"), "otp_expiry": q.Get("otp_expiry")},
		map[string]string{"template_id": "tmpl-1", "mobile": "91" + phone, "otp": "482913", "otp_expiry": "5"})
	eq(t, len(q), 4)
	eq(t, r.Method, "POST")
	eq(t, r.Header.Get("authkey"), "key-1")
}

func TestMSG91RefusalOrOutageIs502(t *testing.T) {
	for _, reply := range []func(*http.Request) (int, any, error){
		func(*http.Request) (int, any, error) {
			return 200, map[string]string{"type": "error", "message": "Invalid template"}, nil
		},
		func(*http.Request) (int, any, error) { return 500, map[string]any{}, nil },
		func(*http.Request) (int, any, error) { return 0, nil, errors.New("ECONNRESET") },
	} {
		f := &fakeHTTP{reply: reply}
		err := (&MSG91{AuthKey: "k", TemplateID: "t", Client: f.client()}).Send(bg, phone, "1")
		var ae *domain.ApiError
		if !errors.As(err, &ae) || ae.Status != 502 || ae.Code != "sms_failed" {
			t.Fatalf("got %v", err)
		}
	}
}

// ---- Ola Maps ----

func TestOlaPlaceSearch(t *testing.T) {
	f := &fakeHTTP{reply: func(*http.Request) (int, any, error) {
		return 200, map[string]any{
			"predictions": []any{
				map[string]any{
					"place_id":              "ola:123",
					"description":           "Forum Mall, Hosur Road, Koramangala, Bengaluru, Karnataka",
					"structured_formatting": map[string]any{"main_text": "Forum Mall", "secondary_text": "Hosur Road, Koramangala, Bengaluru, Karnataka"},
					"geometry":              map[string]any{"location": map[string]any{"lat": 12.9346, "lng": 77.6113}},
				},
				map[string]any{"place_id": "ola:no-geometry", "description": "Somewhere"},
			},
			"status": "ok",
		}, nil
	}}
	places, err := (&OlaMaps{APIKey: "ola-key", Client: f.client()}).SearchPlaces(bg, "forum", CityAreas["blr"])
	eq(t, err, nil)
	eq(t, places, []domain.Place{{ID: "ola:123", Title: "Forum Mall", Detail: "Hosur Road, Koramangala", Lat: 12.9346, Lng: 77.6113}})
	u := f.calls[0].req.URL
	eq(t, u.Path, "/places/v1/autocomplete")
	q := u.Query()
	eq(t, []string{q.Get("input"), q.Get("location"), q.Get("radius"), q.Get("strictbounds"), q.Get("api_key")},
		[]string{"forum", "12.9716,77.5946", "45000", "true", "ola-key"})
}

func TestOlaOneLetterSearchAsksNothing(t *testing.T) {
	f := &fakeHTTP{reply: func(*http.Request) (int, any, error) { return 200, map[string]any{}, nil }}
	places, _ := (&OlaMaps{APIKey: "k", Client: f.client()}).SearchPlaces(bg, "f", CityAreas["blr"])
	eq(t, places, []domain.Place{})
	eq(t, len(f.calls), 0)
}

func TestOlaTripEstimate(t *testing.T) {
	a, b := domain.LatLng{Lat: 12.9121, Lng: 77.6446}, domain.LatLng{Lat: 12.9406, Lng: 77.6964}
	f := &fakeHTTP{reply: func(*http.Request) (int, any, error) {
		return 200, map[string]any{"rows": []any{map[string]any{"elements": []any{map[string]any{"distance": 14_230, "duration": 2_710, "status": "OK"}}}}}, nil
	}}
	eq(t, must((&OlaMaps{APIKey: "k", Client: f.client()}).EstimateTrip(bg, a, b))(t), domain.TripEstimate{DistanceKm: 14.2, Minutes: 45})
	eq(t, f.calls[0].req.URL.Path, "/routing/v1/distanceMatrix")

	none := &fakeHTTP{reply: func(*http.Request) (int, any, error) {
		return 200, map[string]any{"rows": []any{map[string]any{"elements": []any{map[string]any{"status": "NO_ROUTE"}}}}}, nil
	}}
	eq(t, must((&OlaMaps{APIKey: "k", Client: none.client()}).EstimateTrip(bg, a, b))(t), domain.TripEstimate{DistanceKm: 11.9, Minutes: 38})
}

func TestOlaOutageIs502(t *testing.T) {
	f := &fakeHTTP{reply: func(*http.Request) (int, any, error) { return 503, map[string]any{}, nil }}
	_, err := (&OlaMaps{APIKey: "k", Client: f.client()}).SearchPlaces(bg, "forum", CityAreas["blr"])
	var ae *domain.ApiError
	if !errors.As(err, &ae) || ae.Code != "maps_unavailable" {
		t.Fatalf("got %v", err)
	}
}

// ---- providers from the environment ----

func TestProductionRefusesToStartWithoutSMS(t *testing.T) {
	_, err := ProvidersFromEnv(env(map[string]string{"APP_ENV": "production"}), shared, nil)
	if err == nil || !strings.Contains(err.Error(), "MSG91") {
		t.Fatalf("got %v", err)
	}
}

func TestPicksMSG91AndOlaWhenKeysAreSet(t *testing.T) {
	p, err := ProvidersFromEnv(env(map[string]string{"APP_ENV": "production", "MSG91_AUTH_KEY": "a", "MSG91_OTP_TEMPLATE_ID": "b", "OLA_MAPS_API_KEY": "c"}), shared, nil)
	eq(t, err, nil)
	eq(t, p.Maps.Name(), "ola")
	if _, ok := p.OTPSender.(*MSG91); !ok {
		t.Fatalf("sender %T", p.OTPSender)
	}
}

func TestMapsProviderDemoKeepsDemoCity(t *testing.T) {
	p, _ := ProvidersFromEnv(env(map[string]string{"OLA_MAPS_API_KEY": "c", "MAPS_PROVIDER": "demo"}), shared, nil)
	eq(t, p.Maps.Name(), "demo")
	p, _ = ProvidersFromEnv(env(nil), shared, nil)
	eq(t, p.Maps.Name(), "demo")
}

// ---- CORS ----

func TestCORSPreflightForAllowedOriginOnly(t *testing.T) {
	ta := startTestApp(t, func(c *Config) { c.CORSOrigins = []string{"https://example.github.io"} })
	ok := ta.do("OPTIONS", "/v1/home", nil, map[string]string{"origin": "https://example.github.io"})
	status(t, ok, 204)
	eq(t, ok.header.Get("access-control-allow-origin"), "https://example.github.io")
	if !strings.Contains(ok.header.Get("access-control-allow-headers"), "authorization") {
		t.Fatal("authorization not allowed")
	}
	token := ta.signIn("")
	evil := ta.do("GET", "/v1/home", nil, map[string]string{"origin": "https://evil.example", "authorization": "Bearer " + token})
	status(t, evil, 200)
	eq(t, evil.header.Get("access-control-allow-origin"), "")
}

// ---- seed-config ----

func TestSeedConfigAddsMissingNeverDeletes(t *testing.T) {
	ta := startTestApp(t)
	rider := ta.signIn("")
	ta.exec(`update fare_rules set base = 99 where ride_type = 'bike'`)
	ta.exec(`delete from places where id = 'airport'`)
	added, err := db.SeedConfig(bg, shared, true)
	eq(t, err, nil)
	eq(t, added, map[string]int64{"fareRules": 0, "rideTypeConfig": 0, "places": 1, "drivers": 0})
	eq(t, ta.count(`select base from fare_rules where ride_type = 'bike'`), 99)
	status(t, ta.get("/v1/home", rider), 200)
}

// ---- the map style for the app ----

var olaStyle = map[string]any{
	"version": 8,
	"sprite":  "https://api.olamaps.io/tiles/vector/v1/styles/default-light-standard/sprite",
	"glyphs":  "https://api.olamaps.io/tiles/vector/v1/fonts/{fontstack}/{range}.pbf",
	"sources": map[string]any{
		"openmaptiles": map[string]any{"type": "vector", "url": "https://api.olamaps.io/tiles/vector/v1/data/planet.json"},
		"vectordata":   map[string]any{"type": "vector", "url": "https://api.olamaps.io/tiles/vector/v1/data/vectordata.json"},
		"street":       map[string]any{"type": "vector", "url": "https://api.olamaps.io/tiles/vector/v1/data/street.json"},
	},
	"layers": []any{
		map[string]any{"id": "bg", "type": "background"},
		map[string]any{"id": "road", "type": "line", "source": "openmaptiles", "source-layer": "transportation"},
		map[string]any{"id": "building-3d", "type": "fill-extrusion", "source": "openmaptiles", "source-layer": "building"},
		map[string]any{"id": "ola-pois", "type": "symbol", "source": "vectordata", "source-layer": "hyperchargers"},
		map[string]any{"id": "sli", "type": "line", "source": "street", "source-layer": "street"},
	},
}

var tilejson = map[string]any{"tiles": []string{"https://api.olamaps.io/tiles/vector/v1/data/planet/{z}/{x}/{y}.pbf"}, "minzoom": 0, "maxzoom": 14}

func fakeOla() *fakeHTTP {
	return &fakeHTTP{reply: func(r *http.Request) (int, any, error) {
		switch p := r.URL.Path; {
		case strings.HasSuffix(p, "style.json"):
			return 200, olaStyle, nil
		case strings.HasSuffix(p, ".json"):
			return 200, tilejson, nil
		default:
			return 200, map[string]string{"tile": r.URL.EscapedPath()}, nil
		}
	}}
}

func paths(f *fakeHTTP) []string {
	out := []string{}
	for _, c := range f.calls {
		out = append(out, c.req.URL.EscapedPath())
	}
	return out
}

func asJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	json.Unmarshal(b, &out)
	return out
}

func TestStyleKeepsBaseMapAndHidesTheKey(t *testing.T) {
	f := fakeOla()
	style := asJSON(t, must(NewOlaMapStyle("SECRET-KEY", OlaMapStyleOptions{Client: f.client()}).Style(bg, "https://api.example.com"))(t))
	raw, _ := json.Marshal(style)
	if strings.Contains(string(raw), "SECRET-KEY") {
		t.Fatal("the key leaked into the style")
	}
	eq(t, pathIn(style, "sources"), map[string]any{"openmaptiles": map[string]any{
		"type": "vector", "minzoom": 0.0, "maxzoom": 14.0,
		"tiles": []any{"https://api.example.com/v1/map/tiles/openmaptiles/{z}/{x}/{y}.pbf"},
	}})
	eq(t, pluck(pathIn(style, "layers").([]any), "id"), []any{"bg", "road"})
	eq(t, pathIn(style, "glyphs"), "https://api.example.com/v1/map/fonts/{fontstack}/{range}.pbf")
	eq(t, pathIn(style, "sprite"), "https://api.example.com/v1/map/sprite")
	// Ola is asked with the key, and the dropped sources are never fetched.
	for _, c := range f.calls {
		eq(t, c.req.URL.Query().Get("api_key"), "SECRET-KEY")
	}
	eq(t, paths(f), []string{"/tiles/vector/v1/styles/default-light-standard/style.json", "/tiles/vector/v1/data/planet.json"})
}

func TestStyleIsFetchedOncePer6Hours(t *testing.T) {
	clock := time.Unix(0, 0)
	f := fakeOla()
	ms := NewOlaMapStyle("k", OlaMapStyleOptions{Client: f.client(), Now: func() time.Time { return clock }})
	ms.Style(bg, "https://a")
	ms.Style(bg, "https://a")
	eq(t, len(f.calls), 2)
	clock = clock.Add(7 * time.Hour)
	ms.Style(bg, "https://a")
	eq(t, len(f.calls), 4)
}

func TestTilesAreCachedImpossibleTilesRefused(t *testing.T) {
	f := fakeOla()
	ms := NewOlaMapStyle("k", OlaMapStyleOptions{Client: f.client()})
	a := must(ms.Tile(bg, "openmaptiles", 14, 11726, 7605))(t)
	eq(t, a.ContentType, "application/x-protobuf")
	eq(t, asJSON(t, json.RawMessage(a.Body)), map[string]any{"tile": "/tiles/vector/v1/data/planet/14/11726/7605.pbf"})
	before := len(f.calls)
	ms.Tile(bg, "openmaptiles", 14, 11726, 7605)
	eq(t, len(f.calls), before)
	for _, c := range []struct {
		src     string
		z, x, y int
	}{
		{"vectordata", 14, 1, 1},
		{"openmaptiles", 15, 1, 1}, // past the source's max zoom
		{"openmaptiles", 2, 4, 0},  // x out of range at z2
		{"openmaptiles", -1, 0, 0},
	} {
		_, err := ms.Tile(bg, c.src, c.z, c.x, c.y)
		var ae *domain.ApiError
		if !errors.As(err, &ae) || ae.Code != "no_tile" {
			t.Fatalf("%v: got %v", c, err)
		}
	}
}

func TestWithCacheOffEveryTileGoesToOla(t *testing.T) {
	f := fakeOla()
	off := 0.0
	ms := NewOlaMapStyle("k", OlaMapStyleOptions{Client: f.client(), CacheMB: &off})
	ms.Tile(bg, "openmaptiles", 3, 1, 1)
	ms.Tile(bg, "openmaptiles", 3, 1, 1)
	n := 0
	for _, p := range paths(f) {
		if strings.HasSuffix(p, ".pbf") {
			n++
		}
	}
	eq(t, n, 2)
}

func TestFontsPassThroughOddNamesRefused(t *testing.T) {
	f := fakeOla()
	ms := NewOlaMapStyle("k", OlaMapStyleOptions{Client: f.client()})
	must(ms.Font(bg, "Gentona Book", "0-255"))(t)
	eq(t, paths(f)[len(f.calls)-1], "/tiles/vector/v1/fonts/Gentona%20Book/0-255.pbf")
	for _, c := range [][2]string{{"../../etc", "0-255"}, {"Gentona Book", "all"}} {
		_, err := ms.Font(bg, c[0], c[1])
		var ae *domain.ApiError
		if !errors.As(err, &ae) || ae.Code != "no_font" {
			t.Fatalf("%v: got %v", c, err)
		}
	}
}

func TestMapIsServedWithoutSession(t *testing.T) {
	ta := startTestApp(t)
	status(t, ta.get("/v1/map/style.json", ""), 404)
	ta.app.MapStyle = NewOlaMapStyle("k1", OlaMapStyleOptions{Client: fakeOla().client()})
	res := ta.do("GET", "/v1/map/style.json", nil, map[string]string{"host": "api.example.com", "x-forwarded-proto": "https"})
	status(t, res, 200)
	eq(t, res.str("sprite"), "https://api.example.com/v1/map/sprite")
	tile := ta.get("/v1/map/tiles/openmaptiles/3/1/2.pbf", "")
	eq(t, []any{tile.status, tile.header.Get("content-type"), tile.header.Get("cache-control")},
		[]any{200, "application/x-protobuf", "public, max-age=86400"})
	status(t, ta.get("/v1/map/tiles/openmaptiles/3/1/2.png", ""), 404)
	status(t, ta.get("/v1/map/fonts/Gentona%20Book/0-255.pbf", ""), 200)
}

// ---- road routes for drawing ----

func TestEachRideKeepsTheRouteOfItsDirection(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	var asked []string
	ta.app.Maps = &fakeMaps{estimate: func(from, _ domain.LatLng) domain.TripEstimate {
		outbound := from.Lat < 12.92 // home is south of the office
		route := "BACK_ROUTE"
		if outbound {
			route = "OUT_ROUTE"
		}
		asked = append(asked, route)
		return domain.TripEstimate{DistanceKm: 9.5, Minutes: 20, Polyline: route}
	}}
	rider := ta.signIn("")
	proposals := ta.propose(rider, nil)
	eq(t, proposals.str("proposals.0.trip.polyline"), "OUT_ROUTE")
	ta.confirm(rider, proposals.str("proposals.0.id"), "upi")
	rides := ta.get("/v1/rides/upcoming", rider).list("rides")
	matches(t, rides[0], map[string]any{"direction": "outbound", "route": "OUT_ROUTE"})
	matches(t, rides[1], map[string]any{"direction": "return", "route": "BACK_ROUTE"})
	n := 0
	for _, a := range asked {
		if a == "BACK_ROUTE" {
			n++
		}
	}
	eq(t, n, 1)
}

// ---- JSON details the app relies on ----

func TestErrorsAndNotFoundAreJSON(t *testing.T) {
	ta := startTestApp(t)
	res := ta.get("/v1/nowhere", "")
	status(t, res, 404)
	eq(t, res.errCode(), "not_found")
	bad := ta.do("POST", "/v1/auth/verify", nil, nil)
	status(t, bad, 400)
	eq(t, bad.errCode(), "invalid_request")
	if len(bad.list("issues")) == 0 {
		t.Fatal("no issues listed")
	}
	notUUID := ta.get("/v1/rides/not-a-uuid", ta.signIn(""))
	status(t, notUUID, 400)
	eq(t, ta.get("/health", "").json(), map[string]any{"ok": true})
}
