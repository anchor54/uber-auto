package scheduling

import (
	"time"

	"commute-backend/internal/travel"
)

// Trip is one ride a driver drives or would drive: when and where it starts
// and ends.
type Trip struct {
	Pickup, Drop time.Time
	From, To     travel.Point
	// What it is, for logs and events ("ride <id>", "slot MON outbound").
	Label string
}

// TravelFunc is the driving time in minutes between two points.
type TravelFunc func(from, to travel.Point) int

// Clash reports whether one driver can't drive both [a] and [b]: the earlier
// one's drop-off, plus the travel to the later one's pickup, plus Buffer,
// must not be after the later one's pickup.
func Clash(a, b Trip, tf TravelFunc) bool {
	if b.Pickup.Before(a.Pickup) {
		a, b = b, a
	}
	free := a.Drop.Add(time.Duration(tf(a.To, b.From))*time.Minute + Buffer)
	return free.After(b.Pickup)
}

// FirstClash finds a clash between any [candidate] trip and any [existing]
// trip, or between two candidate trips. ok is false when everything fits.
func FirstClash(existing, candidate []Trip, tf TravelFunc) (a, b Trip, ok bool) {
	for i, c := range candidate {
		// Trips more than a day apart can't clash; the check stays cheap.
		for _, e := range existing {
			if absDur(c.Pickup.Sub(e.Pickup)) > 24*time.Hour {
				continue
			}
			if Clash(c, e, tf) {
				return c, e, true
			}
		}
		for _, o := range candidate[i+1:] {
			if absDur(c.Pickup.Sub(o.Pickup)) > 24*time.Hour {
				continue
			}
			if Clash(c, o, tf) {
				return c, o, true
			}
		}
	}
	return Trip{}, Trip{}, false
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
