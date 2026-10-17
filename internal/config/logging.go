package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// LogFormat is the encoding of the process's log lines on stderr. The zero
// value is the default, text.
type LogFormat int

const (
	// LogFormatText is slog's key=value text encoding.
	LogFormatText LogFormat = iota
	// LogFormatJSON is slog's one-JSON-object-per-line encoding.
	LogFormatJSON
)

// LogConfig is the operator's logging choice: how verbose, and in which
// encoding. The zero value is the default: info, text.
type LogConfig struct {
	Level  slog.Level
	Format LogFormat
}

// LoadLogging decodes LOG_LEVEL (debug, info, warn or error; default info) and
// LOG_FORMAT (text or json; default text) from the environment. Matching is
// case-insensitive and ignores surrounding whitespace.
//
// An unset or empty variable takes its default. A MALFORMED one is a fatal
// configuration error, matching ParseBoolEnv's stance that a typo is an
// operator error rather than a silently ignored setting, and BOTH variables
// are checked so one error names every offender.
//
// The returned LogConfig is fully formed even on error, with the default in
// place of each bad value, so the caller can build a logger to report the error
// with.
func LoadLogging() (LogConfig, error) {
	var cfg LogConfig
	var errs []error

	switch v := os.Getenv("LOG_LEVEL"); strings.ToLower(strings.TrimSpace(v)) {
	case "", "info":
		cfg.Level = slog.LevelInfo
	case "debug":
		cfg.Level = slog.LevelDebug
	case "warn":
		cfg.Level = slog.LevelWarn
	case "error":
		cfg.Level = slog.LevelError
	default:
		errs = append(errs, fmt.Errorf("invalid log level %q for LOG_LEVEL: want debug, info, warn or error", v))
	}

	switch v := os.Getenv("LOG_FORMAT"); strings.ToLower(strings.TrimSpace(v)) {
	case "", "text":
		cfg.Format = LogFormatText
	case "json":
		cfg.Format = LogFormatJSON
	default:
		errs = append(errs, fmt.Errorf("invalid log format %q for LOG_FORMAT: want text or json", v))
	}

	return cfg, errors.Join(errs...)
}
