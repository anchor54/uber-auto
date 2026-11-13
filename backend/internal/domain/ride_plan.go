package domain

import (
	"slices"
	"time"
)

// Which ride records a commute needs over a range of dates. Pure.

const CreateDaysAhead = 14

type PlannedRide struct {
	RideDate    string // YYYY-MM-DD, local
	Direction   string // outbound | return
	Pickup      Place
	Drop        Place
	PickupAt    time.Time
	WindowStart time.Time
	WindowEnd   time.Time
	// On a holiday the ride is created already skipped.
	Skipped bool
}

type CommuteForPlan struct {
	Pickup     Place
	Drop       Place
	Weekdays   []int
	PickupTime int
	ReturnTrip bool
	ReturnTime int
	StartDate  string
}

type PlanOptions struct {
	From             string
	To               string
	Now              time.Time
	UTCOffsetMinutes int
	Holidays         map[string]bool
}

func AddDays(date string, days int) string {
	return mustDate(date).AddDate(0, 0, days).Format(dateLayout)
}

// LocalInstant is the instant of [minuteOfDay] on local [date].
func LocalInstant(date string, minuteOfDay, utcOffsetMinutes int) time.Time {
	return mustDate(date).Add(time.Duration(minuteOfDay-utcOffsetMinutes) * time.Minute)
}

// PlanRides lists rides for every chosen weekday from max(from, start date)
// to [to] inclusive: the outbound leg, and the return leg (pickup and drop
// swapped) when there is one. Legs whose pickup is already past are left out.
func PlanRides(c CommuteForPlan, o PlanOptions) []PlannedRide {
	var out []PlannedRide
	day := o.From
	if c.StartDate > day {
		day = c.StartDate
	}
	type leg struct {
		direction    string
		minute       int
		pickup, drop Place
	}
	for day <= o.To {
		if slices.Contains(c.Weekdays, IsoWeekday(mustDate(day))) {
			legs := []leg{{"outbound", c.PickupTime, c.Pickup, c.Drop}}
			if c.ReturnTrip {
				legs = append(legs, leg{"return", c.ReturnTime, c.Drop, c.Pickup})
			}
			for _, l := range legs {
				pickupAt := LocalInstant(day, l.minute, o.UTCOffsetMinutes)
				if !pickupAt.After(o.Now) {
					continue
				}
				out = append(out, PlannedRide{
					RideDate:    day,
					Direction:   l.direction,
					Pickup:      l.pickup,
					Drop:        l.drop,
					PickupAt:    pickupAt,
					WindowStart: pickupAt.Add(-WindowMinutes * time.Minute),
					WindowEnd:   pickupAt.Add(WindowMinutes * time.Minute),
					Skipped:     o.Holidays[day],
				})
			}
		}
		day = AddDays(day, 1)
	}
	return out
}
