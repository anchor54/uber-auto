package app

import (
	"context"
	"net/http"
	"slices"

	"commute-backend/internal/domain"
)

type RideTypeOption struct {
	Type           string `json:"type"`
	Fare           int    `json:"fare"`
	DriversOnRoute int    `json:"driversOnRoute"`
}

type BookingOptions struct {
	Flow        string           `json:"flow"`
	DefaultType *string          `json:"defaultType"`
	Options     []RideTypeOption `json:"options"`
}

func (a *App) fareRules(ctx context.Context) (map[string]domain.FareRule, error) {
	rows, err := a.DB.Query(ctx, "select ride_type, base, per_km, minimum from fare_rules where city = $1", a.Config.City)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rules := map[string]domain.FareRule{}
	for rows.Next() {
		var typ string
		var r domain.FareRule
		if err := rows.Scan(&typ, &r.Base, &r.PerKm, &r.Minimum); err != nil {
			return nil, err
		}
		rules[typ] = r
	}
	return rules, rows.Err()
}

// bookingOptions is the ride types the config allows for [flow] in the
// rider's city, each with its fare and the drivers who could take it.
func (a *App) bookingOptions(ctx context.Context, flow string, pickup, drop domain.Place, pickupTime int) (BookingOptions, error) {
	out := BookingOptions{Flow: flow, Options: []RideTypeOption{}}
	var allowed []string
	var defaultType *string
	err := a.DB.QueryRow(ctx, "select allowed, default_type from ride_type_config where city = $1 and flow = $2",
		a.Config.City, flow).Scan(&allowed, &defaultType)
	if noRows(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	rules, err := a.fareRules(ctx)
	if err != nil {
		return out, err
	}
	trip, err := a.maps().EstimateTrip(ctx, pickup.Point(), drop.Point())
	if err != nil {
		return out, err
	}
	rows, err := a.DB.Query(ctx, `select ride_type, base_lat, base_lng, service_radius_km, work_start, work_end
		from drivers where city = $1 and active`, a.Config.City)
	if err != nil {
		return out, err
	}
	type driver struct {
		rideType           string
		lat, lng, radiusKm float64
		workStart, workEnd int
	}
	var drivers []driver
	for rows.Next() {
		var d driver
		if err := rows.Scan(&d.rideType, &d.lat, &d.lng, &d.radiusKm, &d.workStart, &d.workEnd); err != nil {
			rows.Close()
			return out, err
		}
		drivers = append(drivers, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	for _, typ := range allowed {
		rule, ok := rules[typ]
		if !ok {
			continue // A type with no fare rule can't be offered.
		}
		n := 0
		for _, d := range drivers {
			if d.rideType == typ &&
				domain.HaversineKm(d.lat, d.lng, pickup.Lat, pickup.Lng) <= d.radiusKm &&
				pickupTime >= d.workStart && pickupTime+trip.Minutes <= d.workEnd {
				n++
			}
		}
		out.Options = append(out.Options, RideTypeOption{Type: typ, Fare: domain.SuggestedFare(rule, trip.DistanceKm), DriversOnRoute: n})
	}
	if defaultType != nil && slices.ContainsFunc(out.Options, func(o RideTypeOption) bool { return o.Type == *defaultType }) {
		out.DefaultType = defaultType
	} else if len(out.Options) > 0 {
		out.DefaultType = &out.Options[0].Type
	}
	return out, nil
}

func (a *App) bookingRoutes(handle func(string, handler)) {
	handle("GET /v1/places", func(w http.ResponseWriter, r *http.Request) error {
		if _, err := a.requireRider(r); err != nil {
			return err
		}
		q := r.URL.Query().Get("q")
		var c domain.Check
		c.If(len([]rune(q)) > 100, "Search for at most 100 characters", "q")
		if err := c.Err(); err != nil {
			return err
		}
		places, err := a.maps().SearchPlaces(r.Context(), q, a.cityArea())
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"places": orEmpty(places)})
		return nil
	})

	handle("POST /v1/trips/estimate", func(w http.ResponseWriter, r *http.Request) error {
		if _, err := a.requireRider(r); err != nil {
			return err
		}
		var body struct{ Pickup, Drop *domain.Place }
		if err := decode(r, &body); err != nil {
			return err
		}
		var c domain.Check
		c.CheckPlace(body.Pickup, "pickup")
		c.CheckPlace(body.Drop, "drop")
		if err := c.Err(); err != nil {
			return err
		}
		trip, err := a.maps().EstimateTrip(r.Context(), body.Pickup.Point(), body.Drop.Point())
		if err != nil {
			return err
		}
		writeJSON(w, 200, trip)
		return nil
	})

	handle("POST /v1/booking-options", func(w http.ResponseWriter, r *http.Request) error {
		if _, err := a.requireRider(r); err != nil {
			return err
		}
		var body struct {
			Flow         string
			Pickup, Drop *domain.Place
			PickupTime   *int
		}
		if err := decode(r, &body); err != nil {
			return err
		}
		var c domain.Check
		c.OneOf(body.Flow, domain.BookingFlows, "flow")
		c.CheckPlace(body.Pickup, "pickup")
		c.CheckPlace(body.Drop, "drop")
		c.If(body.PickupTime == nil || *body.PickupTime < 0 || *body.PickupTime > 1439, "Pickup time must be a minute of the day (0-1439)", "pickupTime")
		if err := c.Err(); err != nil {
			return err
		}
		opts, err := a.bookingOptions(r.Context(), body.Flow, *body.Pickup, *body.Drop, *body.PickupTime)
		if err != nil {
			return err
		}
		writeJSON(w, 200, opts)
		return nil
	})
}
