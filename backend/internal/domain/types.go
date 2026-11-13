// Package domain holds the rules that need no database or clock of their
// own: dates and legs, trips and fares, the ride plan, the R2 ride
// lifecycle and polylines.
package domain

import (
	"fmt"
	"math"
	"slices"
)

var RideTypes = []string{"bike", "auto", "cab_hatchback", "cab_sedan", "cab_suv"}

var BookingFlows = []string{"one_time", "recurring_weekday", "recurring_weekly"}

var Preferences = []string{"ac_on", "quiet", "woman_driver"}

var PaymentMethods = []string{"upi", "cash"}

// Place is a pickup or drop-off point.
type Place struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Detail string  `json:"detail"`
	Lat    float64 `json:"lat"`
	Lng    float64 `json:"lng"`
}

// LatLng is a point on the map.
type LatLng struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

func (p Place) Point() LatLng { return LatLng{p.Lat, p.Lng} }

// CommuteRequest is what the rider app sends after R1.2 and R1.3.
type CommuteRequest struct {
	Pickup      Place    `json:"pickup"`
	Drop        Place    `json:"drop"`
	Repeat      string   `json:"repeat"`
	Weekdays    []int    `json:"weekdays"`
	Timing      string   `json:"timing"`
	Time        int      `json:"time"`
	ReturnTrip  bool     `json:"returnTrip"`
	ReturnTime  int      `json:"returnTime"`
	RideType    string   `json:"rideType"`
	Preferences []string `json:"preferences"`
}

// FlowFor is the booking flow a commute's repeat setting books under.
func FlowFor(repeat string) string {
	if repeat == "every_weekday" {
		return "recurring_weekday"
	}
	return "recurring_weekly"
}

// ApiError is an error the API sends as {error: Code, message: Message}.
type ApiError struct {
	Status  int
	Code    string
	Message string
}

func (e *ApiError) Error() string { return e.Message }

func NewApiError(status int, code, message string) *ApiError {
	return &ApiError{Status: status, Code: code, Message: message}
}

// Issue is one problem with a request body, as {path, message}.
type Issue struct {
	Path    []any  `json:"path"`
	Message string `json:"message"`
}

// ValidationError is a request that doesn't match its schema: a 400
// invalid_request whose message is the first issue's.
type ValidationError struct {
	Issues []Issue
}

func (e *ValidationError) Error() string {
	if len(e.Issues) == 0 {
		return "Invalid request"
	}
	return e.Issues[0].Message
}

// Check collects validation issues; Err returns them as one error, or nil.
type Check struct {
	issues []Issue
}

func (c *Check) Fail(message string, path ...any) {
	c.issues = append(c.issues, Issue{Path: append([]any{}, path...), Message: message})
}

// If records [message] at [path] when [bad] is true.
func (c *Check) If(bad bool, message string, path ...any) {
	if bad {
		c.Fail(message, path...)
	}
}

func (c *Check) Err() error {
	if len(c.issues) == 0 {
		return nil
	}
	return &ValidationError{Issues: c.issues}
}

func (c *Check) OneOf(v string, allowed []string, path ...any) {
	if !slices.Contains(allowed, v) {
		c.Fail(fmt.Sprintf("Expected one of %v, received '%s'", allowed, v), path...)
	}
}

func (c *Check) Range(v, min, max float64, path ...any) {
	if math.IsNaN(v) || v < min || v > max {
		c.Fail(fmt.Sprintf("Must be between %v and %v", min, max), path...)
	}
}

// CheckPlace checks a place as the app sends it.
func (c *Check) CheckPlace(p *Place, path ...any) {
	if p == nil {
		c.Fail("Required", path...)
		return
	}
	c.If(p.ID == "", "Required", append(path, "id")...)
	c.If(p.Title == "", "Required", append(path, "title")...)
	c.Range(p.Lat, -90, 90, append(path, "lat")...)
	c.Range(p.Lng, -180, 180, append(path, "lng")...)
}

// ValidateCommuteRequest applies the rules the app's form already follows,
// so a bad request gets a readable message.
func ValidateCommuteRequest(r *CommuteRequest, path ...any) error {
	var c Check
	if r == nil {
		c.Fail("Required", path...)
		return c.Err()
	}
	at := func(k string) []any { return append(append([]any{}, path...), k) }
	c.CheckPlace(&r.Pickup, at("pickup")...)
	c.CheckPlace(&r.Drop, at("drop")...)
	c.OneOf(r.Repeat, []string{"every_weekday", "chosen_days"}, at("repeat")...)
	c.If(len(r.Weekdays) < 1 || len(r.Weekdays) > 7, "Choose between 1 and 7 days", at("weekdays")...)
	for i, d := range r.Weekdays {
		c.If(d < 1 || d > 7, "Weekdays run from 1 (Monday) to 7 (Sunday)", append(at("weekdays"), i)...)
	}
	c.OneOf(r.Timing, []string{"pickup_at", "arrive_by"}, at("timing")...)
	c.Range(float64(r.Time), 0, 1439, at("time")...)
	c.Range(float64(r.ReturnTime), 0, 1439, at("returnTime")...)
	c.OneOf(r.RideType, RideTypes, at("rideType")...)
	for i, p := range r.Preferences {
		c.OneOf(p, Preferences, append(at("preferences"), i)...)
	}
	if err := c.Err(); err != nil {
		return err
	}
	// The cross-field rules, checked once the fields themselves are valid.
	c.If(r.Pickup.ID == r.Drop.ID, "Pickup and drop-off can't be the same place", at("drop")...)
	seen := map[int]bool{}
	for _, d := range r.Weekdays {
		seen[d] = true
	}
	c.If(len(seen) != len(r.Weekdays), "Weekdays must be unique", at("weekdays")...)
	c.If(r.Repeat == "every_weekday" && !sameSet(r.Weekdays, []int{1, 2, 3, 4, 5}),
		"Every-weekday commutes run Monday to Friday", at("weekdays")...)
	if r.Preferences == nil {
		r.Preferences = []string{}
	}
	return c.Err()
}

func sameSet(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range b {
		if !slices.Contains(a, x) {
			return false
		}
	}
	return true
}

// JSRound rounds half up, as JavaScript's Math.round does.
func JSRound(x float64) float64 { return math.Floor(x + 0.5) }
