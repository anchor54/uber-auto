package domain

import (
	"reflect"
	"testing"
)

func TestValidPlate(t *testing.T) {
	for _, ok := range []string{"KA 05 AB 1234", "ka-05-ab-1234", "DL1CAB1234"} {
		if !ValidPlate(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "  ", "AB1", "KA 05 AB 1234 5678 9", "KA#05"} {
		if ValidPlate(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestRideTypeAndVehicleText(t *testing.T) {
	sedan := "sedan"
	if got := RideTypeFor("cab", &sedan); got != "cab_sedan" {
		t.Fatal(got)
	}
	if got := RideTypeFor("auto", nil); got != "auto" {
		t.Fatal(got)
	}
	if got := VehicleText("White", "Dzire", true); got != "White Dzire · AC" {
		t.Fatal(got)
	}
	if got := VehicleText("Green", "Bajaj RE", false); got != "Green Bajaj RE" {
		t.Fatal(got)
	}
}

func TestReviewableItemsEndWithTheVehicle(t *testing.T) {
	want := append(append([]string{}, DriverItemKinds...), "vehicle")
	if !reflect.DeepEqual(ReviewableItems, want) {
		t.Fatal(ReviewableItems)
	}
	if len(FileItemKinds) != 9 || IsFileItem("upi_id") || !IsFileItem("vehicle_back") {
		t.Fatal(FileItemKinds)
	}
	for _, k := range ReviewableItems {
		if ItemLabel(k) == k {
			t.Errorf("no label for %s", k)
		}
	}
}

func TestRejectedItemsMustBeReplacedBeforeSubmitting(t *testing.T) {
	items := map[string]string{}
	for _, k := range DriverItemKinds {
		items[k] = ItemVerified
	}
	items["licence"] = ItemRejected
	if got := MissingForSubmit(items, true, true); !reflect.DeepEqual(got, []string{"licence"}) {
		t.Fatal(got)
	}
	items["licence"] = ItemUploaded
	if got := MissingForSubmit(items, true, true); len(got) != 0 || got == nil {
		t.Fatalf("%#v", got)
	}
}
