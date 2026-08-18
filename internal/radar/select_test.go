package radar

import "testing"

func TestNearestSite(t *testing.T) {
	// Near Oklahoma City; the nearest WSR-88D site is KTLX ("TLX"), whose
	// table entry is (35.333361, -97.277761) -- measured 25.96 km from this
	// query point.
	const lat, lon = 35.47, -97.51

	got, gotKm := NearestSite(lat, lon)
	if got != "TLX" {
		t.Errorf("NearestSite(%v, %v) code = %q, want %q", lat, lon, got, "TLX")
	}
	// A wide band on purpose. The assertion is that a real distance comes
	// back rather than a zero value or +Inf; re-deriving the haversine here
	// would only test the test.
	if gotKm < 20 || gotKm > 35 {
		t.Errorf("NearestSite(%v, %v) distanceKm = %v, want between 20 and 35", lat, lon, gotKm)
	}
}

func TestIsValidSite(t *testing.T) {
	tests := []struct {
		name string
		code string
		want bool
	}{
		{"valid site code", "TLX", true},
		{"path traversal attempt", "../etc", false},
		{"well-formed but unknown code", "ZZZ", false},
		{"wrong case", "tlx", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidSite(tt.code)
			if got != tt.want {
				t.Errorf("IsValidSite(%q) = %v, want %v", tt.code, got, tt.want)
			}
		})
	}
}
