package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadStation_AllUnset(t *testing.T) {
	clearStationEnv(t)

	cfg, err := LoadStation()
	if err != nil {
		t.Fatalf("LoadStation: %v", err)
	}
	if cfg.Name != nil || cfg.Latitude != nil || cfg.Longitude != nil ||
		cfg.Elevation != nil || cfg.RadarSite != nil {
		t.Fatalf("every field must be nil when unset, got %+v", cfg)
	}
	if cfg.Location != time.UTC {
		t.Fatalf("Location = %v, want time.UTC", cfg.Location)
	}
}

func TestLoadStation_Valid(t *testing.T) {
	clearStationEnv(t)
	t.Setenv("STATION_NAME", "Backyard")
	t.Setenv("STATION_LATITUDE", "40.1234")
	t.Setenv("STATION_LONGITUDE", "-75.9876")
	t.Setenv("STATION_ELEVATION", "118.3")
	t.Setenv("STATION_TIMEZONE", "America/New_York")
	t.Setenv("RADAR_SITE", "TLX")

	cfg, err := LoadStation()
	if err != nil {
		t.Fatalf("LoadStation: %v", err)
	}
	if cfg.Name == nil || *cfg.Name != "Backyard" {
		t.Errorf("Name = %v", cfg.Name)
	}
	if cfg.Latitude == nil || *cfg.Latitude != 40.1234 {
		t.Errorf("Latitude = %v", cfg.Latitude)
	}
	if cfg.Longitude == nil || *cfg.Longitude != -75.9876 {
		t.Errorf("Longitude = %v", cfg.Longitude)
	}
	if cfg.Elevation == nil || *cfg.Elevation != 118.3 {
		t.Errorf("Elevation = %v", cfg.Elevation)
	}
	if cfg.RadarSite == nil || *cfg.RadarSite != "TLX" {
		t.Errorf("RadarSite = %v", cfg.RadarSite)
	}
	if cfg.Location == nil || cfg.Location.String() != "America/New_York" {
		t.Errorf("Location = %v", cfg.Location)
	}
}

// TestLoadStation_ZeroCoordinatesAreRealValues guards the whole reason these
// fields are pointers: the equator, the prime meridian and sea level are
// legitimate configured values, not "unset".
func TestLoadStation_ZeroCoordinatesAreRealValues(t *testing.T) {
	clearStationEnv(t)
	t.Setenv("STATION_LATITUDE", "0")
	t.Setenv("STATION_LONGITUDE", "0")
	t.Setenv("STATION_ELEVATION", "0")

	cfg, err := LoadStation()
	if err != nil {
		t.Fatalf("LoadStation: %v", err)
	}
	if cfg.Latitude == nil || *cfg.Latitude != 0 {
		t.Errorf("Latitude = %v, want a non-nil pointer to 0", cfg.Latitude)
	}
	if cfg.Longitude == nil || *cfg.Longitude != 0 {
		t.Errorf("Longitude = %v, want a non-nil pointer to 0", cfg.Longitude)
	}
	if cfg.Elevation == nil || *cfg.Elevation != 0 {
		t.Errorf("Elevation = %v, want a non-nil pointer to 0", cfg.Elevation)
	}
}

func TestLoadStation_MalformedValues(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantsIn []string // substrings the aggregated error must contain
	}{
		{"unparseable_latitude", map[string]string{"STATION_LATITUDE": "north", "STATION_LONGITUDE": "0"},
			[]string{"STATION_LATITUDE", "north"}},
		{"latitude_out_of_range", map[string]string{"STATION_LATITUDE": "91", "STATION_LONGITUDE": "0"},
			[]string{"STATION_LATITUDE", "out of range"}},
		{"longitude_out_of_range", map[string]string{"STATION_LATITUDE": "0", "STATION_LONGITUDE": "-181"},
			[]string{"STATION_LONGITUDE", "out of range"}},
		{"non_finite_latitude", map[string]string{"STATION_LATITUDE": "NaN", "STATION_LONGITUDE": "0"},
			[]string{"STATION_LATITUDE", "finite"}},
		{"non_finite_elevation", map[string]string{"STATION_ELEVATION": "Inf"},
			[]string{"STATION_ELEVATION", "finite"}},
		{"unknown_timezone", map[string]string{"STATION_TIMEZONE": "Mars/Olympus_Mons"},
			[]string{"STATION_TIMEZONE", "Mars/Olympus_Mons"}},
		{"half_set_coordinates_latitude_only", map[string]string{"STATION_LATITUDE": "40.1"},
			[]string{"STATION_LATITUDE", "STATION_LONGITUDE", "together"}},
		{"half_set_coordinates_longitude_only", map[string]string{"STATION_LONGITUDE": "-75.9"},
			[]string{"STATION_LATITUDE", "STATION_LONGITUDE", "together"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearStationEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			_, err := LoadStation()
			if err == nil {
				t.Fatal("want a non-nil error")
			}
			for _, want := range tc.wantsIn {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q must mention %q", err, want)
				}
			}
		})
	}
}

// TestLoadStation_ReportsEveryErrorAtOnce is the go-standards §15.3
// requirement: an operator with three bad values fixes them in one deploy
// cycle, not three.
func TestLoadStation_ReportsEveryErrorAtOnce(t *testing.T) {
	clearStationEnv(t)
	t.Setenv("STATION_LATITUDE", "999")
	t.Setenv("STATION_LONGITUDE", "not-a-number")
	t.Setenv("STATION_ELEVATION", "high")
	t.Setenv("STATION_TIMEZONE", "Nowhere/Nothing")

	_, err := LoadStation()
	if err == nil {
		t.Fatal("want a non-nil error")
	}
	for _, want := range []string{"STATION_LATITUDE", "STATION_LONGITUDE", "STATION_ELEVATION", "STATION_TIMEZONE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("aggregated error %q must mention %q -- all four must be reported together", err, want)
		}
	}
}

// TestLoadStation_LoadsNonUTCZone proves time/tzdata is linked in, so a
// non-UTC STATION_TIMEZONE works in the scratch runtime image even if a
// Renovate bump of the base digest ever drops the OS tzdata package.
func TestLoadStation_LoadsNonUTCZone(t *testing.T) {
	clearStationEnv(t)
	t.Setenv("STATION_TIMEZONE", "America/Denver")

	cfg, err := LoadStation()
	if err != nil {
		t.Fatalf("LoadStation: %v", err)
	}
	if cfg.Location == nil || cfg.Location.String() != "America/Denver" {
		t.Fatalf("Location = %v, want America/Denver", cfg.Location)
	}
}

func TestLoadStation_TimezoneConfigured(t *testing.T) {
	tests := []struct {
		name     string
		value    string // "" means the variable is not set at all
		set      bool
		wantFlag bool
		wantErr  bool
	}{
		{name: "unset", set: false, wantFlag: false},
		// An operator at a genuinely UTC station made a choice, and must not
		// be warned. Location cannot express this: time.LoadLocation("UTC")
		// returns the time.UTC pointer itself, so the flag is the only way to
		// tell "chose UTC" from "did not choose".
		{name: "explicit_utc", value: "UTC", set: true, wantFlag: true},
		{name: "valid_zone", value: "America/Denver", set: true, wantFlag: true},
		// Malformed stays fatal, and the flag stays false: it is set only in
		// the success branch.
		{name: "malformed", value: "Not/AZone", set: true, wantFlag: false, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Every test in this file starts here, so a stray STATION_* in the
			// developer's shell cannot make LoadStation error and fail a
			// wantErr:false row. See the helper's own comment.
			clearStationEnv(t)

			if tc.set {
				t.Setenv("STATION_TIMEZONE", tc.value)
			} else {
				t.Setenv("STATION_TIMEZONE", "")
			}

			cfg, err := LoadStation()
			if (err != nil) != tc.wantErr {
				t.Fatalf("LoadStation() err = %v, wantErr = %v", err, tc.wantErr)
			}
			if cfg.TimezoneConfigured != tc.wantFlag {
				t.Errorf("TimezoneConfigured = %v, want %v", cfg.TimezoneConfigured, tc.wantFlag)
			}
			if tc.name == "explicit_utc" && cfg.Location != time.UTC {
				t.Errorf("Location = %v, want time.UTC", cfg.Location)
			}
		})
	}
}

// clearStationEnv unsets every variable LoadStation reads, so a test never
// inherits one from the developer's shell. t.Setenv restores on cleanup.
func clearStationEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"STATION_NAME", "STATION_LATITUDE", "STATION_LONGITUDE",
		"STATION_ELEVATION", "STATION_TIMEZONE", "RADAR_SITE",
	} {
		t.Setenv(k, "")
	}
}
