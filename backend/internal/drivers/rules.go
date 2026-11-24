// Package drivers is D1: a driver signs up, adds documents, vehicle and day
// off, submits for review; an admin verifies each item and approves; the
// driver claims the first payment and the admin's confirmation makes them
// live. Offer eligibility (live and paid) is read by the offers package from
// drivers.status and drivers.subscription_until.
package drivers

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Driver statuses.
const (
	StatusSigningUp     = "signing_up"
	StatusSubmitted     = "submitted"
	StatusChangesNeeded = "changes_needed"
	StatusApproved      = "approved"
	StatusLive          = "live"
	StatusPaused        = "paused"
	StatusRejected      = "rejected"
)

// Item states. "missing" is shown for an item with no row.
const (
	StateMissing  = "missing"
	StateUploaded = "uploaded"
	StateVerified = "verified"
	StateRejected = "rejected"
)

// Document kinds: the seven documents (six files and the UPI ID), the vehicle
// details and its three photos.
const (
	KindProfilePhoto    = "profile_photo"
	KindIDProof         = "id_proof"
	KindLicence         = "licence"
	KindRC              = "rc"
	KindInsurance       = "insurance"
	KindPermit          = "permit"
	KindUPI             = "upi"
	KindVehicle         = "vehicle"
	KindVehicleFront    = "vehicle_front"
	KindVehicleBack     = "vehicle_back"
	KindVehicleInterior = "vehicle_interior"
	// Not reviewed, but needed to submit.
	KindDayOff = "day_off"
)

// DocumentKinds are the seven documents of D1.2, in screen order.
var DocumentKinds = []string{KindProfilePhoto, KindIDProof, KindLicence, KindRC, KindInsurance, KindPermit, KindUPI}

// FileKinds are the items uploaded as files.
var FileKinds = []string{KindProfilePhoto, KindIDProof, KindLicence, KindRC, KindInsurance, KindPermit,
	KindVehicleFront, KindVehicleBack, KindVehicleInterior}

// VehiclePhotoKinds are the three vehicle photos.
var VehiclePhotoKinds = []string{KindVehicleFront, KindVehicleBack, KindVehicleInterior}

// ReviewKinds are every item the admin reviews, in order.
var ReviewKinds = []string{KindProfilePhoto, KindIDProof, KindLicence, KindRC, KindInsurance, KindPermit, KindUPI,
	KindVehicle, KindVehicleFront, KindVehicleBack, KindVehicleInterior}

// RequiredKinds are what submit checks for. The day off isn't among them:
// "No fixed day off" (null) is a valid choice (orchestrator ruling).
var RequiredKinds = slices.Clone(ReviewKinds)

// IsFileKind reports whether [kind] is uploaded as a file.
func IsFileKind(kind string) bool { return slices.Contains(FileKinds, kind) }

// IsReviewKind reports whether the admin reviews [kind].
func IsReviewKind(kind string) bool { return slices.Contains(ReviewKinds, kind) }

// NormalizeKind accepts a few spellings ("driving_licence", "license",
// "vehicle-front", "upi").
func NormalizeKind(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	k = strings.ReplaceAll(k, "-", "_")
	switch k {
	case "driving_licence", "driving_license", "license", "dl":
		return KindLicence
	case "vehicle_rc", "registration":
		return KindRC
	case "upi_id", "upi_vpa":
		return KindUPI
	case "photo", "selfie":
		return KindProfilePhoto
	case "id":
		return KindIDProof
	case "front":
		return KindVehicleFront
	case "back":
		return KindVehicleBack
	case "interior":
		return KindVehicleInterior
	}
	return k
}

// Label is an item's name for people ("Driving licence").
func Label(kind string) string {
	switch kind {
	case KindProfilePhoto:
		return "Profile photo"
	case KindIDProof:
		return "ID proof"
	case KindLicence:
		return "Driving licence"
	case KindRC:
		return "Vehicle RC"
	case KindInsurance:
		return "Insurance"
	case KindPermit:
		return "Permit"
	case KindUPI:
		return "UPI ID"
	case KindVehicle:
		return "Vehicle details"
	case KindVehicleFront:
		return "Vehicle front photo"
	case KindVehicleBack:
		return "Vehicle back photo"
	case KindVehicleInterior:
		return "Vehicle interior photo"
	case KindDayOff:
		return "Day off"
	}
	return kind
}

// Missing is the completeness check: the required kinds whose state is
// missing or rejected, in RequiredKinds order. [states] maps kind → state;
// the day off counts as present when its state is anything but missing.
func Missing(states map[string]string) []string {
	out := []string{}
	for _, k := range RequiredKinds {
		switch states[k] {
		case "", StateMissing, StateRejected:
			out = append(out, k)
		}
	}
	return out
}

// AllVerified reports whether every reviewed item is verified.
func AllVerified(states map[string]string) bool {
	for _, k := range ReviewKinds {
		if states[k] != StateVerified {
			return false
		}
	}
	return true
}

// Unverified are the reviewed items not yet verified.
func Unverified(states map[string]string) []string {
	out := []string{}
	for _, k := range ReviewKinds {
		if states[k] != StateVerified {
			out = append(out, k)
		}
	}
	return out
}

// ---------------------------------------------------------------- UPI

var upiRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{1,255}@[a-zA-Z][a-zA-Z0-9]{1,63}$`)

// ValidUPI checks the name@handle pattern.
func ValidUPI(s string) bool { return upiRe.MatchString(s) }

// ---------------------------------------------------------------- plates

var spaces = regexp.MustCompile(`\s+`)

// NormalizePlate upper-cases a plate and collapses its spaces.
func NormalizePlate(p string) string {
	return spaces.ReplaceAllString(strings.ToUpper(strings.TrimSpace(p)), " ")
}

// PlateKey is what uniqueness compares: letters and digits only.
func PlateKey(p string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(p) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var plateRe = regexp.MustCompile(`^[A-Z0-9]{4,12}$`)

// ValidPlate is a plate of 4–12 letters and digits (spaces and dashes aside).
func ValidPlate(p string) bool { return plateRe.MatchString(PlateKey(p)) }

// ---------------------------------------------------------------- vehicle type

// RideType is the ride type a vehicle serves (ride_type_config and
// drivers.vehicle_type): bike, auto, cab_<class>.
func RideType(typ, cabClass string) string {
	if typ == "cab" {
		return "cab_" + cabClass
	}
	return typ
}

// TypeLabel is the vehicle type for people ("auto", "sedan").
func TypeLabel(typ, cabClass string) string {
	if typ == "cab" && cabClass != "" {
		return cabClass
	}
	return typ
}

// ---------------------------------------------------------------- day off

var dayNames = []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"}

// ParseWeekday reads 1–7 (ISO) or a day name ("WED", "wednesday").
func ParseWeekday(s string) (int, bool) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) == 1 && s[0] >= '1' && s[0] <= '7' {
		return int(s[0] - '0'), true
	}
	if len(s) >= 3 {
		for i, d := range dayNames {
			if s[:3] == d {
				return i + 1, true
			}
		}
	}
	return 0, false
}

// DayName is "MON" … "SUN" for ISO 1–7.
func DayName(w int) string {
	if w < 1 || w > 7 {
		return ""
	}
	return dayNames[w-1]
}

// ---------------------------------------------------------------- money

// Period is the first period paid for: [today] to the last day of its month.
func Period(today time.Time) (start, end time.Time) {
	start = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	end = time.Date(today.Year(), today.Month()+1, 0, 0, 0, 0, 0, time.UTC)
	return start, end
}

// DaysIn is the number of days in [t]'s month.
func DaysIn(t time.Time) int {
	return time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// AmountDue is the amount to pay now: monthly price × days left in the
// month (today included) ÷ days in the month, rounded up to the rupee.
func AmountDue(monthlyPrice int, today time.Time) int {
	dim := DaysIn(today)
	left := dim - today.Day() + 1
	return (monthlyPrice*left + dim - 1) / dim
}

// ---------------------------------------------------------------- reference

// NewReference is DRV- and 5 random digits (10000–99999). Uniqueness is the
// database's: callers retry on a clash.
func NewReference() string {
	n, err := rand.Int(rand.Reader, big.NewInt(90000))
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("DRV-%05d", 10000+n.Int64())
}

var referenceRe = regexp.MustCompile(`^DRV-\d{5}$`)

// ValidReference checks the DRV-xxxxx shape.
func ValidReference(s string) bool { return referenceRe.MatchString(s) }
