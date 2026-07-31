package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseBackfillFlagsDefaults(t *testing.T) {
	cfg, _, err := parseBackfillFlags(nil)
	if err != nil {
		t.Fatalf("parseBackfillFlags: %v", err)
	}
	if cfg.MinGap != 30*time.Minute {
		t.Errorf("MinGap = %v, want 30m", cfg.MinGap)
	}
	if cfg.DryRun {
		t.Error("DryRun should default to false")
	}
	if !cfg.From.IsZero() || !cfg.To.IsZero() {
		t.Error("From/To should default to zero (auto-detect)")
	}
}

// The Z-suffixed case alone cannot pin the ".UTC()" conversion in
// parseBackfillFlags: time.Parse(time.RFC3339, ...) already returns a
// time.UTC location for a Z suffix, so cfg.From.Location() != time.UTC can
// never fire regardless of whether the conversion is present. The
// offset-bearing case forces time.Parse to return a non-UTC location, so the
// location check only passes if the conversion actually runs — and the
// instant check confirms it converts to the correct point in time, not just
// relabels the location.
func TestParseBackfillFlagsRFC3339IsUTC(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantFrom time.Time
		wantTo   time.Time
	}{
		{
			name:     "Z suffix is already UTC",
			args:     []string{"--from", "2026-01-02T03:04:05Z", "--to", "2026-01-03T03:04:05Z"},
			wantFrom: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			wantTo:   time.Date(2026, 1, 3, 3, 4, 5, 0, time.UTC),
		},
		{
			name:     "offset is normalized to UTC",
			args:     []string{"--from", "2026-01-02T03:04:05+05:00", "--to", "2026-01-03T03:04:05+05:00"},
			wantFrom: time.Date(2026, 1, 1, 22, 4, 5, 0, time.UTC),
			wantTo:   time.Date(2026, 1, 2, 22, 4, 5, 0, time.UTC),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, _, err := parseBackfillFlags(tt.args)
			if err != nil {
				t.Fatalf("parseBackfillFlags: %v", err)
			}
			if cfg.From.Location() != time.UTC {
				t.Errorf("From location = %v, want UTC", cfg.From.Location())
			}
			if cfg.To.Location() != time.UTC {
				t.Errorf("To location = %v, want UTC", cfg.To.Location())
			}
			if !cfg.From.Equal(tt.wantFrom) {
				t.Errorf("From = %v, want %v", cfg.From, tt.wantFrom)
			}
			if !cfg.To.Equal(tt.wantTo) {
				t.Errorf("To = %v, want %v", cfg.To, tt.wantTo)
			}
		})
	}
}

// Both flags are always passed here, deliberately. The both-or-neither guard
// (TestParseBackfillFlagsRequiresBothOrNeither) fires before the RFC3339
// parse and its error also contains "from" or "to" — so a single-flag call
// would satisfy a substring-on-the-flag-name assertion without ever reaching
// the RFC3339 branch under test. Asserting on "RFC3339" instead, which only
// the parse-failure message contains, is what actually pins this branch.
func TestParseBackfillFlagsRejectsNonRFC3339(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"bad --from", []string{"--from", "2026-01-02", "--to", "2026-01-03T00:00:00Z"}},
		{"bad --to", []string{"--from", "2026-01-02T00:00:00Z", "--to", "2026-01-03"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := parseBackfillFlags(tt.args)
			if err == nil {
				t.Fatal("a non-RFC3339 date must be rejected; an ambiguous local-time parse is a quiet wrong-window bug")
			}
			if !strings.Contains(err.Error(), "RFC3339") {
				t.Errorf("error = %q, want it to report the RFC3339 requirement (the both-or-neither guard's message would also contain the flag name)", err)
			}
		})
	}
}

func TestParseBackfillFlagsRequiresBothOrNeither(t *testing.T) {
	if _, _, err := parseBackfillFlags([]string{"--from", "2026-01-02T00:00:00Z"}); err == nil {
		t.Error("--from without --to must be rejected")
	}
	if _, _, err := parseBackfillFlags([]string{"--to", "2026-01-02T00:00:00Z"}); err == nil {
		t.Error("--to without --from must be rejected")
	}
}

func TestParseBackfillFlagsRejectsInvertedRange(t *testing.T) {
	_, _, err := parseBackfillFlags([]string{"--from", "2026-01-03T00:00:00Z", "--to", "2026-01-02T00:00:00Z"})
	if err == nil {
		t.Fatal("--to before --from must be rejected")
	}
}

func TestParseBackfillFlagsDryRun(t *testing.T) {
	cfg, _, err := parseBackfillFlags([]string{"--dry-run", "--min-gap", "2h"})
	if err != nil {
		t.Fatalf("parseBackfillFlags: %v", err)
	}
	if !cfg.DryRun {
		t.Error("--dry-run not applied")
	}
	if cfg.MinGap != 2*time.Hour {
		t.Errorf("MinGap = %v, want 2h", cfg.MinGap)
	}
}

// TOKEN must be validated BEFORE any store handle is opened, so the failure
// costs no I/O and leaves nothing to close.
//
// The ordering is proven by pointing SQLITE_PATH at a path inside a writable
// temp dir and asserting the file was never created. An earlier version of
// this test used a bogus path and asserted only exit==1 — which passes
// identically whether TOKEN is checked before or after the store opens, since
// a failed open also returns 1. That test could not fail for its stated
// reason.
func TestRunBackfillWithoutTokenFailsBeforeOpeningTheStore(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "should-never-be-created.db")
	t.Setenv("TOKEN", "")
	t.Setenv("ENABLE_POSTGRES", "")
	t.Setenv("SQLITE_PATH", dbPath)

	if got := runBackfill(t.Context(), nil); got != 1 {
		t.Errorf("exit code = %d, want 1 for a missing TOKEN", got)
	}
	if _, err := os.Stat(dbPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("SQLite database at %s was created; TOKEN must be validated before the store is opened", dbPath)
	}
}

// --min-gap must be strictly positive. Zero makes every consecutive
// one-minute observation pair a "gap"; a negative value pushes detectTo
// into the future. Both are silent misconfigurations, not usable settings.
func TestParseBackfillFlagsRejectsNonPositiveMinGap(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"zero", []string{"--min-gap", "0s"}},
		{"negative", []string{"--min-gap", "-5m"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := parseBackfillFlags(tt.args)
			if err == nil {
				t.Fatalf("--min-gap %v must be rejected", tt.args)
			}
			if !strings.Contains(err.Error(), "min-gap") {
				t.Errorf("error = %q, want it to name --min-gap", err)
			}
		})
	}
}

func TestParseBackfillFlagsStoreValidation(t *testing.T) {
	for _, v := range []string{"sqlite", "postgres", ""} {
		if _, got, err := parseBackfillFlags([]string{"--store", v}); err != nil || got != v {
			t.Errorf("--store=%q: got (%q, %v), want (%q, nil)", v, got, err, v)
		}
	}
	if _, _, err := parseBackfillFlags([]string{"--store", "mysql"}); err == nil {
		t.Error("--store=mysql must be rejected")
	}
}

func TestResolveStore(t *testing.T) {
	both := storeChoice{sqlite: true, postgres: true}
	onlySQLite := storeChoice{sqlite: true}
	onlyPG := storeChoice{postgres: true}
	none := storeChoice{}

	tests := []struct {
		name    string
		choice  storeChoice
		flag    string
		want    string
		wantErr bool
	}{
		{"single store needs no flag", onlySQLite, "", "sqlite", false},
		{"single store, matching flag", onlyPG, "postgres", "postgres", false},
		{"single store, contradicting flag", onlySQLite, "postgres", "", true},
		{"both configured, flag chooses", both, "postgres", "postgres", false},
		{"both configured, flag chooses sqlite", both, "sqlite", "sqlite", false},
		// The regression: never silently pick one.
		{"both configured, no flag", both, "", "", true},
		{"nothing configured", none, "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveStore(tt.choice, tt.flag)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// The design's "Store selection" row demands exit 2 AND nothing written.
// TestResolveStore covers the pure function; this covers the command, which
// is what the row actually specifies. No Docker needed — resolveStore fires
// before any handle is opened.
func TestRunBackfillRefusesAmbiguousStoreAndWritesNothing(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "never.db")
	t.Setenv("TOKEN", "x")
	t.Setenv("ENABLE_POSTGRES", "true")
	t.Setenv("SQLITE_PATH", dbPath)

	if got := runBackfill(t.Context(), nil); got != 2 {
		t.Errorf("exit code = %d, want 2 — both stores configured with no --store must refuse", got)
	}
	if _, err := os.Stat(dbPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("SQLite database at %s was created; an ambiguous store selection must write nothing", dbPath)
	}
}

// --help is not a usage error.
func TestRunBackfillHelpExitsZero(t *testing.T) {
	t.Setenv("TOKEN", "x")
	if got := runBackfill(t.Context(), []string{"--help"}); got != 0 {
		t.Errorf("exit code = %d, want 0 — `cmd --help` is a successful invocation", got)
	}
}

func TestRunBackfillUsageErrorExitsTwo(t *testing.T) {
	t.Setenv("TOKEN", "x")
	if got := runBackfill(t.Context(), []string{"--from", "not-a-time", "--to", "also-not"}); got != 2 {
		t.Errorf("exit code = %d, want 2 for a usage error", got)
	}
}

func TestKnownSubcommands(t *testing.T) {
	for _, name := range []string{"healthcheck", "backfill"} {
		if !isKnownSubcommand(name) {
			t.Errorf("%q should be a known subcommand", name)
		}
	}
	for _, name := range []string{"backfil", "helthcheck", "migrate", ""} {
		if isKnownSubcommand(name) {
			t.Errorf("%q should NOT be a known subcommand", name)
		}
	}
}

// TestKnownSubcommands above tests a pure predicate — it would still pass if
// main() never called it. This one tests the BEHAVIOR the design mandates:
// an unknown subcommand must exit 2 and must NOT start the daemon.
//
// The regression it guards is real: before the dispatch fix, `backfil` fell
// through and silently started a UDP listener, which never exits — so a
// failure here shows up as a timeout, not just a bad exit code.
func TestUnknownSubcommandExitsTwoWithoutStartingDaemon(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "twx")
	//nolint:gosec // G204: test-only; builds this package's own binary into t.TempDir()
	build := exec.CommandContext(t.Context(), "go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	//nolint:gosec // G204: test-only; runs the binary this test just built
	cmd := exec.CommandContext(t.Context(), bin, "backfil")
	out, err := cmd.CombinedOutput()

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected a non-zero exit, got err=%v\n%s", err, out)
	}
	if code := exitErr.ExitCode(); code != 2 {
		t.Errorf("exit code = %d, want 2\n%s", code, out)
	}
	if !strings.Contains(string(out), "unknown subcommand") {
		t.Errorf("output should name the bad subcommand, got:\n%s", out)
	}
}

// `backfill` must actually REACH runBackfill through main()'s dispatch.
//
// TestRunBackfillHelpExitsZero calls runBackfill directly and so never crosses
// the dispatch — deleting `case "backfill":` from main() leaves the entire
// suite green while the binary falls through and starts the UDP daemon. That
// is the same silent-daemon failure Task 10 exists to eliminate, reachable by
// a one-line edit. Only a re-exec catches it.
func TestBackfillSubcommandReachesRunBackfill(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "twx")
	//nolint:gosec // G204: test-only; builds this package's own binary into t.TempDir()
	build := exec.CommandContext(t.Context(), "go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	// --help exits 0 and prints the backfill flag set. The daemon prints
	// nothing resembling this, so it discriminates cleanly.
	//nolint:gosec // G204: test-only; runs the binary this test just built
	cmd := exec.CommandContext(t.Context(), bin, "backfill", "--help")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("`backfill --help` should exit 0, got %v\n%s", err, out)
	}
	for _, flagName := range []string{"-dry-run", "-from", "-min-gap", "-store", "-to"} {
		if !strings.Contains(string(out), flagName) {
			t.Errorf("`backfill --help` output missing %s — dispatch may not be reaching runBackfill.\ngot:\n%s", flagName, out)
		}
	}
}
