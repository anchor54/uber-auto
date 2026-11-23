// Package scheduling holds the pure rules about days and times: weekdays,
// local times, the start date, arrive-by pickups, ride dates and the clash
// check.
package scheduling

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DefaultTimeZone is every series' zone until other cities exist.
const DefaultTimeZone = "Asia/Kolkata"

// ArriveByBuffer is the slack added before an arrive-by time.
const ArriveByBuffer = 10

// Buffer is the gap a driver needs between one drop-off and the next pickup,
// on top of the travel time between them. busy_until is drop-off + Buffer.
const Buffer = 10 * time.Minute

// HorizonDays is how far ahead rides are created.
const HorizonDays = 14

var weekdayNames = []string{"", "MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"}

// WeekdayName is "MON" … "SUN" for ISO weekday 1 … 7.
func WeekdayName(w int) string {
	if w < 1 || w > 7 {
		return ""
	}
	return weekdayNames[w]
}

// ParseWeekday reads 1–7 or a day name ("MON", "monday", "Mon").
func ParseWeekday(s string) (int, error) {
	s = strings.TrimSpace(s)
	if n, err := strconv.Atoi(s); err == nil {
		if n < 1 || n > 7 {
			return 0, fmt.Errorf("weekday %d is not 1–7", n)
		}
		return n, nil
	}
	up := strings.ToUpper(s)
	if len(up) >= 3 {
		for i := 1; i <= 7; i++ {
			if strings.HasPrefix(up, weekdayNames[i]) {
				return i, nil
			}
		}
	}
	return 0, fmt.Errorf("%q is not a weekday", s)
}

// ISOWeekday is 1 (Monday) … 7 (Sunday).
func ISOWeekday(t time.Time) int {
	w := int(t.Weekday())
	if w == 0 {
		return 7
	}
	return w
}

// ParseClock reads "08:30" (or "8:30", "8:30 AM", "18:30:00") into minutes
// after midnight.
func ParseClock(s string) (int, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	pm, am := strings.HasSuffix(s, "PM"), strings.HasSuffix(s, "AM")
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(s, "AM"), "PM"))
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("%q is not a time (HH:MM)", s)
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || m < 0 || m > 59 || h < 0 || h > 23 {
		return 0, fmt.Errorf("%q is not a time (HH:MM)", s)
	}
	if am || pm {
		if h < 1 || h > 12 {
			return 0, fmt.Errorf("%q is not a time", s)
		}
		h %= 12
		if pm {
			h += 12
		}
	}
	return h*60 + m, nil
}

// FormatClock is minutes after midnight as "08:30".
func FormatClock(min int) string {
	min = ((min % 1440) + 1440) % 1440
	return fmt.Sprintf("%02d:%02d", min/60, min%60)
}

// PickupForArrival is the pickup for an arrive-by time: arrival − travel −
// 10 minutes, rounded down to 5 minutes. False when that is before midnight.
func PickupForArrival(arrival, travelMinutes int) (int, bool) {
	p := arrival - travelMinutes - ArriveByBuffer
	if p < 0 {
		return 0, false
	}
	return p - p%5, true
}

// DateOf is [t]'s calendar date in [loc], as midnight UTC.
func DateOf(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// ParseDate reads "2026-11-23" as midnight UTC.
func ParseDate(s string) (time.Time, error) { return time.Parse(time.DateOnly, s) }

// FormatDate is a date as "2026-11-23".
func FormatDate(d time.Time) string { return d.Format(time.DateOnly) }

// AddDays is [d] plus [n] days.
func AddDays(d time.Time, n int) time.Time { return d.AddDate(0, 0, n) }

// LocalInstant is [date] at [minuteOfDay] in [loc].
func LocalInstant(date time.Time, minuteOfDay int, loc *time.Location) time.Time {
	return time.Date(date.Year(), date.Month(), date.Day(), minuteOfDay/60, minuteOfDay%60, 0, 0, loc).UTC()
}

// StartDate is the first date on or after [earliest] (and never before
// tomorrow) that falls on one of [weekdays].
func StartDate(today time.Time, weekdays []int, earliest *time.Time) time.Time {
	from := AddDays(today, 1)
	if earliest != nil && earliest.After(from) {
		from = *earliest
	}
	set := map[int]bool{}
	for _, w := range weekdays {
		set[w] = true
	}
	for i := 0; i < 7; i++ {
		d := AddDays(from, i)
		if set[ISOWeekday(d)] {
			return d
		}
	}
	return from
}

// RideDates are the dates from [from] to [to], both included, that fall on [weekday].
func RideDates(weekday int, from, to time.Time) []time.Time {
	var out []time.Time
	for d := from; !d.After(to); d = AddDays(d, 1) {
		if ISOWeekday(d) == weekday {
			out = append(out, d)
		}
	}
	return out
}

// RidesPerMonth is about how many rides a weekly pattern gives in a month.
func RidesPerMonth(slots int) int {
	return int(float64(slots)*30.0/7.0 + 0.5)
}
