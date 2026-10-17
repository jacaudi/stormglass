package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jacaudi/stormglass/internal/config"
	"github.com/jacaudi/stormglass/internal/otel"

	sdklog "go.opentelemetry.io/otel/sdk/log"
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

func TestNewLogHandler(t *testing.T) {
	t.Run("text format is key=value", func(t *testing.T) {
		var buf bytes.Buffer
		slog.New(newLogHandler(&buf, config.LogConfig{Format: config.LogFormatText})).
			Info("hello", "port", 8080)

		out := buf.String()
		for _, want := range []string{"level=INFO", "msg=hello", "port=8080"} {
			if !strings.Contains(out, want) {
				t.Errorf("text output %q does not contain %q", out, want)
			}
		}
	})

	t.Run("json format is one JSON object per line", func(t *testing.T) {
		var buf bytes.Buffer
		slog.New(newLogHandler(&buf, config.LogConfig{Format: config.LogFormatJSON})).
			Info("hello", "port", 8080)

		var got map[string]any
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("json output %q is not valid JSON: %v", buf.String(), err)
		}
		if got["level"] != "INFO" || got["msg"] != "hello" || got["port"] != float64(8080) {
			t.Errorf("json record = %v, want level=INFO msg=hello port=8080", got)
		}
	})

	t.Run("level filters records below it", func(t *testing.T) {
		tests := []struct {
			level     slog.Level
			wantDebug bool
			wantInfo  bool
			wantWarn  bool
		}{
			{slog.LevelDebug, true, true, true},
			{slog.LevelInfo, false, true, true},
			{slog.LevelWarn, false, false, true},
			{slog.LevelError, false, false, false},
		}
		for _, tt := range tests {
			var buf bytes.Buffer
			l := slog.New(newLogHandler(&buf, config.LogConfig{Level: tt.level}))
			l.Debug("a-debug")
			l.Info("a-info")
			l.Warn("a-warn")

			out := buf.String()
			for name, want := range map[string]bool{"a-debug": tt.wantDebug, "a-info": tt.wantInfo, "a-warn": tt.wantWarn} {
				if got := strings.Contains(out, name); got != want {
					t.Errorf("level %v: emitted %s = %v, want %v\n%s", tt.level, name, got, want, out)
				}
			}
		}
	})
}

// LOG_LEVEL must govern the OTel log export as well as stderr: an operator who
// raises the level to cut noise (or OTLP volume) expects both outputs to go
// quiet. The OTel bridge enables every level, so the tee has to do the gating.
func TestTeeHandler_LevelGatesEveryHandler(t *testing.T) {
	exporter := &capturingExporter{}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exporter)))
	t.Cleanup(func() { _ = lp.Shutdown(t.Context()) })

	var stderrBuf bytes.Buffer
	// The stderr handler is deliberately left at its own default (info) here,
	// below the tee's warn: only the tee's level can be what stops "info".
	stderrHandler := slog.NewTextHandler(&stderrBuf, &slog.HandlerOptions{Level: slog.LevelDebug})

	logger := slog.New(newTeeHandler(slog.LevelWarn, otel.NewSlogHandler(lp), stderrHandler))
	logger.InfoContext(t.Context(), "below-the-level")
	logger.WarnContext(t.Context(), "at-the-level")

	if logger.Enabled(t.Context(), slog.LevelInfo) {
		t.Error("tee reports Info enabled below its level; callers would build records for nothing")
	}
	if strings.Contains(stderrBuf.String(), "below-the-level") || !strings.Contains(stderrBuf.String(), "at-the-level") {
		t.Errorf("stderr = %q, want only the warn record", stderrBuf.String())
	}
	records := exporter.captured()
	if len(records) != 1 || records[0].Body().AsString() != "at-the-level" {
		t.Fatalf("OTel exporter captured %d records, want exactly the warn record: %+v", len(records), records)
	}
}

// The level must survive With: slog.Logger.With calls WithAttrs, and a derived
// tee that dropped its level would silently re-enable every record.
func TestTeeHandler_LevelSurvivesWithAttrsAndGroup(t *testing.T) {
	var buf bytes.Buffer
	base := newTeeHandler(slog.LevelWarn, slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	for name, h := range map[string]slog.Handler{
		"WithAttrs": base.WithAttrs([]slog.Attr{slog.String("k", "v")}),
		"WithGroup": base.WithGroup("g"),
	} {
		if h.Enabled(context.Background(), slog.LevelInfo) {
			t.Errorf("%s: derived handler enables Info below the tee's Warn level", name)
		}
	}
}

// Both malformed values must abort startup naming every offender, and the
// process must exit 1 like the log.Fatal it replaces.
func TestMalformedLogConfigIsFatalAndNamesEveryOffender(t *testing.T) {
	bin := buildTestBinary(t)

	cmd := exec.CommandContext(t.Context(), bin)
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
}
