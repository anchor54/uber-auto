package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"commute-backend/internal/drivers"
)

// ---------------------------------------------------------------- driver (D1)

// dayOffBody reads {"day_off": 1..7 | "WED" | null | "none"} (also "weekday").
type dayOffBody struct {
	DayOff  json.RawMessage `json:"day_off"`
	Weekday json.RawMessage `json:"weekday"`
	None    bool            `json:"none"`
}

func (b dayOffBody) parse() (*int, error) {
	raw := b.DayOff
	if len(raw) == 0 {
		raw = b.Weekday
	}
	if b.None || len(raw) == 0 || string(raw) == "null" {
		if !b.None && len(raw) == 0 {
			return nil, apiErr(422, "bad_weekday", "Pick a day off, or no fixed day off.")
		}
		return nil, nil
	}
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		if n == 0 {
			return nil, nil
		}
		return &n, nil
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		switch strings.ToLower(strings.TrimSpace(str)) {
		case "", "none", "no", "no_fixed_day_off":
			return nil, nil
		}
		if w, ok := drivers.ParseWeekday(str); ok {
			return &w, nil
		}
	}
	return nil, apiErr(422, "bad_weekday", "Pick a day from Monday to Sunday, or no fixed day off.")
}

func (s *Server) driverRoutes(handle func(string, handler)) {
	d := s.Drivers
	handle("POST /v1/driver/signup", func(w http.ResponseWriter, r *http.Request) error {
		id, err := s.requireDriver(r)
		if err != nil {
			return err
		}
		var req drivers.RegisterRequest
		if err := decode(r, &req); err != nil {
			return err
		}
		v, err := d.Register(r.Context(), id, req)
		if err != nil {
			return err
		}
		writeJSON(w, 201, v)
		return nil
	})
	handle("GET /v1/driver", func(w http.ResponseWriter, r *http.Request) error {
		id, err := s.requireDriver(r)
		if err != nil {
			return err
		}
		v, err := d.Get(r.Context(), id)
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
		return nil
	})
	handle("POST /v1/driver/upload-url", func(w http.ResponseWriter, r *http.Request) error {
		id, err := s.requireDriver(r)
		if err != nil {
			return err
		}
		var req drivers.UploadURLRequest
		if err := decode(r, &req); err != nil {
			return err
		}
		v, err := d.UploadURL(r.Context(), id, req)
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
		return nil
	})
	handle("PUT /v1/driver/documents/{kind}", func(w http.ResponseWriter, r *http.Request) error {
		id, err := s.requireDriver(r)
		if err != nil {
			return err
		}
		var body struct {
			StoragePath string `json:"storage_path"`
			Path        string `json:"path"`
		}
		if err := decode(r, &body); err != nil {
			return err
		}
		if body.StoragePath == "" {
			body.StoragePath = body.Path
		}
		v, err := d.ItemUploaded(r.Context(), id, r.PathValue("kind"), body.StoragePath)
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
		return nil
	})
	handle("PUT /v1/driver/upi", func(w http.ResponseWriter, r *http.Request) error {
		id, err := s.requireDriver(r)
		if err != nil {
			return err
		}
		var body struct {
			UPIID string `json:"upi_id"`
			UPI   string `json:"upi"`
		}
		if err := decode(r, &body); err != nil {
			return err
		}
		if body.UPIID == "" {
			body.UPIID = body.UPI
		}
		v, err := d.SetUPI(r.Context(), id, body.UPIID)
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
		return nil
	})
	handle("PUT /v1/driver/vehicle", func(w http.ResponseWriter, r *http.Request) error {
		id, err := s.requireDriver(r)
		if err != nil {
			return err
		}
		var req drivers.VehicleRequest
		if err := decode(r, &req); err != nil {
			return err
		}
		v, err := d.SetVehicle(r.Context(), id, req)
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
		return nil
	})
	handle("PUT /v1/driver/day-off", func(w http.ResponseWriter, r *http.Request) error {
		id, err := s.requireDriver(r)
		if err != nil {
			return err
		}
		var body dayOffBody
		if err := decode(r, &body); err != nil {
			return err
		}
		day, err := body.parse()
		if err != nil {
			return err
		}
		v, err := d.SetDayOff(r.Context(), id, day)
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
		return nil
	})
	handle("POST /v1/driver/submit", func(w http.ResponseWriter, r *http.Request) error {
		id, err := s.requireDriver(r)
		if err != nil {
			return err
		}
		v, err := d.Submit(r.Context(), id)
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
		return nil
	})
	handle("GET /v1/driver/plan", func(w http.ResponseWriter, r *http.Request) error {
		id, err := s.requireDriver(r)
		if err != nil {
			return err
		}
		v, err := d.Plan(r.Context(), id)
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
		return nil
	})
	handle("POST /v1/driver/payments", func(w http.ResponseWriter, r *http.Request) error {
		id, err := s.requireDriver(r)
		if err != nil {
			return err
		}
		var body struct {
			PeriodStart string `json:"period_start"`
		}
		if err := decode(r, &body); err != nil {
			return err
		}
		v, err := d.ClaimPayment(r.Context(), id, body.PeriodStart)
		if err != nil {
			return err
		}
		writeJSON(w, 201, v)
		return nil
	})
}

// ---------------------------------------------------------------- admin (D1)

func (s *Server) adminRoutes(handle func(string, handler)) {
	d := s.Drivers
	driverID := func(r *http.Request) (uuid.UUID, uuid.UUID, error) {
		admin, err := s.requireAdmin(r)
		if err != nil {
			return admin, uuid.Nil, err
		}
		id, err := pathID(r)
		return admin, id, err
	}
	var reasonBody = func(r *http.Request) (string, error) {
		var body struct {
			Reason string `json:"reason"`
		}
		err := decode(r, &body)
		return body.Reason, err
	}
	handle("GET /v1/admin/pending", func(w http.ResponseWriter, r *http.Request) error {
		if _, err := s.requireAdmin(r); err != nil {
			return err
		}
		v, err := d.Pending(r.Context())
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
		return nil
	})
	handle("GET /v1/admin/drivers/{id}", func(w http.ResponseWriter, r *http.Request) error {
		_, id, err := driverID(r)
		if err != nil {
			return err
		}
		v, err := d.AdminShow(r.Context(), id)
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
		return nil
	})
	handle("POST /v1/admin/drivers/{id}/review-item", func(w http.ResponseWriter, r *http.Request) error {
		admin, id, err := driverID(r)
		if err != nil {
			return err
		}
		var body struct {
			Item     string `json:"item"`
			Kind     string `json:"kind"`
			State    string `json:"state"`
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
		}
		if err := decode(r, &body); err != nil {
			return err
		}
		if body.Item == "" {
			body.Item = body.Kind
		}
		if body.State == "" {
			body.State = body.Decision
		}
		v, err := d.ReviewItem(r.Context(), admin, id, body.Item, body.State, body.Reason)
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
		return nil
	})
	handle("POST /v1/admin/drivers/{id}/approve", func(w http.ResponseWriter, r *http.Request) error {
		admin, id, err := driverID(r)
		if err != nil {
			return err
		}
		v, err := d.Approve(r.Context(), admin, id)
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
		return nil
	})
	handle("POST /v1/admin/drivers/{id}/reject", func(w http.ResponseWriter, r *http.Request) error {
		admin, id, err := driverID(r)
		if err != nil {
			return err
		}
		reason, err := reasonBody(r)
		if err != nil {
			return err
		}
		v, err := d.Reject(r.Context(), admin, id, reason)
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
		return nil
	})
	handle("POST /v1/admin/payments/{id}/confirm", func(w http.ResponseWriter, r *http.Request) error {
		admin, id, err := driverID(r)
		if err != nil {
			return err
		}
		v, err := d.ConfirmPayment(r.Context(), admin, id)
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
		return nil
	})
	handle("POST /v1/admin/payments/{id}/reject", func(w http.ResponseWriter, r *http.Request) error {
		admin, id, err := driverID(r)
		if err != nil {
			return err
		}
		reason, err := reasonBody(r)
		if err != nil {
			return err
		}
		v, err := d.RejectPayment(r.Context(), admin, id, reason)
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
		return nil
	})
}
