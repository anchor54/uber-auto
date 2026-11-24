package httpapi

// The daily ride (R2 rider, D3 driver).

import (
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"commute-backend/internal/offers"
	"commute-backend/internal/scheduling"
)

func queryDate(r *http.Request, key string) (*time.Time, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return nil, nil
	}
	d, err := scheduling.ParseDate(v)
	if err != nil {
		return nil, apiErr(400, "invalid_request", key+" must be YYYY-MM-DD")
	}
	return &d, nil
}

func queryInt(r *http.Request, key string) (int, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, apiErr(400, "invalid_request", key+" must be a number")
	}
	return n, nil
}

func (s *Server) rideRoutes(handle func(string, handler)) {
	// ---- rider (R2)
	handle("GET /v1/rides/next", func(w http.ResponseWriter, r *http.Request) error {
		rider, err := s.requireRider(r)
		if err != nil {
			return err
		}
		list, err := s.Offers.NextRides(r.Context(), rider)
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"rides": list})
		return nil
	})
	handle("GET /v1/rides", func(w http.ResponseWriter, r *http.Request) error {
		rider, err := s.requireRider(r)
		if err != nil {
			return err
		}
		from, err := queryDate(r, "from")
		if err != nil {
			return err
		}
		days, err := queryInt(r, "days")
		if err != nil {
			return err
		}
		list, err := s.Offers.RiderRides(r.Context(), rider, from, days)
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"rides": list})
		return nil
	})
	handle("GET /v1/rides/{id}", func(w http.ResponseWriter, r *http.Request) error {
		user, role, err := s.requireUser(r)
		if err != nil {
			return err
		}
		id, err := pathID(r)
		if err != nil {
			return err
		}
		v, err := s.Offers.Ride(r.Context(), user, id, role)
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
		return nil
	})
	handle("GET /v1/rides/{id}/live", func(w http.ResponseWriter, r *http.Request) error {
		rider, err := s.requireRider(r)
		if err != nil {
			return err
		}
		id, err := pathID(r)
		if err != nil {
			return err
		}
		v, err := s.Offers.Live(r.Context(), rider, id)
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
		return nil
	})
	handle("POST /v1/rides/{id}/running-late", func(w http.ResponseWriter, r *http.Request) error {
		rider, err := s.requireRider(r)
		if err != nil {
			return err
		}
		id, err := pathID(r)
		if err != nil {
			return err
		}
		if err := s.Offers.RunningLate(r.Context(), rider, id); err != nil {
			return err
		}
		w.WriteHeader(204)
		return nil
	})
	handle("POST /v1/rides/{id}/paid", func(w http.ResponseWriter, r *http.Request) error {
		rider, err := s.requireRider(r)
		if err != nil {
			return err
		}
		id, err := pathID(r)
		if err != nil {
			return err
		}
		var body struct {
			Mark   string `json:"mark"`
			Method string `json:"method"`
		}
		if err := decode(r, &body); err != nil {
			return err
		}
		if body.Mark == "" {
			body.Mark = body.Method
		}
		v, err := s.Offers.RiderPaid(r.Context(), rider, id, body.Mark)
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
		return nil
	})
	handle("POST /v1/rides/{id}/rate", func(w http.ResponseWriter, r *http.Request) error {
		rider, err := s.requireRider(r)
		if err != nil {
			return err
		}
		id, err := pathID(r)
		if err != nil {
			return err
		}
		var body struct {
			Stars int      `json:"stars"`
			Tags  []string `json:"tags"`
			Note  string   `json:"note"`
		}
		if err := decode(r, &body); err != nil {
			return err
		}
		v, err := s.Offers.Rate(r.Context(), rider, id, body.Stars, body.Tags, body.Note)
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
		return nil
	})

	// ---- driver (D3)
	handle("GET /v1/driver/run", func(w http.ResponseWriter, r *http.Request) error {
		driver, err := s.requireDriver(r)
		if err != nil {
			return err
		}
		from, err := queryDate(r, "from")
		if err != nil {
			return err
		}
		days, err := queryInt(r, "days")
		if err != nil {
			return err
		}
		if days > scheduling.HorizonDays {
			return apiErr(400, "invalid_request", "days is at most 14")
		}
		run, err := s.Offers.DriverRun(r.Context(), driver, from, days)
		if err != nil {
			return err
		}
		writeJSON(w, 200, run)
		return nil
	})
	type move func(s *offers.Service, r *http.Request, driver, id uuid.UUID) (offers.RideView, error)
	driverAction := func(f move) handler {
		return func(w http.ResponseWriter, r *http.Request) error {
			driver, err := s.requireDriver(r)
			if err != nil {
				return err
			}
			id, err := pathID(r)
			if err != nil {
				return err
			}
			v, err := f(s.Offers, r, driver, id)
			if err != nil {
				return err
			}
			writeJSON(w, 200, v)
			return nil
		}
	}
	handle("POST /v1/driver/rides/{id}/on-my-way", driverAction(func(o *offers.Service, r *http.Request, d, id uuid.UUID) (offers.RideView, error) {
		return o.OnMyWay(r.Context(), d, id)
	}))
	handle("POST /v1/driver/rides/{id}/arrived", driverAction(func(o *offers.Service, r *http.Request, d, id uuid.UUID) (offers.RideView, error) {
		return o.Arrived(r.Context(), d, id)
	}))
	handle("POST /v1/driver/rides/{id}/start", driverAction(func(o *offers.Service, r *http.Request, d, id uuid.UUID) (offers.RideView, error) {
		return o.Start(r.Context(), d, id)
	}))
	handle("POST /v1/driver/rides/{id}/end", driverAction(func(o *offers.Service, r *http.Request, d, id uuid.UUID) (offers.RideView, error) {
		return o.Complete(r.Context(), d, id)
	}))
	handle("POST /v1/driver/rides/{id}/no-show", driverAction(func(o *offers.Service, r *http.Request, d, id uuid.UUID) (offers.RideView, error) {
		return o.NoShow(r.Context(), d, id)
	}))
	handle("POST /v1/driver/rides/{id}/payment", driverAction(func(o *offers.Service, r *http.Request, d, id uuid.UUID) (offers.RideView, error) {
		var body struct {
			Mark   string `json:"mark"`
			Method string `json:"method"`
		}
		if err := decode(r, &body); err != nil {
			return offers.RideView{}, err
		}
		if body.Mark == "" {
			body.Mark = body.Method
		}
		return o.DriverPaid(r.Context(), d, id, body.Mark)
	}))
	handle("POST /v1/driver/rides/{id}/flag", driverAction(func(o *offers.Service, r *http.Request, d, id uuid.UUID) (offers.RideView, error) {
		var body struct {
			Tags []string `json:"tags"`
		}
		if err := decode(r, &body); err != nil {
			return offers.RideView{}, err
		}
		return o.FlagRider(r.Context(), d, id, body.Tags)
	}))
	handle("POST /v1/driver/rides/{id}/cant-make-it", driverAction(func(o *offers.Service, r *http.Request, d, id uuid.UUID) (offers.RideView, error) {
		var body struct {
			Reason string `json:"reason"`
		}
		if err := decode(r, &body); err != nil {
			return offers.RideView{}, err
		}
		return o.GiveUp(r.Context(), d, id, body.Reason)
	}))
	handle("POST /v1/driver/rides/{id}/location", func(w http.ResponseWriter, r *http.Request) error {
		driver, err := s.requireDriver(r)
		if err != nil {
			return err
		}
		id, err := pathID(r)
		if err != nil {
			return err
		}
		var body struct {
			Lat       float64  `json:"lat"`
			Lng       float64  `json:"lng"`
			Accuracy  *float64 `json:"accuracy"`
			AccuracyM *float64 `json:"accuracy_m"`
		}
		if err := decode(r, &body); err != nil {
			return err
		}
		if body.Accuracy == nil {
			body.Accuracy = body.AccuracyM
		}
		if err := s.Offers.Location(r.Context(), driver, id, body.Lat, body.Lng, body.Accuracy); err != nil {
			return err
		}
		w.WriteHeader(204)
		return nil
	})
}
