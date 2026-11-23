package domain

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
)

// Flow D1: driver registration, review and the subscription.

// Driver statuses.
const (
	DriverSigningUp     = "signing_up"
	DriverSubmitted     = "submitted"
	DriverChangesNeeded = "changes_needed"
	DriverApproved      = "approved"
	DriverLive          = "live"
	DriverPaused        = "paused"
	DriverRejected      = "rejected"
)

// Item states. "missing" is never stored: it is an item with no row.
const (
	ItemMissing  = "missing"
	ItemUploaded = "uploaded"
	ItemVerified = "verified"
	ItemRejected = "rejected"
)

// DriverItemKinds is every item a driver adds, in the order the app and the
// admin tool show them: the seven documents, then the vehicle photos.
var DriverItemKinds = []string{"profile_photo", "id_proof", "licence", "rc", "insurance", "permit", "upi_id",
	"vehicle_front", "vehicle_back", "vehicle_interior"}

// FileItemKinds is the items that are an uploaded photo (all but the UPI ID).
var FileItemKinds = []string{"profile_photo", "id_proof", "licence", "rc", "insurance", "permit",
	"vehicle_front", "vehicle_back", "vehicle_interior"}

// VehicleItem is the vehicle details, reviewed like an item.
const VehicleItem = "vehicle"

// ReviewableItems is what the admin verifies before approving: every item and the vehicle details.
var ReviewableItems = append(append([]string{}, DriverItemKinds...), VehicleItem)

var itemLabels = map[string]string{
	"profile_photo":    "Profile photo",
	"id_proof":         "ID proof",
	"licence":          "Driving licence",
	"rc":               "Vehicle RC",
	"insurance":        "Insurance",
	"permit":           "Permit",
	"upi_id":           "UPI ID",
	"vehicle_front":    "Front photo",
	"vehicle_back":     "Back photo",
	"vehicle_interior": "Interior photo",
	"vehicle":          "Vehicle details",
	"day_off":          "Day off",
}

var itemHints = map[string]string{
	"profile_photo":    "A clear selfie, no sunglasses",
	"id_proof":         "Aadhaar or passport",
	"licence":          "Front and back, valid for commercial use",
	"rc":               "Registration certificate",
	"insurance":        "Commercial policy in force",
	"permit":           "Taxi or contract-carriage permit",
	"upi_id":           "Shown to riders so they can pay you",
	"vehicle_front":    "The whole front, number plate readable",
	"vehicle_back":     "The whole back, number plate readable",
	"vehicle_interior": "The seats, from the driver's door",
}

// ItemLabel is the name the apps and the admin tool show for an item kind
// (or "vehicle", or "day_off").
func ItemLabel(kind string) string {
	if l, ok := itemLabels[kind]; ok {
		return l
	}
	return kind
}

// ItemHint is the line under an item's name in the driver app.
func ItemHint(kind string) string { return itemHints[kind] }

// IsFileItem tells whether [kind] is an uploaded photo.
func IsFileItem(kind string) bool {
	for _, k := range FileItemKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// MissingForSubmit is what still stops a driver submitting for review.
// [items] maps kind → state for the items that have a row; an item with no
// row, or one that was rejected and not yet replaced, is missing. Returns
// the missing item kinds in DriverItemKinds order, then "vehicle" and
// "day_off". Empty (not nil) when nothing is missing.
func MissingForSubmit(items map[string]string, vehicleSet, dayOffSet bool) []string {
	missing := []string{}
	for _, k := range DriverItemKinds {
		if s, ok := items[k]; !ok || s == "" || s == ItemMissing || s == ItemRejected {
			missing = append(missing, k)
		}
	}
	if !vehicleSet {
		missing = append(missing, VehicleItem)
	}
	if !dayOffSet {
		missing = append(missing, "day_off")
	}
	return missing
}

// AmountDueNow is the first payment: the monthly price for the days left in
// [today]'s month, today included, rounded up to the rupee. The period runs
// from today to the last day of the month. Dates are "YYYY-MM-DD".
func AmountDueNow(monthlyPrice int, today string) (amount int, periodStart, periodEnd string) {
	d := mustDate(today)
	last := time.Date(d.Year(), d.Month()+1, 0, 0, 0, 0, 0, time.UTC)
	daysInMonth := last.Day()
	daysLeft := daysInMonth - d.Day() + 1
	amount = (monthlyPrice*daysLeft + daysInMonth - 1) / daysInMonth
	return amount, d.Format(dateLayout), last.Format(dateLayout)
}

// NewReference is a payment reference: "DRV-" and 5 random digits.
func NewReference() string {
	n, err := rand.Int(rand.Reader, big.NewInt(100000))
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("DRV-%05d", n.Int64())
}

var upiRe = regexp.MustCompile(`^[A-Za-z0-9._-]{2,256}@[A-Za-z][A-Za-z0-9]{1,63}$`)

// ValidUPI tells whether [s] looks like a UPI ID (name@handle).
func ValidUPI(s string) bool { return upiRe.MatchString(s) }

// NormalizePlate is a number plate as stored and shown: upper case, single
// spaces, no space at either end ("ka 05  ab 1234" → "KA 05 AB 1234").
func NormalizePlate(s string) string {
	return strings.Join(strings.Fields(strings.ToUpper(s)), " ")
}

// PlateKey is what plates are compared by: upper case, without spaces or
// hyphens ("KA-05 AB 1234" → "KA05AB1234").
func PlateKey(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, strings.ToUpper(s))
}

var plateKeyRe = regexp.MustCompile(`^[A-Z0-9]{4,12}$`)

// ValidPlate tells whether [s] can be a number plate: 4 to 12 letters and
// digits once spaces and hyphens are taken out.
func ValidPlate(s string) bool { return plateKeyRe.MatchString(PlateKey(s)) }

// VehicleTypes and CabClasses are what a driver can register.
var (
	VehicleTypes = []string{"bike", "auto", "cab"}
	CabClasses   = []string{"hatchback", "sedan", "suv"}
)

// RideTypeFor is the ride type a vehicle serves: bike, auto or cab_<class>.
func RideTypeFor(vehicleType string, cabClass *string) string {
	if vehicleType == "cab" && cabClass != nil {
		return "cab_" + *cabClass
	}
	return vehicleType
}

// VehicleText is the one-line vehicle riders see: "{colour} {makeModel}[ · AC]".
func VehicleText(colour, makeModel string, ac bool) string {
	s := strings.TrimSpace(colour + " " + makeModel)
	if ac {
		s += " · AC"
	}
	return s
}

// Plan is the subscription for one ride type.
type Plan struct {
	RideType     string
	VehicleType  string
	CabClass     *string
	MonthlyPrice int
}

func cabClassPtr(s string) *string { return &s }

// DefaultPlans is the seeded price list (rupees a month).
var DefaultPlans = []Plan{
	{"bike", "bike", nil, 499},
	{"auto", "auto", nil, 799},
	{"cab_hatchback", "cab", cabClassPtr("hatchback"), 1499},
	{"cab_sedan", "cab", cabClassPtr("sedan"), 1499},
	{"cab_suv", "cab", cabClassPtr("suv"), 1999},
}

// PlanName is the plan's title: "Auto plan", "Sedan cab plan", ...
func PlanName(rideType string) string {
	switch rideType {
	case "bike":
		return "Bike plan"
	case "auto":
		return "Auto plan"
	case "cab_hatchback":
		return "Hatchback cab plan"
	case "cab_sedan":
		return "Sedan cab plan"
	case "cab_suv":
		return "SUV cab plan"
	}
	return "Driver plan"
}

// CityNames are the cities drivers can sign up in, by id.
var CityNames = map[string]string{"blr": "Bengaluru"}

// CityName is the city's display name (its id when unknown).
func CityName(id string) string {
	if n, ok := CityNames[id]; ok {
		return n
	}
	return id
}
