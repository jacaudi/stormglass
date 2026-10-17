package logging

import (
	"bytes"
	"encoding/json"
	"log"
	"log/slog"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name       string
		level      string
		format     string
		wantLevel  slog.Level
		wantFormat Format
	}{
		{name: "unset takes the defaults", wantLevel: slog.LevelInfo, wantFormat: FormatText},
		{name: "debug", level: "debug", wantLevel: slog.LevelDebug, wantFormat: FormatText},
		{name: "info", level: "info", wantLevel: slog.LevelInfo, wantFormat: FormatText},
		{name: "warn", level: "warn", wantLevel: slog.LevelWarn, wantFormat: FormatText},
		{name: "error", level: "error", wantLevel: slog.LevelError, wantFormat: FormatText},
		{name: "level is case-insensitive", level: "DEBUG", wantLevel: slog.LevelDebug, wantFormat: FormatText},
		{name: "level tolerates padding", level: " warn ", wantLevel: slog.LevelWarn, wantFormat: FormatText},
		{name: "json", format: "json", wantLevel: slog.LevelInfo, wantFormat: FormatJSON},
		{name: "text", format: "text", wantLevel: slog.LevelInfo, wantFormat: FormatText},
		{name: "format is case-insensitive", format: "JSON", wantLevel: slog.LevelInfo, wantFormat: FormatJSON},
		{name: "both set", level: "error", format: "json", wantLevel: slog.LevelError, wantFormat: FormatJSON},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LOG_LEVEL", tt.level)
			t.Setenv("LOG_FORMAT", tt.format)

			got, err := load()
			if err != nil {
				t.Fatalf("load() unexpected error: %v", err)
			}
			if got.Level != tt.wantLevel {
				t.Errorf("Level = %v, want %v", got.Level, tt.wantLevel)
			}
			if got.Format != tt.wantFormat {
				t.Errorf("Format = %v, want %v", got.Format, tt.wantFormat)
			}
		})
	}
}

// A malformed value is a fatal configuration error that names the variable and
// the offending value, and every offender is reported in one error. The
// returned config is still fully formed (defaults in place of the bad values)
// so the caller can build a logger to report the error with.
func TestLoad_MalformedIsReportedTogether(t *testing.T) {
	tests := []struct {
		name       string
		level      string
		format     string
		wantNames  []string
		wantLevel  slog.Level
		wantFormat Format
	}{
		{
			name: "bad level", level: "verbose", format: "json",
			wantNames: []string{"LOG_LEVEL", `"verbose"`}, wantLevel: slog.LevelInfo, wantFormat: FormatJSON,
		},
		{
			name: "bad format", level: "debug", format: "yaml",
			wantNames: []string{"LOG_FORMAT", `"yaml"`}, wantLevel: slog.LevelDebug, wantFormat: FormatText,
		},
		{
			name: "both bad", level: "loud", format: "xml",
			wantNames: []string{"LOG_LEVEL", `"loud"`, "LOG_FORMAT", `"xml"`}, wantLevel: slog.LevelInfo, wantFormat: FormatText,
		},
		{
			// slog.Level.UnmarshalText accepts "INFO+2"; the documented set is the four names.
			name: "slog offset syntax is not accepted", level: "info+2",
			wantNames: []string{"LOG_LEVEL", `"info+2"`}, wantLevel: slog.LevelInfo, wantFormat: FormatText,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LOG_LEVEL", tt.level)
			t.Setenv("LOG_FORMAT", tt.format)

			got, err := load()
			if err == nil {
				t.Fatalf("load() = %+v, nil; want an error", got)
			}
			for _, want := range tt.wantNames {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err.Error(), want)
				}
			}
			if got.Level != tt.wantLevel {
				t.Errorf("Level = %v, want %v", got.Level, tt.wantLevel)
			}
			if got.Format != tt.wantFormat {
				t.Errorf("Format = %v, want %v", got.Format, tt.wantFormat)
			}
		})
	}
}

func TestNewHandler(t *testing.T) {
	t.Run("text format is key=value", func(t *testing.T) {
		var buf bytes.Buffer
		slog.New(NewHandler(&buf, Config{Format: FormatText})).Info("hello", "port", 8080)

		out := buf.String()
		for _, want := range []string{"level=INFO", "msg=hello", "port=8080"} {
			if !strings.Contains(out, want) {
				t.Errorf("text output %q does not contain %q", out, want)
			}
		}
	})

	t.Run("json format is one JSON object per line", func(t *testing.T) {
		var buf bytes.Buffer
		slog.New(NewHandler(&buf, Config{Format: FormatJSON})).Info("hello", "port", 8080)

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
			l := slog.New(NewHandler(&buf, Config{Level: tt.level}))
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

// Init installs the logger BEFORE it reports a bad value, so the caller's fatal
// error is itself written to w in the requested format, with the bad value
// replaced by its default.
func TestInit_InstallsLoggerEvenOnError(t *testing.T) {
	// slog.SetDefault also redirects the stdlib log package, and restoring the
	// previous slog logger does not undo that, so restore log's output too.
	prevLogger, prevOut, prevFlags := slog.Default(), log.Writer(), log.Flags()
	t.Cleanup(func() {
		slog.SetDefault(prevLogger)
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})
	t.Setenv("LOG_LEVEL", "bogus")
	t.Setenv("LOG_FORMAT", "json")

	var buf bytes.Buffer
	cfg, err := Init(&buf)
	if err == nil || !strings.Contains(err.Error(), "LOG_LEVEL") {
		t.Fatalf("Init() error = %v, want one naming LOG_LEVEL", err)
	}
	if cfg.Level != slog.LevelInfo || cfg.Format != FormatJSON {
		t.Errorf("Init() config = %+v, want info (the default for the bad level) and json", cfg)
	}

	slog.Debug("below-the-default-level")
	slog.Info("reported")
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("default logger wrote %q, want exactly one JSON record: %v", buf.String(), err)
	}
	if got["msg"] != "reported" {
		t.Errorf("default logger record = %v, want msg=reported", got)
	}
}
