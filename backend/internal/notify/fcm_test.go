package notify

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
	"reflect"
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
	m := Message{Title: "Ravi is on the way", Body: "Track your ride live.", Data: map[string]string{"kind": "driver_on_the_way", "rideId": "ride-1"}}
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
	eq[any](t, body, map[string]any{"message": map[string]any{
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
	m := Message{Title: "t", Body: "b", Data: map[string]string{"kind": "leg_is_on"}}
	if err := fcm.Send(bg, "gone", m); !errors.Is(err, ErrUnregistered) {
		t.Fatalf("got %v", err)
	}
	err := fcm.Send(bg, "flaky", m)
	if err == nil || errors.Is(err, ErrUnregistered) || !strings.Contains(err.Error(), "503") {
		t.Fatalf("got %v", err)
	}
}

func TestPushReturnsDeadTokens(t *testing.T) {
	fcm, _, _ := fakeFirebase(t, func(token string) (int, any) {
		if token == "gone" {
			return 404, map[string]any{"error": map[string]any{"status": "NOT_FOUND",
				"details": []any{map[string]any{"errorCode": "UNREGISTERED"}}}}
		}
		return ok200(token)
	})
	dead, err := fcm.Push(bg, []string{"gone", "fine"}, Message{Title: "t", Body: "b"})
	eq(t, err, nil)
	eq(t, dead, []string{"gone"})
}

func TestPusherFromEnv(t *testing.T) {
	p, err := PusherFromEnv(func(string) string { return "" }, testLog())
	eq(t, err, nil)
	if _, ok := p.(LogPusher); !ok {
		t.Fatalf("got %T", p)
	}
	_, err = PusherFromEnv(func(k string) string {
		if k == "FCM_SERVICE_ACCOUNT_JSON" {
			return `{"project_id": "x"}`
		}
		return ""
	}, testLog())
	if err == nil {
		t.Fatal("want an error for an incomplete service account")
	}
}

// ---------------------------------------------------------------- helpers

var bg = context.Background()

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func eq[T any](t *testing.T, got, want T, what ...any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v: got %#v, want %#v", what, got, want)
	}
}

func matches(t *testing.T, got map[string]any, want map[string]any) {
	t.Helper()
	for k, w := range want {
		g := got[k]
		if f, ok := g.(float64); ok {
			if n, ok := w.(int64); ok {
				w = float64(n)
			}
			g = f
		}
		if !reflect.DeepEqual(g, w) {
			t.Fatalf("%s: got %#v, want %#v", k, g, w)
		}
	}
}

type call struct {
	req  *http.Request
	body string
}

// fakeHTTP answers every request with reply and logs it.
type fakeHTTP struct {
	calls []call
	reply func(r *http.Request) (int, any, error)
}

func (f *fakeHTTP) RoundTrip(r *http.Request) (*http.Response, error) {
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
	}
	f.calls = append(f.calls, call{r, string(body)})
	code, v, err := f.reply(r)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(v)
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(string(raw))), Header: http.Header{}, Request: r}, nil
}

func (f *fakeHTTP) client() *http.Client { return &http.Client{Transport: f} }
