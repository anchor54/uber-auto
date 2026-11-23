package geo

import (
	"math"
	"testing"
)

func TestDecodeGooglesExample(t *testing.T) {
	pts, err := DecodePolyline("_p~iF~ps|U_ulLnnqC_mqNvxq`@")
	if err != nil {
		t.Fatal(err)
	}
	want := []LatLng{{38.5, -120.2}, {40.7, -120.95}, {43.252, -126.453}}
	if len(pts) != len(want) {
		t.Fatalf("got %v", pts)
	}
	for i := range want {
		if math.Abs(pts[i].Lat-want[i].Lat) > 1e-9 || math.Abs(pts[i].Lng-want[i].Lng) > 1e-9 {
			t.Errorf("point %d = %v, want %v", i, pts[i], want[i])
		}
	}
	if got := EncodePolyline(want); got != "_p~iF~ps|U_ulLnnqC_mqNvxq`@" {
		t.Errorf("encode = %q", got)
	}
}
