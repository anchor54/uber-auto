package travel

import (
	"math"
	"testing"
)

var (
	koramangala = Point{Lat: 12.9352, Lng: 77.6245}
	whitefield  = Point{Lat: 12.9698, Lng: 77.7178}
)

func TestKoramangalaToWhitefieldIs51MinutesByAuto(t *testing.T) {
	road := RoadKm(koramangala, whitefield)
	if math.Abs(road-15.145) > 0.01 {
		t.Errorf("road km %.3f, want about 15.145 (straight line × 1.4)", road)
	}
	if m := Minutes(road, 18); m != 51 {
		t.Errorf("%d min at 18 km/h, want 51 (rounded up)", m)
	}
	if RoundKm(road) != 15.1 {
		t.Errorf("RoundKm = %v", RoundKm(road))
	}
}

func TestMinutesRoundsUp(t *testing.T) {
	cases := []struct {
		km, speed float64
		want      int
	}{{18, 18, 60}, {18.01, 18, 61}, {0, 18, 0}, {3, 18, 10}, {1, 0, 0}}
	for _, c := range cases {
		if got := Minutes(c.km, c.speed); got != c.want {
			t.Errorf("Minutes(%v, %v) = %d, want %d", c.km, c.speed, got, c.want)
		}
	}
	if MinutesBetween(whitefield, whitefield, 18) != 0 {
		t.Error("same point is 0 minutes")
	}
}

func TestFare(t *testing.T) {
	auto := FareRule{Base: 30, PerKm: 11, Minimum: 40}
	// 30 + 11 × 15.145 = 196.6 → ₹197.
	if got := Fare(auto, RoadKm(koramangala, whitefield)); got != 197 {
		t.Errorf("auto fare %d, want 197", got)
	}
	if got := Fare(auto, 0.5); got != 40 {
		t.Errorf("short ride %d, want the minimum 40", got)
	}
}
