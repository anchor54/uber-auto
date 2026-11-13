package domain

import (
	"errors"
	"math"
)

// DecodePolyline decodes an encoded polyline (Google's format, precision 5),
// as Ola Maps returns routes.
func DecodePolyline(encoded string) ([]LatLng, error) {
	const factor = 1e5
	var out []LatLng
	index, lat, lng := 0, 0, 0
	next := func() (int, error) {
		shift, result := 0, 0
		for {
			if index >= len(encoded) {
				return 0, errors.New("polyline ends mid-value")
			}
			b := int(encoded[index]) - 63
			index++
			result |= (b & 0x1f) << shift
			shift += 5
			if b < 0x20 {
				break
			}
		}
		if result&1 != 0 {
			return ^(result >> 1), nil
		}
		return result >> 1, nil
	}
	for index < len(encoded) {
		dLat, err := next()
		if err != nil {
			return nil, err
		}
		dLng, err := next()
		if err != nil {
			return nil, err
		}
		lat += dLat
		lng += dLng
		out = append(out, LatLng{float64(lat) / factor, float64(lng) / factor})
	}
	return out, nil
}

// EncodePolyline is the inverse of DecodePolyline (precision 5).
func EncodePolyline(points []LatLng) string {
	var out []byte
	one := func(v int) {
		n := v << 1
		if v < 0 {
			n = ^(v << 1)
		}
		for n >= 0x20 {
			out = append(out, byte((0x20|(n&0x1f))+63))
			n >>= 5
		}
		out = append(out, byte(n+63))
	}
	pLat, pLng := 0, 0
	for _, p := range points {
		lat := int(JSRound(p.Lat * 1e5))
		lng := int(JSRound(p.Lng * 1e5))
		one(lat - pLat)
		one(lng - pLng)
		pLat, pLng = lat, lng
	}
	return string(out)
}

// AlongPath gives the point [f] of the way along [path] (0 = start, 1 = end),
// by distance, so a simulated car moves at an even speed along the roads.
func AlongPath(path []LatLng) func(f float64) LatLng {
	if len(path) == 0 {
		panic("empty path")
	}
	cum := []float64{0}
	for i := 1; i < len(path); i++ {
		cum = append(cum, cum[i-1]+HaversineKm(path[i-1].Lat, path[i-1].Lng, path[i].Lat, path[i].Lng))
	}
	total := cum[len(cum)-1]
	return func(f float64) LatLng {
		if total == 0 || f <= 0 {
			return path[0]
		}
		if f >= 1 {
			return path[len(path)-1]
		}
		target := f * total
		i := 1
		for cum[i] < target {
			i++
		}
		seg := cum[i] - cum[i-1]
		t := 0.0
		if seg != 0 {
			t = (target - cum[i-1]) / seg
		}
		a, b := path[i-1], path[i]
		return LatLng{a.Lat + (b.Lat-a.Lat)*t, a.Lng + (b.Lng-a.Lng)*t}
	}
}

// DistanceToPathKm is the shortest distance in km from [p] to the path (its
// segments, not just its points).
func DistanceToPathKm(p LatLng, path []LatLng) float64 {
	if len(path) == 0 {
		return math.Inf(1)
	}
	if len(path) == 1 {
		return HaversineKm(p.Lat, p.Lng, path[0].Lat, path[0].Lng)
	}
	// Flat projection around p: plenty accurate over a city.
	kx := 111.32 * math.Cos(p.Lat*math.Pi/180)
	const ky = 110.57
	best := math.Inf(1)
	for i := 1; i < len(path); i++ {
		ax := (path[i-1].Lng - p.Lng) * kx
		ay := (path[i-1].Lat - p.Lat) * ky
		bx := (path[i].Lng - p.Lng) * kx
		by := (path[i].Lat - p.Lat) * ky
		dx, dy := bx-ax, by-ay
		len2 := dx*dx + dy*dy
		t := 0.0
		if len2 != 0 {
			t = math.Max(0, math.Min(1, -(ax*dx+ay*dy)/len2))
		}
		best = math.Min(best, math.Hypot(ax+t*dx, ay+t*dy))
	}
	return best
}
