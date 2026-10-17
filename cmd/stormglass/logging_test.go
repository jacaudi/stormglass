package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildTestBinary compiles this package into a temp dir and returns the path,
// for the tests that must observe the real process: its exit code and what it
// writes to stderr.
func buildTestBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "twx")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// Both malformed values must abort startup naming every offender, and the
// process must exit 1, in the daemon and in the backfill subcommand alike.
func TestMalformedLogConfigIsFatalAndNamesEveryOffender(t *testing.T) {
	bin := buildTestBinary(t)

	for name, args := range map[string][]string{"daemon": nil, "backfill": {"backfill"}} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), bin, args...)
			cmd.Env = append(os.Environ(), "LOG_LEVEL=loud", "LOG_FORMAT=xml")
			out, err := cmd.CombinedOutput()

			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("expected a non-zero exit, got err=%v\n%s", err, out)
			}
			if code := exitErr.ExitCode(); code != 1 {
				t.Errorf("exit code = %d, want 1\n%s", code, out)
			}
			for _, want := range []string{"LOG_LEVEL", "loud", "LOG_FORMAT", "xml"} {
				if !strings.Contains(string(out), want) {
					t.Errorf("output does not name %s:\n%s", want, out)
				}
			}
		})
	}
}

// End to end: LOG_FORMAT=json reaches the real process, and a startup failure
// (every fatal call site) is reported through the same handler as an ERROR
// record, on stderr, with exit code 1.
func TestFatalStartupErrorHonorsLogFormat(t *testing.T) {
	bin := buildTestBinary(t)

	cmd := exec.CommandContext(t.Context(), bin)
	// ENABLE_POSTGRES is parsed before any resource is opened, so this fails
	// fast with a malformed boolean and touches no database or socket.
	cmd.Env = append(os.Environ(), "LOG_FORMAT=json", "ENABLE_POSTGRES=maybe")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("want exit code 1, got err=%v\nstderr:\n%s", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty: logs belong on stderr", stdout.String())
	}

	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	var last map[string]any
	for _, line := range lines {
		last = nil
		if err := json.Unmarshal([]byte(line), &last); err != nil {
			t.Fatalf("stderr line %q is not JSON: %v\nfull stderr:\n%s", line, err, stderr.String())
		}
	}
	errAttr, _ := last["error"].(string)
	if last["level"] != "ERROR" || !strings.Contains(errAttr, "ENABLE_POSTGRES") {
		t.Errorf("final record = %v, want an ERROR naming ENABLE_POSTGRES in its error attribute", last)
	}
}
