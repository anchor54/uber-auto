// Package travel works out road distance, travel time and fares without a
// maps provider: road distance is the straight line × 1.4, driven at the
// ride type's speed.
package travel

import "math"

// RoadFactor turns a straight-line distance into a road distance.
const RoadFactor = 1.4

// Point is a place on the map.
type Point struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// HaversineKm is the straight-line distance in km.
func HaversineKm(a, b Point) float64 {
	const r = 6371
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat := rad(b.Lat - a.Lat)
	dLng := rad(b.Lng - a.Lng)
	h := math.Pow(math.Sin(dLat/2), 2) + math.Cos(rad(a.Lat))*math.Cos(rad(b.Lat))*math.Pow(math.Sin(dLng/2), 2)
	return 2 * r * math.Asin(math.Sqrt(h))
}

// RoadKm is the road distance between two points.
func RoadKm(a, b Point) float64 { return HaversineKm(a, b) * RoadFactor }

// Minutes is the time to drive [roadKm] at [speedKmh], rounded up.
func Minutes(roadKm, speedKmh float64) int {
	if roadKm <= 0 || speedKmh <= 0 {
		return 0
	}
	// Rounded to a micro-minute first, so float noise doesn't add a minute.
	m := math.Round(roadKm/speedKmh*60*1e6) / 1e6
	return int(math.Ceil(m))
}

// MinutesBetween is the travel time from [a] to [b] at [speedKmh].
func MinutesBetween(a, b Point, speedKmh float64) int { return Minutes(RoadKm(a, b), speedKmh) }

// FareRule is a ride type's fare in one city.
type FareRule struct {
	Base    float64 `json:"base"`
	PerKm   float64 `json:"per_km"`
	Minimum float64 `json:"minimum"`
}

// Fare is the suggested fare per ride in rupees: base + per-km × road km,
// at least the minimum, rounded to the nearest rupee.
func Fare(rule FareRule, roadKm float64) int {
	raw := math.Max(rule.Minimum, rule.Base+rule.PerKm*roadKm)
	return int(math.Floor(raw + 0.5))
}

// RoundKm is a distance to one decimal, for display.
func RoundKm(km float64) float64 { return math.Round(km*10) / 10 }
