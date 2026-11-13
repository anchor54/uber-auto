package domain

import (
	"slices"
	"time"
)

// Date and leg-timing rules shared by booking and matching.

const dateLayout = "2006-01-02"

// IsoWeekday is 1 = Monday … 7 = Sunday, in UTC.
func IsoWeekday(t time.Time) int {
	w := int(t.UTC().Weekday())
	if w == 0 {
		return 7
	}
	return w
}

// LocalDateString is "YYYY-MM-DD" of the calendar date in the rider's time zone.
func LocalDateString(now time.Time, utcOffsetMinutes int) string {
	return now.Add(time.Duration(utcOffsetMinutes) * time.Minute).UTC().Format(dateLayout)
}

// ParseDate reads "YYYY-MM-DD" as midnight UTC.
func ParseDate(date string) (time.Time, error) {
	return time.Parse(dateLayout, date)
}

func mustDate(date string) time.Time {
	t, err := ParseDate(date)
	if err != nil {
		panic(err)
	}
	return t
}

// FirstRideDate is the first day after [today] whose weekday is in
// [weekdays]. Rides never start on the booking day.
func FirstRideDate(today string, weekdays []int) string {
	d := mustDate(today)
	for i := 1; i <= 7; i++ {
		c := d.AddDate(0, 0, i)
		if slices.Contains(weekdays, IsoWeekday(c)) {
			return c.Format(dateLayout)
		}
	}
	panic("weekdays must not be empty")
}

// PickupForArrival is the pickup for an arrive-by commute: trip time plus a
// 10-minute buffer, rounded down to 5 minutes.
func PickupForArrival(arriveBy, tripMinutes int) int {
	raw := arriveBy - tripMinutes - 10
	return raw - (((raw % 5) + 5) % 5)
}

type Leg struct {
	Weekday int
	Start   int // minutes after midnight
	End     int
}

type WeeklyCommute struct {
	Weekdays    []int
	PickupTime  int
	ReturnTrip  bool
	ReturnTime  int
	TripMinutes int
}

// WeeklyLegs is every leg a commute occupies in a week.
func WeeklyLegs(c WeeklyCommute) []Leg {
	var legs []Leg
	for _, w := range c.Weekdays {
		legs = append(legs, Leg{w, c.PickupTime, c.PickupTime + c.TripMinutes})
		if c.ReturnTrip {
			legs = append(legs, Leg{w, c.ReturnTime, c.ReturnTime + c.TripMinutes})
		}
	}
	return legs
}

// LegGapMinutes is the minimum time between one drop and the next pickup
// for the same driver.
const LegGapMinutes = 20

func LegsClash(a, b Leg) bool {
	return a.Weekday == b.Weekday && a.Start < b.End+LegGapMinutes && b.Start < a.End+LegGapMinutes
}
