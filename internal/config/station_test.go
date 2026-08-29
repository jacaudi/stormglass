package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadStation_AllUnset(t *testing.T) {
	clearStationEnv(t)

	cfg, err := LoadStation(time.UTC)
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

	cfg, err := LoadStation(time.UTC)
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

	cfg, err := LoadStation(time.UTC)
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
			_, err := LoadStation(time.UTC)
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

	_, err := LoadStation(time.UTC)
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

	cfg, err := LoadStation(time.UTC)
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
		wantFlag bool
		wantErr  bool
	}{
		{name: "unset", wantFlag: false},
		// An operator at a genuinely UTC station made a choice, and must not
		// be warned. Location cannot express this: time.LoadLocation("UTC")
		// returns the time.UTC pointer itself, so the flag is the only way to
		// tell "chose UTC" from "did not choose".
		{name: "explicit_utc", value: "UTC", wantFlag: true},
		{name: "valid_zone", value: "America/Denver", wantFlag: true},
		// Malformed stays fatal, and the flag stays false: it is set only in
		// the success branch.
		{name: "malformed", value: "Not/AZone", wantFlag: false, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Every test in this file starts here, so a stray STATION_* in the
			// developer's shell cannot make LoadStation error and fail a
			// wantErr:false row. See the helper's own comment.
			clearStationEnv(t)

			t.Setenv("STATION_TIMEZONE", tc.value)

			cfg, err := LoadStation(time.UTC)
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
// inherits one from the developer's shell or from CI. t.Setenv restores on
// cleanup.
//
// TZ is in the list because LoadStation reads it: with TZ=America/Denver
// exported and a time.UTC fixture, TestLoadStation_TimezoneConfigured's
// "unset" AND "malformed" subtests both fail, the latter because
// STATION_TIMEZONE did not parse while a non-empty TZ still sets the flag.
//
// Setting TZ via t.Setenv also pins this package's time.Local to UTC for the
// life of the test binary (time.Local resolves lazily, once). That is benign
// here because nothing in package config reads time.Local -- the local zone is
// a LoadStation parameter -- but it is stated rather than left to be
// discovered.
func clearStationEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"STATION_NAME", "STATION_LATITUDE", "STATION_LONGITUDE",
		"STATION_ELEVATION", "STATION_TIMEZONE", "RADAR_SITE", "TZ",
	} {
		t.Setenv(k, "")
	}
}

// TestLoadStation_TimezoneResolution is the whole timezone contract: the
// station's zone, the TimezoneConfigured flag, and the notices, across every
// combination that behaves differently.
//
// Every row asserts notice CONTENT, not merely the count. A defect that emits
// two advisories, or the deprecation text where the advisory belongs, must not
// pass. "is deprecated" and "resolved to UTC" are the discriminators.
//
// local is a parameter, so each row passes an explicit fixture and nothing
// depends on the machine's or the CI runner's zone.
func TestLoadStation_TimezoneResolution(t *testing.T) {
	denver, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatalf("LoadLocation(America/Denver): %v", err)
	}

	tests := []struct {
		name      string
		local     *time.Location
		tz        string
		stationTZ string
		wantZone  string // Location.String()
		wantFlag  bool
		wantErr   bool
		// wantNotices is ordered: entry i lists substrings that notice i must
		// contain. len(wantNotices) is also the exact expected notice count.
		wantNotices [][]string
		// wantAbsent lists substrings that must appear in NO notice.
		wantAbsent []string
	}{
		{
			// r2 regression guard: applying local unconditionally rendered the
			// almanac in the host zone while the #165 warning said UTC.
			name: "both_unset_ignores_local", local: denver,
			wantZone: "UTC", wantFlag: false,
		},
		{
			name: "tz_only_supplies_the_zone", local: denver, tz: "America/Denver",
			wantZone: "America/Denver", wantFlag: true,
		},
		{
			name: "tz_unresolvable_advises_and_degrades_to_utc", local: time.UTC, tz: "Nonsense/Zone",
			wantZone: "UTC", wantFlag: true,
			wantNotices: [][]string{{"resolved to UTC"}},
			wantAbsent:  []string{"Station times are unaffected", "is deprecated"},
		},
		{
			// glibc accepts this POSIX form; time.LoadLocation does not. D2:
			// non-fatal.
			name: "tz_posix_rule_form_advises", local: time.UTC, tz: "EST5EDT,M3.2.0/2,M11.1.0/2",
			wantZone: "UTC", wantFlag: true,
			wantNotices: [][]string{{"resolved to UTC"}},
		},
		{
			name: "tz_Local_advises", local: time.UTC, tz: "Local",
			wantZone: "UTC", wantFlag: true,
			wantNotices: [][]string{{"resolved to UTC"}},
		},
		{
			// An operator who deliberately chose UTC must not be advised.
			name: "tz_UTC_is_silent", local: time.UTC, tz: "UTC",
			wantZone: "UTC", wantFlag: true,
		},
		{
			// EqualFold: TZ=utc yields String()=="utc" on a case-insensitive
			// filesystem (darwin) and "UTC" on Linux.
			name: "tz_lowercase_utc_is_silent", local: time.UTC, tz: "utc",
			wantZone: "UTC", wantFlag: true,
		},
		{
			// TrimPrefix: the runtime strips a leading colon; the recorded
			// name does not carry it.
			name: "tz_colon_prefixed_utc_is_silent", local: time.UTC, tz: ":UTC",
			wantZone: "UTC", wantFlag: true,
		},
		{
			// The GMT arm is not dead code: on Linux TZ=gmt gives
			// String()=="UTC", and this arm is what suppresses it.
			name: "tz_lowercase_gmt_is_silent", local: time.UTC, tz: "gmt",
			wantZone: "UTC", wantFlag: true,
		},
		{
			name: "tz_colon_prefixed_zone_is_forwarded", local: denver, tz: ":America/Denver",
			wantZone: "America/Denver", wantFlag: true,
		},
		{
			name: "tz_absolute_path_is_forwarded", local: denver,
			tz:       "/usr/share/zoneinfo/America/Denver",
			wantZone: "America/Denver", wantFlag: true,
		},
		{
			name: "station_timezone_still_wins_and_is_deprecated", local: time.UTC,
			stationTZ: "America/Denver",
			wantZone:  "America/Denver", wantFlag: true,
			wantNotices: [][]string{{"is deprecated", "takes precedence over TZ"}},
			wantAbsent:  []string{"is also set", "resolved to UTC"},
		},
		{
			// D2: malformed STATION_TIMEZONE stays fatal, the flag stays
			// false, and the zone stays UTC -- but the deprecation notice
			// still fires, so the operator learns both in one restart.
			name: "station_timezone_malformed_is_fatal_and_still_notices", local: time.UTC,
			stationTZ: "Not/AZone",
			wantZone:  "UTC", wantFlag: false, wantErr: true,
			wantNotices: [][]string{{"is deprecated", "did not parse"}},
			// The parsed variant's clauses are all false here.
			wantAbsent: []string{"still honoured", "Set TZ to the same value", "takes precedence"},
		},
		{
			name: "both_set_and_equal_is_silent", local: denver,
			tz: "America/Denver", stationTZ: "America/Denver",
			wantZone: "America/Denver", wantFlag: true,
			wantNotices: [][]string{{"is deprecated"}},
			wantAbsent:  []string{"is also set"},
		},
		{
			// Without this suffix an operator who set TZ alongside a stale
			// STATION_TIMEZONE would not learn why their logs and their
			// dashboard disagree.
			name: "both_set_to_different_zones_carries_the_also_set_suffix", local: time.UTC,
			tz: "UTC", stationTZ: "America/Denver",
			wantZone: "America/Denver", wantFlag: true,
			wantNotices: [][]string{{"is deprecated", "is also set", "log timestamps follow TZ"}},
			wantAbsent:  []string{"resolved to UTC"},
		},
		{
			// Two notices, and the advisory MUST carry the unaffected clause:
			// station times really are unaffected here, so S3's wording would
			// be false.
			name: "unresolvable_tz_beside_a_valid_station_timezone", local: time.UTC,
			tz: "Nonsense/Zone", stationTZ: "America/Denver",
			wantZone: "America/Denver", wantFlag: true,
			wantNotices: [][]string{
				{"is deprecated"},
				{"resolved to UTC", "Station times are unaffected"},
			},
			// No "also set" suffix: it would say log timestamps follow TZ,
			// while the advisory beside it says they use UTC. TZ did not
			// resolve, so the advisory is the true one.
			wantAbsent: []string{"is also set"},
		},
		{
			// Location must never be nil, even when the caller hands us one.
			name: "nil_local_with_everything_unset_does_not_panic", local: nil,
			wantZone: "UTC", wantFlag: false,
		},
		{
			// The row that actually kills the nil-guard mutation: with TZ set,
			// a missing guard assigns nil to Location, which the
			// "Location must never be nil" assertion catches. The all-unset
			// row above never enters that path. It does NOT panic:
			// (*time.Location)(nil).String() returns "UTC" because
			// Location.get() nil-checks.
			name: "nil_local_with_tz_set_falls_back_to_utc", local: nil, tz: "America/Denver",
			wantZone: "UTC", wantFlag: true,
			wantNotices: [][]string{{"resolved to UTC"}},
			wantAbsent:  []string{"Station times are unaffected"},
		},
		{
			// The runtime strips a leading colon, so these are one zone
			// spelled two ways and the suffix is pure noise. Suppression
			// only -- the suffix would not be FALSE here, just redundant.
			name: "colon_prefixed_tz_matching_station_timezone_is_silent", local: denver,
			tz: ":America/Denver", stationTZ: "America/Denver",
			wantZone: "America/Denver", wantFlag: true,
			wantNotices: [][]string{{"is deprecated"}},
			wantAbsent:  []string{"is also set"},
		},
		{
			// Both halves bad: the malformed deprecation plus the
			// stationTZOK==false advisory, and still fatal.
			name: "both_malformed_emits_both_notices_and_is_fatal", local: time.UTC,
			tz: "Nonsense/Zone", stationTZ: "Not/AZone",
			wantZone: "UTC", wantFlag: true, wantErr: true,
			wantNotices: [][]string{
				{"is deprecated", "did not parse"},
				{"resolved to UTC", "every station-local time"},
			},
			wantAbsent: []string{"Station times are unaffected", "is also set"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearStationEnv(t)
			t.Setenv("TZ", tc.tz)
			t.Setenv("STATION_TIMEZONE", tc.stationTZ)

			cfg, err := LoadStation(tc.local)
			if (err != nil) != tc.wantErr {
				t.Fatalf("LoadStation() err = %v, wantErr = %v", err, tc.wantErr)
			}
			if cfg.Location == nil {
				t.Fatal("Location must never be nil")
			}
			if got := cfg.Location.String(); got != tc.wantZone {
				t.Errorf("Location = %q, want %q", got, tc.wantZone)
			}
			if cfg.TimezoneConfigured != tc.wantFlag {
				t.Errorf("TimezoneConfigured = %v, want %v", cfg.TimezoneConfigured, tc.wantFlag)
			}
			if len(cfg.TimezoneNotices) != len(tc.wantNotices) {
				t.Fatalf("got %d notices, want %d: %#v",
					len(cfg.TimezoneNotices), len(tc.wantNotices), cfg.TimezoneNotices)
			}
			for i, wants := range tc.wantNotices {
				for _, want := range wants {
					if !strings.Contains(cfg.TimezoneNotices[i], want) {
						t.Errorf("notice[%d] = %q, must contain %q", i, cfg.TimezoneNotices[i], want)
					}
				}
			}
			for _, absent := range tc.wantAbsent {
				for i, n := range cfg.TimezoneNotices {
					if strings.Contains(n, absent) {
						t.Errorf("notice[%d] = %q, must not contain %q", i, n, absent)
					}
				}
			}
		})
	}
}

// TestTimezoneMessages_ExactText pins the operator-facing strings verbatim.
// The resolution table asserts only short discriminating substrings, so
// without this a dropped sentence, a reworded clause, or a "--" substituted
// for the em dash ships silently. Each clause of these messages had to be
// true in every state that can reach it, which is why they are not free to
// drift -- see the design's §3.2.1 and §3.2.2.
//
// If a message legitimately changes, update this test AND re-check the
// reachable-state analysis. Do not update it to match whatever the code now
// says.
func TestTimezoneMessages_ExactText(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"deprecation_parsed", tzDeprecationMsg,
			"STATION_TIMEZONE is deprecated and will be removed in a future release. " +
				"It is still honoured and still takes precedence over TZ, so this station's " +
				"times use %q. Set TZ to the same value and remove STATION_TIMEZONE."},
		{"deprecation_malformed", tzDeprecationMalformedMsg,
			"STATION_TIMEZONE is deprecated and will be removed in a future release; set TZ " +
				"instead. The value %q did not parse, so it supplied no zone \u2014 the startup " +
				"error below names it."},
		{"deprecation_also_set", tzDeprecationAlsoSetMsg,
			" Note TZ=%q is also set; log timestamps follow TZ while station times follow " +
				"STATION_TIMEZONE."},
		{"advisory_station_unset", tzAdvisoryStationUnsetMsg,
			"TZ=%q resolved to UTC, so log timestamps and every station-local time \u2014 " +
				"sunrise/sunset, the almanac's calendar windows and its record date labels " +
				"\u2014 will use UTC. If that is not what you intended, set TZ to an IANA zone " +
				"name such as America/Denver."},
		{"advisory_station_set", tzAdvisoryStationSetMsg,
			"TZ=%q resolved to UTC, so log timestamps will use UTC. Station times are " +
				"unaffected because STATION_TIMEZONE takes precedence. If that is not what " +
				"you intended, set TZ to an IANA zone name such as America/Denver."},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("message drifted.\n got: %q\nwant: %q", tc.got, tc.want)
			}
		})
	}
}
