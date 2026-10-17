package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// allLevels is a handler that enables every level, as the OTel log bridge
// does. Only the tee's own level can be what filters records it receives.
func allLevels(buf *bytes.Buffer) slog.Handler {
	return slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
}

// Every record reaches every handler: installing the OTel bridge as the sole
// slog default would take records (and stdlib log output) off stderr.
func TestNewTee_FansOutToAllHandlers(t *testing.T) {
	var otelBuf, stderrBuf bytes.Buffer
	logger := slog.New(NewTee(slog.LevelDebug, allLevels(&otelBuf), allLevels(&stderrBuf)))
	logger.InfoContext(t.Context(), "tee test message", "key", "val")

	for name, buf := range map[string]*bytes.Buffer{"first": &otelBuf, "second": &stderrBuf} {
		if !strings.Contains(buf.String(), "tee test message") || !strings.Contains(buf.String(), "key=val") {
			t.Errorf("%s handler got %q, want the record with its attrs", name, buf.String())
		}
	}
}

// LOG_LEVEL must govern the OTel log export as well as stderr: an operator who
// raises the level to cut noise (or OTLP volume) expects both outputs to go
// quiet. The OTel bridge enables every level, so the tee has to do the gating.
func TestNewTee_LevelGatesEveryHandler(t *testing.T) {
	var otelBuf, stderrBuf bytes.Buffer
	h := NewTee(slog.LevelWarn, allLevels(&otelBuf), allLevels(&stderrBuf))
	logger := slog.New(h)
	logger.InfoContext(t.Context(), "below-the-level")
	logger.WarnContext(t.Context(), "at-the-level")
	// slog.Logger consults Enabled first; a caller that goes straight to Handle
	// must be gated too.
	if err := h.Handle(t.Context(), slog.NewRecord(time.Now(), slog.LevelInfo, "below-the-level", 0)); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if logger.Enabled(t.Context(), slog.LevelInfo) {
		t.Error("tee reports Info enabled below its level; callers would build records for nothing")
	}
	for name, buf := range map[string]*bytes.Buffer{"first": &otelBuf, "second": &stderrBuf} {
		if strings.Contains(buf.String(), "below-the-level") || !strings.Contains(buf.String(), "at-the-level") {
			t.Errorf("%s handler got %q, want only the warn record", name, buf.String())
		}
	}
}

// The level must survive With: slog.Logger.With calls WithAttrs, and a derived
// tee that dropped its level would silently re-enable every record.
func TestNewTee_LevelSurvivesWithAttrsAndGroup(t *testing.T) {
	var buf bytes.Buffer
	base := NewTee(slog.LevelWarn, allLevels(&buf))

	for name, h := range map[string]slog.Handler{
		"WithAttrs": base.WithAttrs([]slog.Attr{slog.String("k", "v")}),
		"WithGroup": base.WithGroup("g"),
	} {
		if h.Enabled(t.Context(), slog.LevelInfo) {
			t.Errorf("%s: derived handler enables Info below the tee's Warn level", name)
		}
	}
}
