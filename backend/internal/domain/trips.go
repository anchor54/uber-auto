package domain

import "math"

type FareRule struct {
	Base    int
	PerKm   int
	Minimum int
}

type TripEstimate struct {
	DistanceKm float64 `json:"distanceKm"`
	Minutes    int     `json:"minutes"`
	// The road route as an encoded polyline (precision 5), when the maps
	// provider gives one.
	Polyline string `json:"polyline,omitempty"`
}

// HaversineKm is the straight-line distance in km.
func HaversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	const r = 6371
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat := rad(lat2 - lat1)
	dLng := rad(lng2 - lng1)
	a := math.Pow(math.Sin(dLat/2), 2) + math.Cos(rad(lat1))*math.Cos(rad(lat2))*math.Pow(math.Sin(dLng/2), 2)
	return 2 * r * math.Asin(math.Sqrt(a))
}

// EstimateTrip is the road trip estimate without a maps provider: roads run
// ~1.85x the straight line in Bengaluru at ~19 km/h average commute speed.
// Must match the rider app's demo backend (lib/core/fares.dart).
func EstimateTrip(a, b LatLng) TripEstimate {
	km := HaversineKm(a.Lat, a.Lng, b.Lat, b.Lng) * 1.85
	return TripEstimate{
		DistanceKm: JSRound(km*10) / 10,
		Minutes:    max(5, int(JSRound(km/19*60))),
	}
}

// SuggestedFare is the fare per ride in rupees, rounded to the nearest ₹5.
func SuggestedFare(rule FareRule, distanceKm float64) int {
	raw := math.Max(float64(rule.Minimum), float64(rule.Base)+float64(rule.PerKm)*distanceKm)
	return int(JSRound(raw/5)) * 5
}

var DefaultFareRules = map[string]FareRule{
	"bike":          {20, 6, 35},
	"auto":          {30, 11, 40},
	"cab_hatchback": {50, 14, 90},
	"cab_sedan":     {60, 16, 110},
	"cab_suv":       {80, 21, 150},
}
