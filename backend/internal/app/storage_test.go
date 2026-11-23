package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// ---- the in-memory store's signed links ----

func memUpload(t *testing.T, h http.Handler, uploadURL, contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	u, err := url.Parse(uploadURL)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("PUT", u.RequestURI(), strings.NewReader(string(body)))
	req.Header.Set("content-type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func memStore(now *time.Time) (*MemStorage, *App) {
	m := NewMemStorage("http://api.test", "secret")
	m.Clock = func() time.Time { return *now }
	return m, &App{Storage: m, Clock: func() time.Time { return *now }}
}

func TestMemStorageUploadsAndReadsThroughSignedLinks(t *testing.T) {
	now := time.Date(2026, 11, 21, 4, 30, 0, 0, time.UTC)
	m, a := memStore(&now)
	h := a.Handler()
	ctx := context.Background()
	path := "driver-docs/drv_1/licence"

	up, _ := m.SignUpload(ctx, path, "image/png", 10*time.Minute)
	if !strings.HasPrefix(up, "http://api.test/storage/upload?") {
		t.Fatalf("upload url %s", up)
	}
	ok, _ := m.Exists(ctx, path)
	eq(t, ok, false)
	rec := memUpload(t, h, up, "image/png", []byte("png-bytes"))
	eq(t, rec.Code, 200, rec.Body.String())
	ok, _ = m.Exists(ctx, path)
	eq(t, ok, true)

	read, _ := m.SignRead(ctx, path, 10*time.Minute)
	u, _ := url.Parse(read)
	get := httptest.NewRecorder()
	h.ServeHTTP(get, httptest.NewRequest("GET", u.RequestURI(), nil))
	eq(t, get.Code, 200)
	eq(t, get.Body.String(), "png-bytes")
	eq(t, get.Header().Get("content-type"), "image/png")

	// Uploading again overwrites.
	eq(t, memUpload(t, h, up, "image/png", []byte("second")).Code, 200)
	data, _, _ := m.Get(path)
	eq(t, string(data), "second")
}

func TestMemStorageRefusesBadUploads(t *testing.T) {
	now := time.Date(2026, 11, 21, 4, 30, 0, 0, time.UTC)
	m, a := memStore(&now)
	h := a.Handler()
	ctx := context.Background()
	path := "driver-docs/drv_1/rc"
	up, _ := m.SignUpload(ctx, path, "image/jpeg", 10*time.Minute)

	code := func(rec *httptest.ResponseRecorder) string {
		var body struct{ Error string }
		json.Unmarshal(rec.Body.Bytes(), &body)
		return body.Error
	}
	// Another content type than the one signed.
	rec := memUpload(t, h, up, "image/png", []byte("x"))
	eq(t, rec.Code, 415)
	// A tampered path.
	rec = memUpload(t, h, strings.Replace(up, "drv_1", "drv_2", 1), "image/jpeg", []byte("x"))
	eq(t, rec.Code, 403)
	eq(t, code(rec), "bad_signature")
	// Too big.
	rec = memUpload(t, h, up, "image/jpeg", make([]byte, MaxUploadBytes+1))
	eq(t, rec.Code, 413)
	eq(t, code(rec), "file_too_large")
	// Not a photo, even when signed for it.
	gif, _ := m.SignUpload(ctx, path, "image/gif", 10*time.Minute)
	eq(t, memUpload(t, h, gif, "image/gif", []byte("x")).Code, 415)
	// Expired.
	now = now.Add(11 * time.Minute)
	rec = memUpload(t, h, up, "image/jpeg", []byte("x"))
	eq(t, rec.Code, 403)
	eq(t, code(rec), "link_expired")
	ok, _ := m.Exists(ctx, path)
	eq(t, ok, false, "nothing was stored")

	// A read link can't be used to upload, nor an upload link to read.
	now = now.Add(-11 * time.Minute)
	m.Put(path, []byte("x"))
	read, _ := m.SignRead(ctx, path, time.Minute)
	eq(t, memUpload(t, h, strings.Replace(read, "/storage/object", "/storage/upload", 1)+"&type=image/jpeg", "image/jpeg", []byte("y")).Code, 403)
	u, _ := url.Parse(strings.Replace(up, "/storage/upload", "/storage/object", 1))
	get := httptest.NewRecorder()
	h.ServeHTTP(get, httptest.NewRequest("GET", u.RequestURI(), nil))
	eq(t, get.Code, 403)
}

func TestLazyMemStorageLinksUseTheRequestHost(t *testing.T) {
	a := &App{Clock: time.Now}
	m, ok := a.storage().(*MemStorage)
	if !ok {
		t.Fatalf("storage %T", a.storage())
	}
	u, _ := m.SignUpload(context.Background(), "driver-docs/d/x", "image/jpeg", time.Minute)
	if !strings.HasPrefix(u, "/storage/upload?") {
		t.Fatalf("got %s", u)
	}
	req := httptest.NewRequest("POST", "/", nil)
	req.Host = "10.0.2.2:8080"
	eq(t, absoluteURL(req, u), "http://10.0.2.2:8080"+u)
	req.Header.Set("x-forwarded-proto", "https")
	eq(t, absoluteURL(req, "https://x.supabase.co/a"), "https://x.supabase.co/a")
	if !strings.HasPrefix(absoluteURL(req, u), "https://") {
		t.Fatal("forwarded https ignored")
	}
}

func TestStorageRoutesAreOffWithSupabase(t *testing.T) {
	a := &App{Storage: &SupabaseStorage{URL: "https://x.supabase.co"}, Clock: time.Now}
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/storage/object?path=x", nil))
	eq(t, rec.Code, 404)
}

// ---- Supabase Storage's REST API ----

func TestSupabaseStorageRequests(t *testing.T) {
	f := &fakeHTTP{reply: func(r *http.Request) (int, any, error) {
		switch {
		case strings.Contains(r.URL.Path, "/object/upload/sign/"):
			return 200, map[string]string{"url": "/object/upload/sign/driver-docs/drv_1/licence?token=up"}, nil
		case strings.Contains(r.URL.Path, "/object/sign/"):
			return 200, map[string]string{"signedURL": "/object/sign/driver-docs/drv_1/licence?token=rd"}, nil
		case r.Method == "HEAD" && strings.HasSuffix(r.URL.Path, "/licence"):
			return 200, nil, nil
		case r.Method == "HEAD":
			return 400, map[string]string{"statusCode": "404", "error": "not_found"}, nil
		}
		return 500, nil, nil
	}}
	s := &SupabaseStorage{URL: "https://proj.supabase.co/", ServiceRoleKey: "service-key", HTTP: f.client()}
	ctx := context.Background()

	up, err := s.SignUpload(ctx, "driver-docs/drv_1/licence", "image/jpeg", 10*time.Minute)
	eq(t, err, nil)
	eq(t, up, "https://proj.supabase.co/storage/v1/object/upload/sign/driver-docs/drv_1/licence?token=up")
	c := f.calls[0]
	eq(t, c.req.Method, "POST")
	eq(t, c.req.URL.String(), "https://proj.supabase.co/storage/v1/object/upload/sign/driver-docs/drv_1/licence")
	eq(t, c.req.Header.Get("authorization"), "Bearer service-key")
	eq(t, c.req.Header.Get("apikey"), "service-key")
	eq(t, c.req.Header.Get("x-upsert"), "true")

	read, err := s.SignRead(ctx, "driver-docs/drv_1/licence", 10*time.Minute)
	eq(t, err, nil)
	eq(t, read, "https://proj.supabase.co/storage/v1/object/sign/driver-docs/drv_1/licence?token=rd")
	c = f.calls[1]
	eq(t, c.req.URL.String(), "https://proj.supabase.co/storage/v1/object/sign/driver-docs/drv_1/licence")
	eq(t, c.body, `{"expiresIn":600}`)

	ok, err := s.Exists(ctx, "driver-docs/drv_1/licence")
	eq(t, err, nil)
	eq(t, ok, true)
	eq(t, f.calls[2].req.Method, "HEAD")
	eq(t, f.calls[2].req.URL.String(), "https://proj.supabase.co/storage/v1/object/driver-docs/drv_1/licence")
	ok, err = s.Exists(ctx, "driver-docs/drv_1/rc")
	eq(t, err, nil)
	eq(t, ok, false)
}

func TestSupabaseStorageErrors(t *testing.T) {
	f := &fakeHTTP{reply: func(r *http.Request) (int, any, error) {
		return 500, map[string]string{"error": "boom"}, nil
	}}
	s := &SupabaseStorage{URL: "https://proj.supabase.co", ServiceRoleKey: "k", HTTP: f.client()}
	ctx := context.Background()
	if _, err := s.SignUpload(ctx, "driver-docs/a/b", "image/png", time.Minute); err == nil {
		t.Fatal("upload sign error swallowed")
	}
	if _, err := s.SignRead(ctx, "driver-docs/a/b", time.Minute); err == nil {
		t.Fatal("read sign error swallowed")
	}
	if _, err := s.Exists(ctx, "driver-docs/a/b"); err == nil {
		t.Fatal("exists error swallowed")
	}
}

// ---- config and providers ----

func TestStorageFromEnv(t *testing.T) {
	p, err := ProvidersFromEnv(env(map[string]string{"SUPABASE_URL": "https://x.supabase.co", "SUPABASE_SERVICE_ROLE_KEY": "k"}), shared, nil)
	eq(t, err, nil)
	if s, ok := p.Storage.(*SupabaseStorage); !ok || s.Bucket != "driver-docs" {
		t.Fatalf("storage %#v", p.Storage)
	}
	p, _ = ProvidersFromEnv(env(nil), shared, nil)
	eq(t, p.Storage, nil)
	_, err = ProvidersFromEnv(env(map[string]string{"APP_ENV": "production", "MSG91_AUTH_KEY": "a", "MSG91_OTP_TEMPLATE_ID": "b"}), shared, nil)
	if err == nil || !strings.Contains(err.Error(), "SUPABASE_URL") {
		t.Fatalf("got %v", err)
	}
}

func TestAdminPhonesAndPlatformUPIFromEnv(t *testing.T) {
	c, err := ConfigFromEnv(env(map[string]string{"ADMIN_PHONES": " 9000000099, 9876543210 ,", "PLATFORM_UPI_ID": "plans@okhdfc"}))
	eq(t, err, nil)
	eq(t, c.AdminPhones, []string{"9000000099", "9876543210"})
	eq(t, c.PlatformUPIID, "plans@okhdfc")
	c, _ = ConfigFromEnv(env(nil))
	eq(t, c.AdminPhones, []string{})
	eq(t, c.PlatformUPIID, "commute.plans@okaxis")
	_, err = ConfigFromEnv(env(map[string]string{"ADMIN_PHONES": "12345"}))
	if err == nil {
		t.Fatal("bad admin phone accepted")
	}
	_, err = ConfigFromEnv(env(map[string]string{"PLATFORM_UPI_ID": "not-upi"}))
	if err == nil {
		t.Fatal("bad UPI accepted")
	}
}

// ---- messages ----

func TestD1MessageTexts(t *testing.T) {
	cases := []struct {
		kind        string
		payload     map[string]any
		title, body string
	}{
		{"items_rejected", map[string]any{"items": "Driving licence, Insurance"}, "A few things need fixing",
			"A few things need fixing: Driving licence, Insurance. Open the app to see why."},
		{"driver_live", map[string]any{"vehicleType": "cab"}, "You're live",
			"You're live. New requests for cab drivers will show up in Requests."},
		{"admin_driver_submitted", map[string]any{"name": "Ravi Kumar"}, "Driver waiting", "Ravi Kumar is waiting for review"},
		{"admin_payment_claimed", map[string]any{"name": "Ravi Kumar", "amount": float64(267)}, "Payment claimed",
			"Ravi Kumar says they've paid ₹267"},
	}
	for _, c := range cases {
		title, body := MessageFor(c.kind, c.payload)
		eq(t, title, c.title, c.kind)
		eq(t, body, c.body, c.kind)
	}
}

func TestDevicesBeforeRegisteringAndForAdmins(t *testing.T) {
	ta := startTestApp(t, func(c *Config) { c.AdminPhones = []string{"9000000099"} })
	// Signed in as a driver, not registered yet: nothing to key the device by.
	status(t, ta.post("/v1/driver/auth/otp", map[string]string{"phone": "9811100001"}, ""), 204)
	driver := ta.post("/v1/driver/auth/verify", map[string]string{"phone": "9811100001", "code": demoOTP}, "").str("token")
	res := ta.put("/v1/devices", map[string]string{"pushToken": "driver-device-1", "platform": "android"}, driver)
	status(t, res, 404)
	eq(t, res.errCode(), "not_registered")

	status(t, ta.post("/v1/admin/auth/otp", map[string]string{"phone": "9000000099"}, ""), 204)
	admin := ta.post("/v1/admin/auth/verify", map[string]string{"phone": "9000000099", "code": demoOTP}, "").str("token")
	status(t, ta.put("/v1/devices", map[string]string{"pushToken": "admin-device-1", "platform": "android"}, admin), 204)
	eq(t, ta.strings("select owner_type || ':' || owner_id from devices"), []string{"admin:9000000099"})

	// Taking the phone out of ADMIN_PHONES ends the session.
	ta.app.Config.AdminPhones = []string{}
	status(t, ta.get("/v1/admin/pending", admin), 403)
}
