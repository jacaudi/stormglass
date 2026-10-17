package config

import (
	"log/slog"
	"strings"
	"testing"
)

func TestLoadLogging(t *testing.T) {
	tests := []struct {
		name       string
		level      string
		format     string
		wantLevel  slog.Level
		wantFormat LogFormat
	}{
		{name: "unset takes the defaults", wantLevel: slog.LevelInfo, wantFormat: LogFormatText},
		{name: "debug", level: "debug", wantLevel: slog.LevelDebug, wantFormat: LogFormatText},
		{name: "info", level: "info", wantLevel: slog.LevelInfo, wantFormat: LogFormatText},
		{name: "warn", level: "warn", wantLevel: slog.LevelWarn, wantFormat: LogFormatText},
		{name: "error", level: "error", wantLevel: slog.LevelError, wantFormat: LogFormatText},
		{name: "level is case-insensitive", level: "DEBUG", wantLevel: slog.LevelDebug, wantFormat: LogFormatText},
		{name: "level tolerates padding", level: " warn ", wantLevel: slog.LevelWarn, wantFormat: LogFormatText},
		{name: "json", format: "json", wantLevel: slog.LevelInfo, wantFormat: LogFormatJSON},
		{name: "text", format: "text", wantLevel: slog.LevelInfo, wantFormat: LogFormatText},
		{name: "format is case-insensitive", format: "JSON", wantLevel: slog.LevelInfo, wantFormat: LogFormatJSON},
		{name: "both set", level: "error", format: "json", wantLevel: slog.LevelError, wantFormat: LogFormatJSON},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LOG_LEVEL", tt.level)
			t.Setenv("LOG_FORMAT", tt.format)

			got, err := LoadLogging()
			if err != nil {
				t.Fatalf("LoadLogging() unexpected error: %v", err)
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
// so main can build a logger to report the error with.
func TestLoadLogging_MalformedIsReportedTogether(t *testing.T) {
	tests := []struct {
		name       string
		level      string
		format     string
		wantNames  []string
		wantLevel  slog.Level
		wantFormat LogFormat
	}{
		{
			name: "bad level", level: "verbose", format: "json",
			wantNames: []string{"LOG_LEVEL", `"verbose"`}, wantLevel: slog.LevelInfo, wantFormat: LogFormatJSON,
		},
		{
			name: "bad format", level: "debug", format: "yaml",
			wantNames: []string{"LOG_FORMAT", `"yaml"`}, wantLevel: slog.LevelDebug, wantFormat: LogFormatText,
		},
		{
			name: "both bad", level: "loud", format: "xml",
			wantNames: []string{"LOG_LEVEL", `"loud"`, "LOG_FORMAT", `"xml"`}, wantLevel: slog.LevelInfo, wantFormat: LogFormatText,
		},
		{
			// slog.Level.UnmarshalText accepts "INFO+2"; the documented set is the four names.
			name: "slog offset syntax is not accepted", level: "info+2",
			wantNames: []string{"LOG_LEVEL", `"info+2"`}, wantLevel: slog.LevelInfo, wantFormat: LogFormatText,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LOG_LEVEL", tt.level)
			t.Setenv("LOG_FORMAT", tt.format)

			got, err := LoadLogging()
			if err == nil {
				t.Fatalf("LoadLogging() = %+v, nil; want an error", got)
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
