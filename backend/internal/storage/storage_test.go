package storage

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"commute-backend/internal/clock"
)

var jpeg = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0x10, 'J', 'F', 'I', 'F'}

func TestSignedUploadAndRead(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 11, 21, 4, 30, 0, 0, time.UTC))
	l := &Local{Secret: "s", Clock: clk}
	srv := httptest.NewServer(l)
	defer srv.Close()
	l.BaseURL = srv.URL
	ctx := context.Background()
	path := "driver-docs/abc/licence"

	put := func(url string, body []byte) int {
		req, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	u, _ := l.UploadURL(ctx, path, "image/jpeg", 10*time.Minute)
	if c := put(u, []byte("not an image")); c != 415 {
		t.Errorf("text: %d, want 415", c)
	}
	if c := put(u, append(jpeg, make([]byte, MaxFileBytes)...)); c != 413 {
		t.Errorf("over 5 MB: %d, want 413", c)
	}
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n', 0}
	if c := put(u, png); c != 415 {
		t.Errorf("PNG on a JPEG link: %d, want 415", c)
	}
	if c := put(strings.Replace(u, "licence", "rc", 1), jpeg); c != 403 {
		t.Errorf("other path: %d, want 403", c)
	}
	if ok, _ := l.Exists(ctx, path); ok {
		t.Error("exists before a good upload")
	}
	if c := put(u, jpeg); c != 200 {
		t.Fatalf("good upload: %d", c)
	}
	if ok, _ := l.Exists(ctx, path); !ok {
		t.Error("not stored")
	}
	r, _ := l.ReadURL(ctx, path, 10*time.Minute)
	res, err := http.Get(r)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("content-type") != "image/jpeg" {
		t.Errorf("read: %d %s", res.StatusCode, res.Header.Get("content-type"))
	}
	clk.Advance(11 * time.Minute)
	if c := put(u, jpeg); c != 403 {
		t.Errorf("expired link: %d, want 403", c)
	}
}
