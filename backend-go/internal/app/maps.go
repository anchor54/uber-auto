package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"commute-backend/internal/domain"
)

// MapsProvider does place search and road trip estimates. The demo city
// (seeded places, straight line × 1.85) is the default; Ola Maps takes over
// when OLA_MAPS_API_KEY is set. Live ride ETAs (R2) stay on the
// straight-line estimate: they're asked for every 5 seconds per ride.
type MapsProvider interface {
	Name() string
	SearchPlaces(ctx context.Context, query string, city CityArea) ([]domain.Place, error)
	EstimateTrip(ctx context.Context, from, to domain.LatLng) (domain.TripEstimate, error)
}

// CityArea biases results towards a point and limits them to RadiusKm of it.
type CityArea struct {
	Code     string
	Lat, Lng float64
	RadiusKm float64
}

// CityAreas: Bengaluru is the only city until multi-city sign-up exists.
var CityAreas = map[string]CityArea{
	"blr": {Code: "blr", Lat: 12.9716, Lng: 77.5946, RadiusKm: 45},
}

func (a *App) cityArea() CityArea {
	area, ok := CityAreas[a.Config.City]
	if !ok {
		panic("no map area for city " + a.Config.City)
	}
	return area
}

// ---- the demo city ----

type demoMaps struct{ db *pgxpool.Pool }

// DemoMaps uses the seeded places and the straight-line estimate. Used in
// demo, dev and tests.
func DemoMaps(db *pgxpool.Pool) MapsProvider { return &demoMaps{db} }

func (d *demoMaps) Name() string { return "demo" }

func (d *demoMaps) SearchPlaces(ctx context.Context, query string, city CityArea) ([]domain.Place, error) {
	like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(strings.TrimSpace(query)) + "%"
	rows, err := d.db.Query(ctx, `select id, title, detail, lat, lng from places
		where city = $1 and (title ilike $2 or detail ilike $2)
		order by title limit 20`, city.Code, like)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Place, error) {
		var p domain.Place
		err := row.Scan(&p.ID, &p.Title, &p.Detail, &p.Lat, &p.Lng)
		return p, err
	})
}

func (d *demoMaps) EstimateTrip(_ context.Context, from, to domain.LatLng) (domain.TripEstimate, error) {
	return domain.EstimateTrip(from, to), nil
}

// ---- Ola Maps ----

// OlaMaps uses Ola Maps (api.olamaps.io): Places Autocomplete for search,
// Distance Matrix for road distance and drive time.
//
// Written against Ola's public API documentation; check it against a live
// key before relying on it (see the README's go-live checklist).
type OlaMaps struct {
	APIKey string
	// Client defaults to one with an 8-second timeout.
	Client *http.Client
	// Base defaults to https://api.olamaps.io.
	Base string
}

func (o *OlaMaps) Name() string { return "ola" }

func newRequestID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (o *OlaMaps) get(ctx context.Context, path string, params url.Values) (map[string]any, error) {
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	base := o.Base
	if base == "" {
		base = "https://api.olamaps.io"
	}
	params.Set("api_key", o.APIKey)
	unavailable := apiErr(502, "maps_unavailable", "Maps aren't responding. Try again in a moment.")
	req, err := http.NewRequestWithContext(ctx, "GET", base+path+"?"+params.Encode(), nil)
	if err != nil {
		return nil, unavailable
	}
	req.Header.Set("X-Request-Id", newRequestID())
	res, err := client.Do(req)
	if err != nil {
		return nil, unavailable
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return nil, unavailable
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, unavailable
	}
	return body, nil
}

// dig follows [keys] through nested JSON objects and arrays (int keys).
func dig(v any, keys ...any) any {
	for _, k := range keys {
		switch k := k.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return nil
			}
			v = m[k]
		case int:
			s, ok := v.([]any)
			if !ok || k >= len(s) {
				return nil
			}
			v = s[k]
		}
	}
	return v
}

func asString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

func (o *OlaMaps) SearchPlaces(ctx context.Context, query string, city CityArea) ([]domain.Place, error) {
	q := strings.TrimSpace(query)
	if len([]rune(q)) < 2 {
		return []domain.Place{}, nil
	}
	body, err := o.get(ctx, "/places/v1/autocomplete", url.Values{
		"input":        {q},
		"location":     {fmt.Sprintf("%v,%v", city.Lat, city.Lng)},
		"radius":       {fmt.Sprint(city.RadiusKm * 1000)},
		"strictbounds": {"true"},
	})
	if err != nil {
		return nil, err
	}
	predictions, _ := body["predictions"].([]any)
	places := []domain.Place{}
	for _, p := range predictions {
		lat, okLat := dig(p, "geometry", "location", "lat").(float64)
		lng, okLng := dig(p, "geometry", "location", "lng").(float64)
		if !okLat || !okLng {
			continue
		}
		description, _ := asString(dig(p, "description"))
		main, ok := asString(dig(p, "structured_formatting", "main_text"))
		if !ok {
			main, _, _ = strings.Cut(description, ",")
		}
		secondary, _ := asString(dig(p, "structured_formatting", "secondary_text"))
		id := fmt.Sprintf("%v,%v", lat, lng)
		if v := dig(p, "place_id"); v != nil {
			id = fmt.Sprint(v)
		} else if v := dig(p, "reference"); v != nil {
			id = fmt.Sprint(v)
		}
		title := strings.TrimSpace(main)
		if title == "" {
			title = description
		}
		// Keep the area short, like the demo places: "Koramangala", not a full address.
		parts := strings.Split(secondary, ",")
		if len(parts) > 2 {
			parts = parts[:2]
		}
		places = append(places, domain.Place{ID: id, Title: title, Detail: strings.TrimSpace(strings.Join(parts, ",")), Lat: lat, Lng: lng})
	}
	if len(places) > 20 {
		places = places[:20]
	}
	return places, nil
}

func (o *OlaMaps) EstimateTrip(ctx context.Context, from, to domain.LatLng) (domain.TripEstimate, error) {
	body, err := o.get(ctx, "/routing/v1/distanceMatrix", url.Values{
		"origins":      {fmt.Sprintf("%v,%v", from.Lat, from.Lng)},
		"destinations": {fmt.Sprintf("%v,%v", to.Lat, to.Lng)},
	})
	if err != nil {
		return domain.TripEstimate{}, err
	}
	el := dig(body, "rows", 0, "elements", 0)
	distance, okD := dig(el, "distance").(float64)
	duration, okT := dig(el, "duration").(float64)
	if !okD || !okT {
		// No route (e.g. across water): fall back to the straight-line estimate.
		return domain.EstimateTrip(from, to), nil
	}
	polyline, _ := asString(dig(el, "polyline"))
	return domain.TripEstimate{
		DistanceKm: domain.JSRound(distance/100) / 10,
		Minutes:    max(5, int(domain.JSRound(duration/60))),
		Polyline:   polyline,
	}, nil
}
