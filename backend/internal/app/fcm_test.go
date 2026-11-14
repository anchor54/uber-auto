package app

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// A throwaway service account, and a fake of Google's token and FCM endpoints.
func fakeFirebase(t *testing.T, sendStatus func(token string) (int, any)) (*FCM, *fakeHTTP, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	account, _ := json.Marshal(map[string]string{
		"type":         "service_account",
		"project_id":   "commute-test",
		"client_email": "push@commute-test.iam.gserviceaccount.com",
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"token_uri":    "https://oauth2.googleapis.com/token",
	})
	f := &fakeHTTP{}
	f.reply = func(r *http.Request) (int, any, error) {
		if r.URL.Host == "oauth2.googleapis.com" {
			return 200, map[string]any{"access_token": "ya29.test", "expires_in": 3600, "token_type": "Bearer"}, nil
		}
		// The fake has already read the body into its call log.
		var body struct {
			Message struct{ Token string } `json:"message"`
		}
		json.Unmarshal([]byte(f.calls[len(f.calls)-1].body), &body)
		code, v := sendStatus(body.Message.Token)
		return code, v, nil
	}
	fcm, err := NewFCM(account, f.client())
	if err != nil {
		t.Fatal(err)
	}
	return fcm, f, key
}

func ok200(string) (int, any) {
	return 200, map[string]string{"name": "projects/commute-test/messages/1"}
}

func TestFCMSignsInOnceAndSendsTheMessage(t *testing.T) {
	fcm, f, key := fakeFirebase(t, ok200)
	clock := time.Date(2026, 10, 22, 3, 0, 0, 0, time.UTC)
	fcm.now = func() time.Time { return clock }
	m := PushMessage{"Ravi is on the way", "Track your ride live.", PushData{"driver_on_the_way", "ride-1"}}
	for range 2 {
		eq(t, fcm.Send(bg, "device-token-1", m), nil)
	}
	eq(t, len(f.calls), 3, "one sign-in, two sends")

	// The sign-in: a JWT for the service account, signed with its key.
	form, _ := url.ParseQuery(f.calls[0].body)
	eq(t, form.Get("grant_type"), "urn:ietf:params:oauth:grant-type:jwt-bearer")
	parts := strings.Split(form.Get("assertion"), ".")
	eq(t, len(parts), 3)
	claimsJSON, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims map[string]any
	json.Unmarshal(claimsJSON, &claims)
	matches(t, claims, map[string]any{
		"iss": "push@commute-test.iam.gserviceaccount.com", "aud": "https://oauth2.googleapis.com/token",
		"scope": "https://www.googleapis.com/auth/firebase.messaging", "iat": clock.Unix(), "exp": clock.Add(time.Hour).Unix(),
	})
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	eq(t, rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sig), nil, "signature")

	// The send.
	send := f.calls[1]
	eq(t, send.req.URL.String(), "https://fcm.googleapis.com/v1/projects/commute-test/messages:send")
	eq(t, send.req.Header.Get("authorization"), "Bearer ya29.test")
	var body any
	json.Unmarshal([]byte(send.body), &body)
	eq(t, body, map[string]any{"message": map[string]any{
		"token":        "device-token-1",
		"notification": map[string]any{"title": "Ravi is on the way", "body": "Track your ride live."},
		"data":         map[string]any{"kind": "driver_on_the_way", "rideId": "ride-1"},
		"android":      map[string]any{"priority": "high", "notification": map[string]any{"channel_id": "ride_updates"}},
	}})

	// Near expiry it signs in again.
	clock = clock.Add(56 * time.Minute)
	fcm.Send(bg, "device-token-1", m)
	eq(t, len(f.calls), 5)
}

func TestFCMUnregisteredTokenAndOtherFailures(t *testing.T) {
	fcm, _, _ := fakeFirebase(t, func(token string) (int, any) {
		switch token {
		case "gone":
			return 404, map[string]any{"error": map[string]any{"status": "NOT_FOUND",
				"details": []any{map[string]any{"errorCode": "UNREGISTERED"}}}}
		default:
			return 503, map[string]any{"error": map[string]any{"status": "UNAVAILABLE", "message": "try later"}}
		}
	})
	m := PushMessage{"t", "b", PushData{"leg_is_on", "r"}}
	if err := fcm.Send(bg, "gone", m); !errors.Is(err, ErrUnregistered) {
		t.Fatalf("got %v", err)
	}
	err := fcm.Send(bg, "flaky", m)
	if err == nil || errors.Is(err, ErrUnregistered) || !strings.Contains(err.Error(), "503") {
		t.Fatalf("got %v", err)
	}
}

func TestPushSenderFromEnv(t *testing.T) {
	s, err := PushSenderFromEnv(env(nil), testLog())
	eq(t, err, nil)
	if _, isFCM := s.(*FCM); isFCM {
		t.Fatal("no account should mean the log sender")
	}
	_, err = PushSenderFromEnv(env(map[string]string{"FCM_SERVICE_ACCOUNT_JSON": `{"project_id": "x"}`}), testLog())
	if err == nil {
		t.Fatal("an incomplete account should be refused")
	}
}

// A device whose app is gone is forgotten, and the rider's other devices still get the push.
func TestUnregisteredDeviceIsForgotten(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c := ta.commuter("", nil)
	for _, token := range []string{"token-old-phone", "token-new-phone"} {
		status(t, ta.put("/v1/devices", map[string]string{"pushToken": token, "platform": "android"}, c.rider), 204)
	}
	ta.departed(c.first.ID, c.first.Pickup)
	var got []string
	sender := PushSenderFunc(func(_ context.Context, token string, _ PushMessage) error {
		if token == "token-old-phone" {
			return ErrUnregistered
		}
		got = append(got, token)
		return nil
	})
	eq(t, must(ta.app.SendDueNotifications(bg, sender))(t), 1)
	eq(t, got, []string{"token-new-phone"})
	eq(t, ta.strings("select push_token from devices order by push_token"), []string{"token-new-phone"})
}

func TestRideTrackIsKept30Days(t *testing.T) {
	ta := startTestApp(t, autoAccept)
	c := ta.commuter("", nil)
	driver := ta.departed(c.first.ID, c.first.Pickup)
	ta.sendLocation(driver, c.first.Pickup.Lat+0.01, c.first.Pickup.Lng) // one track point at 8:20 Thu 22 Oct
	eq(t, ta.count("select count(*)::int from ride_track"), 1)

	ta.now = thu("08:20").Add(29 * 24 * time.Hour)
	eq(t, must(ta.app.PruneRideTrack(bg))(t), 0, "29 days old: kept")
	ta.now = ta.now.Add(2 * time.Hour)
	eq(t, must(ta.app.PruneRideTrack(bg))(t), 0, "runs once a day")
	ta.now = thu("08:20").Add(31 * 24 * time.Hour)
	eq(t, must(ta.app.PruneRideTrack(bg))(t), 1, "31 days old: deleted")
	eq(t, ta.count("select count(*)::int from ride_track"), 0)
}

func TestPlayTakesTheApproachLength(t *testing.T) {
	ta := startTestApp(t, func(c *Config) { c.EnableDriverSim = true })
	// No such ride, so nothing keeps playing in the background after the test.
	const nobody = "00000000-0000-4000-8000-000000000000"
	status(t, ta.post("/dev/sim/rides/"+nobody+"/play", map[string]any{"speed": 120, "approachMinutes": 500}, ""), 400)
	res := ta.post("/dev/sim/rides/"+nobody+"/play", map[string]any{"speed": 120, "approachMinutes": 30, "waitMinutes": 2}, "")
	status(t, res, 202)
	eq(t, res.json(), map[string]any{"playing": true, "speed": 120.0})
}

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
