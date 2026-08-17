package main

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"

	"tempestwx-utilities/internal/config"
	"tempestwx-utilities/internal/otel"

	sdklog "go.opentelemetry.io/otel/sdk/log"
)

func TestSignalContext_RegistersInterruptAndSIGTERM(t *testing.T) {
	var gotSigs []os.Signal
	fakeNotify := func(parent context.Context, sig ...os.Signal) (context.Context, context.CancelFunc) {
		gotSigs = sig
		return context.WithCancel(parent)
	}

	_, cancel := signalContext(context.Background(), fakeNotify)
	defer cancel()

	// os.Signal(syscall.SIGTERM): slices.Contains infers E from both arguments;
	// without this explicit conversion to the slice's element type, Go's generic
	// type inference fails to unify os.Signal (interface, from gotSigs) with
	// syscall.Signal (concrete, from syscall.SIGTERM) and the call doesn't compile.
	if !slices.Contains(gotSigs, os.Interrupt) || !slices.Contains(gotSigs, os.Signal(syscall.SIGTERM)) {
		t.Fatalf("signalContext must register SIGINT+SIGTERM, got %v", gotSigs)
	}
}

func TestSelectStore(t *testing.T) {
	tests := []struct {
		name         string
		enablePG     bool
		sqlitePath   string
		wantPostgres bool
		wantSQLite   bool
		wantPath     string
	}{
		{"default sqlite", false, "", false, true, "/data/tempest.db"},
		{"postgres only", true, "", true, false, ""},
		{"both fan-out", true, "/tmp/x.db", true, true, "/tmp/x.db"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := selectStore(tc.enablePG, tc.sqlitePath)
			if c.postgres != tc.wantPostgres || c.sqlite != tc.wantSQLite {
				t.Fatalf("got %+v", c)
			}
			if tc.wantSQLite && c.sqlitePath != tc.wantPath {
				t.Fatalf("path %q want %q", c.sqlitePath, tc.wantPath)
			}
		})
	}
}

// capturingExporter is a real sdklog.Exporter (not a mock) that appends every
// exported Record to a slice, guarded by a mutex per Export's documented
// concurrency contract.
type capturingExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (e *capturingExporter) Export(_ context.Context, records []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.records = append(e.records, records...)
	return nil
}

func (e *capturingExporter) Shutdown(context.Context) error   { return nil }
func (e *capturingExporter) ForceFlush(context.Context) error { return nil }

func (e *capturingExporter) captured() []sdklog.Record {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.records
}

// TestTeeHandler_FansOutToAllHandlers asserts newTeeHandler's fix for a real
// stdlib behavior: slog.SetDefault(l) calls log.SetOutput(&handlerWriter{l.Handler(),...})
// whenever l.Handler() isn't the unexported *defaultHandler type (see
// log/slog/logger.go's SetDefault) — so wiring ENABLE_OTEL's log bridge as the
// sole slog default would silently redirect ALL of main's existing
// log.Printf/log.Fatal output away from stderr and into the OTel pipeline
// only. newTeeHandler fans every record out to both the OTel bridge and a
// plain stderr handler, so container log visibility is preserved.
func TestTeeHandler_FansOutToAllHandlers(t *testing.T) {
	exporter := &capturingExporter{}
	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewSimpleProcessor(exporter)),
	)
	t.Cleanup(func() { _ = lp.Shutdown(t.Context()) })

	var stderrBuf bytes.Buffer
	stderrHandler := slog.NewTextHandler(&stderrBuf, nil)
	otelHandler := otel.NewSlogHandler(lp)

	logger := slog.New(newTeeHandler(otelHandler, stderrHandler))
	logger.InfoContext(t.Context(), "tee test message", "key", "val")

	if !strings.Contains(stderrBuf.String(), "tee test message") {
		t.Errorf("stderr buffer = %q, want it to contain the log message (visibility must be preserved)", stderrBuf.String())
	}

	records := exporter.captured()
	if len(records) != 1 {
		t.Fatalf("got %d records captured by OTel exporter, want 1: %+v", len(records), records)
	}
	if got := records[0].Body().AsString(); got != "tee test message" {
		t.Errorf("OTel record Body() = %q, want %q", got, "tee test message")
	}
}

// TestRunHealthcheck_HealthyServer asserts runHealthcheck's success path: a
// real listening server answering 200 on /healthz yields exit code 0. Uses
// httptest.NewServer (a real net.Listener + http.Server) rather than a mock,
// per the docker HEALTHCHECK contract: the binary is exec'd as
// `tempestwx-utilities healthcheck` inside the same container as the running
// server, so it must actually dial the loopback address.
func TestRunHealthcheck_HealthyServer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse httptest URL: %v", err)
	}
	// runHealthcheck builds "http://127.0.0.1" + HTTP_ADDR + "/healthz", so
	// HTTP_ADDR must be in the ":port" shape it uses in production (srv.Addr
	// = cmp.Or(os.Getenv("HTTP_ADDR"), ":8080") in main), not "host:port".
	t.Setenv("HTTP_ADDR", ":"+u.Port())

	if got := runHealthcheck(); got != 0 {
		t.Fatalf("runHealthcheck() = %d, want 0 for a healthy /healthz", got)
	}
}

// TestRunHealthcheck_HostPortShape asserts runHealthcheck works when
// HTTP_ADDR is set in "host:port" shape (e.g. "0.0.0.0:8080" or, as here,
// "127.0.0.1:<port>"), not just the ":port" shape used elsewhere in this
// file. runHealthcheck must always probe 127.0.0.1 regardless of the host
// component in HTTP_ADDR, since the healthcheck runs inside the same
// container as the server it's probing.
func TestRunHealthcheck_HostPortShape(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse httptest URL: %v", err)
	}
	t.Setenv("HTTP_ADDR", "127.0.0.1:"+u.Port())

	if got := runHealthcheck(); got != 0 {
		t.Fatalf("runHealthcheck() = %d, want 0 for a healthy /healthz with host:port HTTP_ADDR", got)
	}
}

// TestRunHealthcheck_Unreachable asserts the failure path: nothing listening
// on the configured address yields a non-zero exit code. A listener is
// opened and immediately closed to obtain a port number that is (briefly)
// guaranteed free, rather than hardcoding a port that might be in use.
func TestRunHealthcheck_Unreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	if err := ln.Close(); err != nil {
		t.Fatalf("close reserved listener: %v", err)
	}

	t.Setenv("HTTP_ADDR", ":"+strconv.Itoa(addr.Port))

	if got := runHealthcheck(); got == 0 {
		t.Fatalf("runHealthcheck() = 0, want non-zero when nothing is listening")
	}
}

func TestRequireWriters(t *testing.T) {
	tests := []struct {
		name        string
		mode        Mode
		writerCount int
		keepFiles   bool
		wantErr     bool
	}{
		{"udp no writers", ModeUDP, 0, false, true},
		{"udp one writer", ModeUDP, 1, false, false},
		{"api no writers no files", ModeAPIExport, 0, false, true},
		{"api no writers keep files", ModeAPIExport, 0, true, false},
		{"api db writer", ModeAPIExport, 1, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := requireWriters(tc.mode, tc.writerCount, tc.keepFiles)
			if (err != nil) != tc.wantErr {
				t.Fatalf("requireWriters(%v,%d,%v) err=%v want err=%v", tc.mode, tc.writerCount, tc.keepFiles, err, tc.wantErr)
			}
		})
	}
}

// TestDecideUI covers every row of the design's degrade-loudly table. The
// load-bearing assertion in every case is the same: decideUI RETURNS a
// decision, it never exits. startAPIServer runs before the UDP listener and
// compose restarts on exit, so a fatal path for a cosmetic card flag is a
// crash loop that stops weather ingest entirely.
func TestDecideUI(t *testing.T) {
	lat, lon := 39.74, -104.98
	site := "TLX"
	located := config.StationConfig{Latitude: &lat, Longitude: &lon, RadarSite: &site, TimezoneConfigured: true}
	locatedNoTZ := config.StationConfig{Latitude: &lat, Longitude: &lon, RadarSite: &site}
	locatedNoSite := config.StationConfig{Latitude: &lat, Longitude: &lon}
	siteNoCoords := config.StationConfig{RadarSite: &site}
	badSite := "KTLX"
	locatedBadSite := config.StationConfig{Latitude: &lat, Longitude: &lon, RadarSite: &badSite}

	tests := []struct {
		name           string
		flags          uiFlags
		station        config.StationConfig
		hasStore       bool
		wantAlmanac    bool
		wantRadar      bool
		wantReasons    []string // substrings each ERROR reason must contain
		notWantReasons []string // substrings no ERROR reason may contain
		wantWarnings   []string // substrings each WARN warning must contain
	}{
		{
			name:        "everything_configured",
			flags:       uiFlags{Almanac: true, Radar: true},
			station:     located,
			hasStore:    true,
			wantAlmanac: true, wantRadar: true,
		},
		{
			name:     "nothing_enabled_is_silent",
			flags:    uiFlags{},
			station:  config.StationConfig{},
			hasStore: true,
		},
		{
			name:        "forecast_has_no_provider",
			flags:       uiFlags{Forecast: true},
			station:     located,
			hasStore:    true,
			wantReasons: []string{"ENABLE_FORECAST", "#81"},
		},
		{
			name:        "almanac_without_coordinates",
			flags:       uiFlags{Almanac: true},
			station:     config.StationConfig{},
			hasStore:    true,
			wantAlmanac: false,
			wantReasons: []string{"ENABLE_ALMANAC", "STATION_LATITUDE", "STATION_LONGITUDE"},
		},
		{
			name:        "almanac_without_a_store",
			flags:       uiFlags{Almanac: true},
			station:     located,
			hasStore:    false,
			wantAlmanac: false,
			wantReasons: []string{"ENABLE_ALMANAC", "observation store"},
		},
		{
			name:        "radar_without_a_site",
			flags:       uiFlags{Radar: true},
			station:     locatedNoSite,
			hasStore:    true,
			wantRadar:   false,
			wantReasons: []string{"ENABLE_RADAR", "RADAR_SITE"},
		},
		{
			// KTLX is the ICAO form, which is what most operators know. It is
			// not in the site table, so before this check the card mounted and
			// every tile request 400'd with no startup diagnostic at all.
			name:        "radar_with_an_unknown_site",
			flags:       uiFlags{Radar: true},
			station:     locatedBadSite,
			hasStore:    true,
			wantRadar:   false,
			wantReasons: []string{"ENABLE_RADAR", "KTLX", "FTG"},
		},
		{
			// Both radar preconditions unmet. decideUI reports EVERY unmet
			// precondition in one startup rather than selecting the first, so
			// this operator learns about the bad code AND the missing
			// coordinates without restarting. There are no coordinates from
			// which to compute a hint, so the nearest-site sentence must be
			// absent -- not merely wrong.
			name:           "radar_with_an_unknown_site_and_no_coordinates",
			flags:          uiFlags{Radar: true},
			station:        config.StationConfig{RadarSite: &badSite},
			hasStore:       true,
			wantRadar:      false,
			wantReasons:    []string{"ENABLE_RADAR", "KTLX", "STATION_LATITUDE"},
			notWantReasons: []string{"nearest site"},
		},
		{
			name:        "radar_without_coordinates",
			flags:       uiFlags{Radar: true},
			station:     siteNoCoords,
			hasStore:    true,
			wantRadar:   false,
			wantReasons: []string{"ENABLE_RADAR", "STATION_LATITUDE", "STATION_LONGITUDE"},
		},
		{
			// RADAR_SITE is decoded unconditionally by LoadStation; the flag
			// is what decides whether it reaches the wire, so a site set with
			// the flag off must be cleared -- not merely ignored -- or
			// /api/station would advertise a site for a card that is off.
			name:     "radar_site_is_cleared_when_the_flag_is_off",
			flags:    uiFlags{},
			station:  located,
			hasStore: true,
		},
		{
			// The card MOUNTS and works -- it is just on the wrong clock. So
			// this is a warning, not a reason: capabilities.almanac stays true
			// and the route stays registered.
			name:         "almanac_without_a_timezone_warns_but_mounts",
			flags:        uiFlags{Almanac: true},
			station:      locatedNoTZ,
			hasStore:     true,
			wantAlmanac:  true,
			wantWarnings: []string{"ENABLE_ALMANAC", "STATION_TIMEZONE", "UTC"},
		},
		{
			name:        "almanac_with_a_timezone_is_silent",
			flags:       uiFlags{Almanac: true},
			station:     located,
			hasStore:    true,
			wantAlmanac: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := decideUI(tc.flags, tc.station, tc.hasStore)

			if got.Almanac != tc.wantAlmanac {
				t.Errorf("Almanac = %v, want %v", got.Almanac, tc.wantAlmanac)
			}
			if got.Radar != tc.wantRadar {
				t.Errorf("Radar = %v, want %v", got.Radar, tc.wantRadar)
			}
			if !tc.wantRadar && got.RadarSite != nil {
				t.Errorf("RadarSite = %q, want nil when the radar card is not mounted", *got.RadarSite)
			}
			joined := strings.Join(got.Reasons, " | ")
			if len(tc.wantReasons) == 0 {
				if len(got.Reasons) != 0 {
					t.Errorf("Reasons = %v, want none", got.Reasons)
				}
			} else {
				for _, want := range tc.wantReasons {
					if !strings.Contains(joined, want) {
						t.Errorf("reasons %q must mention %q", joined, want)
					}
				}
			}
			for _, notWant := range tc.notWantReasons {
				if strings.Contains(joined, notWant) {
					t.Errorf("reasons %q must NOT mention %q", joined, notWant)
				}
			}

			if len(tc.wantWarnings) == 0 {
				if len(got.Warnings) != 0 {
					t.Errorf("Warnings = %v, want none", got.Warnings)
				}
			} else {
				joinedW := strings.Join(got.Warnings, " | ")
				for _, want := range tc.wantWarnings {
					if !strings.Contains(joinedW, want) {
						t.Errorf("warnings %q must mention %q", joinedW, want)
					}
				}
			}
		})
	}
}

// TestDecideUI_ReportsEveryUnmetPrecondition proves an operator with three
// broken flags learns all three from one startup, not one per restart.
func TestDecideUI_ReportsEveryUnmetPrecondition(t *testing.T) {
	got := decideUI(uiFlags{Forecast: true, Almanac: true, Radar: true}, config.StationConfig{}, false)

	if got.Almanac || got.Radar {
		t.Fatalf("nothing can be served here, got %+v", got)
	}
	joined := strings.Join(got.Reasons, " | ")
	for _, want := range []string{"ENABLE_FORECAST", "ENABLE_ALMANAC", "ENABLE_RADAR"} {
		if !strings.Contains(joined, want) {
			t.Errorf("reasons %q must mention %q -- all three must be reported in one startup", joined, want)
		}
	}
}

// TestUnknownRadarSiteReason_RendersWholeKilometres guards the "%.0f km away"
// rendering, which TestDecideUI deliberately cannot: its rows assert the site
// CODE rather than a distance, because this repo has two Denver coordinate
// pairs that render 37 and 38 km, and transcribing the wrong one produces a
// red test with no explanation. This pins the SHAPE without pinning the number.
func TestUnknownRadarSiteReason_RendersWholeKilometres(t *testing.T) {
	lat, lon := 39.74, -104.98

	got := unknownRadarSiteReason("KTLX", &lat, &lon)
	want := regexp.MustCompile(`The nearest site to your coordinates is FTG, \d+ km away\.`)
	if !want.MatchString(got) {
		t.Errorf("reason %q must match %v -- whole kilometres, no decimal point", got, want)
	}

	// With no coordinates there is nothing to compute a hint from, so the
	// sentence must be absent rather than wrong.
	if noCoords := unknownRadarSiteReason("KTLX", nil, nil); strings.Contains(noCoords, "nearest site") {
		t.Errorf("with no coordinates the reason must not mention a nearest site; got %q", noCoords)
	}
}
