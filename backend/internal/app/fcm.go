package app

import (
	"bytes"
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
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// FCM sends pushes through Firebase Cloud Messaging's HTTP v1 API, signed
// in as a Firebase service account (Firebase console → Project settings →
// Service accounts → Generate new private key; put the whole JSON file in
// FCM_SERVICE_ACCOUNT_JSON).
//
// Android shows the notification itself while the app is in the
// background; the data {kind, rideId} tells the app which ride to open.
type FCM struct {
	account serviceAccount
	client  *http.Client
	now     func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

type serviceAccount struct {
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`

	key *rsa.PrivateKey
}

// RideUpdatesChannel is the rider app's Android notification channel for
// ride alerts; the app creates it at high importance.
const RideUpdatesChannel = "ride_updates"

// ErrUnregistered means FCM no longer knows the device token (the app was
// uninstalled, or the token was replaced): stop sending to it.
var ErrUnregistered = errors.New("push token is no longer registered")

// NewFCM reads a service account key file's JSON.
func NewFCM(serviceAccountJSON []byte, client *http.Client) (*FCM, error) {
	var a serviceAccount
	if err := json.Unmarshal(serviceAccountJSON, &a); err != nil {
		return nil, fmt.Errorf("FCM service account: %w", err)
	}
	if a.ProjectID == "" || a.ClientEmail == "" || a.PrivateKey == "" {
		return nil, errors.New("FCM service account needs project_id, client_email and private_key")
	}
	if a.TokenURI == "" {
		a.TokenURI = "https://oauth2.googleapis.com/token"
	}
	block, _ := pem.Decode([]byte(a.PrivateKey))
	if block == nil {
		return nil, errors.New("FCM service account: private_key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("FCM service account key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("FCM service account key is not RSA")
	}
	a.key = key
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &FCM{account: a, client: client, now: time.Now}, nil
}

// accessToken is a Google OAuth token for FCM, made from a signed JWT and
// reused until five minutes before it expires.
func (f *FCM) accessToken(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	if f.token != "" && now.Before(f.expires.Add(-5*time.Minute)) {
		return f.token, nil
	}
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	unsigned := enc(map[string]string{"alg": "RS256", "typ": "JWT"}) + "." + enc(map[string]any{
		"iss":   f.account.ClientEmail,
		"scope": "https://www.googleapis.com/auth/firebase.messaging",
		"aud":   f.account.TokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	digest := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.account.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {unsigned + "." + base64.RawURLEncoding.EncodeToString(sig)},
	}
	req, err := http.NewRequestWithContext(ctx, "POST", f.account.TokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	res, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("FCM sign-in: %w", err)
	}
	defer res.Body.Close()
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error_description"`
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	_ = json.Unmarshal(raw, &body)
	if res.StatusCode >= 300 || body.AccessToken == "" {
		return "", fmt.Errorf("FCM sign-in: %d %s", res.StatusCode, body.Error)
	}
	f.token = body.AccessToken
	f.expires = now.Add(time.Duration(body.ExpiresIn) * time.Second)
	return f.token, nil
}

// Send sends one push to one device.
func (f *FCM) Send(ctx context.Context, deviceToken string, m PushMessage) error {
	access, err := f.accessToken(ctx)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"message": map[string]any{
		"token":        deviceToken,
		"notification": map[string]string{"title": m.Title, "body": m.Body},
		"data":         map[string]string{"kind": m.Data.Kind, "rideId": m.Data.RideID},
		// Ride updates are time-sensitive: deliver at once, even in Doze, and
		// show them in the app's high-importance channel, which Android pops
		// up over the screen (MainActivity.kt creates it).
		"android": map[string]any{
			"priority":     "high",
			"notification": map[string]string{"channel_id": RideUpdatesChannel},
		},
	}})
	endpoint := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", url.PathEscape(f.account.ProjectID))
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("authorization", "Bearer "+access)
	req.Header.Set("content-type", "application/json")
	res, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("FCM send: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 300 {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	var body struct {
		Error struct {
			Status  string `json:"status"`
			Message string `json:"message"`
			Details []struct {
				ErrorCode string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &body)
	for _, d := range body.Error.Details {
		if d.ErrorCode == "UNREGISTERED" {
			return ErrUnregistered
		}
	}
	if res.StatusCode == 404 {
		return ErrUnregistered
	}
	if res.StatusCode == 401 {
		// The access token may have been revoked early: sign in afresh next time.
		f.mu.Lock()
		f.token = ""
		f.mu.Unlock()
	}
	return fmt.Errorf("FCM send: %d %s %s", res.StatusCode, body.Error.Status, body.Error.Message)
}

// PushSenderFromEnv is FCM when FCM_SERVICE_ACCOUNT_JSON is set, else a
// sender that only logs (development, and production before Firebase is
// set up: riders then see changes in the app within 5 seconds).
func PushSenderFromEnv(getenv func(string) string, log *slog.Logger) (PushSender, error) {
	raw := strings.TrimSpace(getenv("FCM_SERVICE_ACCOUNT_JSON"))
	if raw == "" {
		if IsProduction(getenv) {
			log.Warn("FCM_SERVICE_ACCOUNT_JSON is not set: push notifications are only logged")
		}
		return LogPushSender(log), nil
	}
	f, err := NewFCM([]byte(raw), nil)
	if err != nil {
		return nil, err
	}
	log.Info("push notifications go through FCM", "project", f.account.ProjectID)
	return f, nil
}
