package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCommandsCallTheAdminAPI(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.Path+" "+strings.TrimSpace(string(body))+" "+r.Header.Get("authorization"))
		w.Header().Set("content-type", "application/json")
		switch {
		case r.URL.Path == "/v1/admin/pending":
			w.Write([]byte(`{"drivers":[{"id":"drv_1","name":"Ravi Kumar","phone":"9876500001","city":"blr","status":"submitted",
				"submittedAt":"2026-11-21T05:00:00.000Z"}],"payments":[{"id":"p-1","driverId":"drv_1","driverName":"Ravi Kumar",
				"reference":"DRV-20417","periodStart":"2026-11-21","periodEnd":"2026-11-30","amount":267,"claimedAt":"2026-11-21T06:00:00.000Z"}]}`))
		case strings.HasSuffix(r.URL.Path, "/approve"):
			w.WriteHeader(409)
			json.NewEncoder(w).Encode(map[string]any{"error": "items_not_verified", "message": "Verify every item first.",
				"items": []string{"licence", "vehicle"}})
		default:
			w.Write([]byte(`{"item":{"kind":"licence","label":"Driving licence","state":"rejected"},
				"driver":{"name":"Ravi Kumar","status":"changes_needed"}}`))
		}
	}))
	defer srv.Close()
	t.Setenv("API_URL", srv.URL)
	t.Setenv("ADMIN_TOKEN", "tok")

	var out bytes.Buffer
	if err := run([]string{"pending"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Drivers waiting for review (1)", "drv_1", "Payments claimed (1)", "₹267", "DRV-20417", "Sat 21 Nov 10:30"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("pending output lacks %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	if err := run([]string{"reject-item", "drv_1", "licence", "photo blurred"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if seen[1] != `POST /v1/admin/drivers/drv_1/items/licence/review {"decision":"rejected","reason":"photo blurred"} Bearer tok` {
		t.Fatal(seen[1])
	}
	if !strings.Contains(out.String(), "Driving licence: rejected") {
		t.Fatal(out.String())
	}

	err := run([]string{"approve", "drv_1"}, nil, &out)
	if err == nil || !strings.Contains(err.Error(), "items_not_verified") || !strings.Contains(err.Error(), "licence, vehicle") {
		t.Fatalf("got %v", err)
	}
	if err := run([]string{"verify", "drv_1"}, nil, &out); err == nil {
		t.Fatal("missing argument accepted")
	}
}
