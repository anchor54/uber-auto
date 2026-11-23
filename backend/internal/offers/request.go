package offers

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"commute-backend/internal/scheduling"
	"commute-backend/internal/travel"
)

// Place is a pickup or drop-off.
type Place struct {
	Lat   float64 `json:"lat"`
	Lng   float64 `json:"lng"`
	Label string  `json:"label"`
}

func (p Place) point() travel.Point { return travel.Point{Lat: p.Lat, Lng: p.Lng} }

// SlotRequest is one weekday and direction with its time.
type SlotRequest struct {
	Weekday   int    `json:"weekday"`
	Direction string `json:"direction"`
	Timing    string `json:"timing"`
	Time      string `json:"time"`
}

// SeriesRequest is the body of series.preview and series.create (and,
// with every field optional, series.retry).
type SeriesRequest struct {
	Pickup      *Place        `json:"pickup"`
	Drop        *Place        `json:"drop"`
	VehicleType string        `json:"vehicle_type"`
	RideType    string        `json:"ride_type"` // alias of vehicle_type
	Preferences []string      `json:"preferences"`
	Slots       []SlotRequest `json:"slots"`
	// upi or cash; series.create only, default upi.
	UsualPayment string `json:"usual_payment"`
	// Optional: the earliest start date wanted (YYYY-MM-DD). Never before tomorrow.
	StartDate string `json:"start_date"`
}

func (r SeriesRequest) vehicleType() string {
	if r.VehicleType != "" {
		return r.VehicleType
	}
	return r.RideType
}

// Preferences a rider can ask for.
var allowedPreferences = []string{"ac", "quiet", "woman_driver"}

// PlannedSlot is a slot with its pickup worked out.
type PlannedSlot struct {
	Weekday   int
	Direction string
	Timing    string
	// Minutes after midnight; ArriveBy only for timing arrive_by.
	ArriveBy *int
	Pickup   int
}

// Plan is a checked request with travel, fare and start date worked out:
// what series.preview returns and series.create stores.
type Plan struct {
	Pickup, Drop  Place
	VehicleType   string
	Preferences   []string
	UsualPayment  string
	City          string
	TimeZone      string
	DistanceKm    float64
	TravelMinutes int
	SuggestedFare int
	StartDate     time.Time
	Slots         []PlannedSlot
}

type rideType struct {
	Type     string
	Label    string
	Seats    int
	SpeedKmh float64
}

func (s *Service) rideTypeFor(ctx context.Context, q querier, typ, city string) (rideType, travel.FareRule, error) {
	var rt rideType
	var rule travel.FareRule
	err := q.QueryRow(ctx, `select c.ride_type, c.label, c.seats, c.speed_kmh::float8, f.base::float8, f.per_km::float8, f.minimum::float8
		from ride_type_config c join fare_rules f on f.ride_type = c.ride_type and f.city = $2
		where c.ride_type = $1 and $3 = any (c.flows) and $2 = any (c.cities)`, typ, city, FlowRecurring).
		Scan(&rt.Type, &rt.Label, &rt.Seats, &rt.SpeedKmh, &rule.Base, &rule.PerKm, &rule.Minimum)
	if noRows(err) {
		return rt, rule, errf(422, "ride_type_not_allowed", "%q rides can't be booked as a regular ride in this city.", typ)
	}
	return rt, rule, err
}

// Plan checks a request and works out travel, fare, pickups and start date.
func (s *Service) Plan(ctx context.Context, q querier, r SeriesRequest) (Plan, error) {
	c := s.cfg()
	p := Plan{City: c.City, TimeZone: c.TimeZone, UsualPayment: "upi", Preferences: []string{}}
	bad := func(format string, args ...any) error { return errf(400, "invalid_request", format, args...) }
	if r.Pickup == nil || r.Drop == nil {
		return p, bad("pickup and drop are required")
	}
	for _, pl := range []*Place{r.Pickup, r.Drop} {
		if pl.Lat < -90 || pl.Lat > 90 || pl.Lng < -180 || pl.Lng > 180 || (pl.Lat == 0 && pl.Lng == 0) {
			return p, bad("pickup and drop need a valid lat and lng")
		}
	}
	p.Pickup, p.Drop = *r.Pickup, *r.Drop
	if travel.HaversineKm(p.Pickup.point(), p.Drop.point()) < 0.2 {
		return p, bad("Pickup and drop-off are the same place")
	}
	p.VehicleType = r.vehicleType()
	if p.VehicleType == "" {
		return p, bad("vehicle_type is required")
	}
	for _, pref := range r.Preferences {
		pref = strings.ToLower(strings.TrimSpace(pref))
		if !slices.Contains(allowedPreferences, pref) {
			return p, bad("Unknown preference %q (use %s)", pref, strings.Join(allowedPreferences, ", "))
		}
		if !slices.Contains(p.Preferences, pref) {
			p.Preferences = append(p.Preferences, pref)
		}
	}
	if r.UsualPayment != "" {
		if r.UsualPayment != "upi" && r.UsualPayment != "cash" {
			return p, bad("usual_payment must be upi or cash")
		}
		p.UsualPayment = r.UsualPayment
	}
	if len(r.Slots) == 0 {
		return p, bad("Choose at least one day")
	}
	rt, rule, err := s.rideTypeFor(ctx, q, p.VehicleType, p.City)
	if err != nil {
		return p, err
	}
	road := travel.RoadKm(p.Pickup.point(), p.Drop.point())
	p.DistanceKm = travel.RoundKm(road)
	p.TravelMinutes = travel.Minutes(road, rt.SpeedKmh)
	p.SuggestedFare = travel.Fare(rule, road)

	seen := map[string]bool{}
	var outboundDays []int
	for i, sr := range r.Slots {
		dir := sr.Direction
		if dir == "" {
			dir = "outbound"
		}
		if dir != "outbound" && dir != "return" {
			return p, bad("slots[%d].direction must be outbound or return", i)
		}
		timing := sr.Timing
		if timing == "" {
			timing = "pickup_at"
		}
		if timing != "pickup_at" && timing != "arrive_by" {
			return p, bad("slots[%d].timing must be pickup_at or arrive_by", i)
		}
		if sr.Weekday < 1 || sr.Weekday > 7 {
			return p, bad("slots[%d].weekday must be 1 (Monday) to 7 (Sunday)", i)
		}
		key := fmt.Sprint(sr.Weekday, dir)
		if seen[key] {
			return p, bad("slots[%d]: %s %s appears twice", i, scheduling.WeekdayName(sr.Weekday), dir)
		}
		seen[key] = true
		t, err := scheduling.ParseClock(sr.Time)
		if err != nil {
			return p, bad("slots[%d].time: %v", i, err)
		}
		ps := PlannedSlot{Weekday: sr.Weekday, Direction: dir, Timing: timing, Pickup: t}
		if timing == "arrive_by" {
			pickup, ok := scheduling.PickupForArrival(t, p.TravelMinutes)
			if !ok {
				return p, errf(422, "invalid_time", "Arriving by %s needs a pickup before midnight", scheduling.FormatClock(t))
			}
			ps.ArriveBy, ps.Pickup = ptr(t), pickup
		}
		p.Slots = append(p.Slots, ps)
		if dir == "outbound" {
			outboundDays = append(outboundDays, sr.Weekday)
		}
	}
	slices.SortFunc(p.Slots, func(a, b PlannedSlot) int {
		if a.Weekday != b.Weekday {
			return a.Weekday - b.Weekday
		}
		return strings.Compare(a.Direction, b.Direction) // outbound < return
	})
	var days []int
	for _, sl := range p.Slots {
		if !slices.Contains(days, sl.Weekday) {
			days = append(days, sl.Weekday)
		}
	}
	today := scheduling.DateOf(s.Clock.Now(), s.loc(p.TimeZone))
	var earliest *time.Time
	if r.StartDate != "" {
		d, err := scheduling.ParseDate(r.StartDate)
		if err != nil {
			return p, bad("start_date must be YYYY-MM-DD")
		}
		if !d.After(today) {
			return p, errf(422, "start_too_soon", "The first ride can be tomorrow at the earliest.")
		}
		earliest = &d
	}
	p.StartDate = scheduling.StartDate(today, days, earliest)
	return p, nil
}
