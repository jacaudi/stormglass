package logging

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Format is the encoding of the process's log lines. The zero value is the
// default, text.
type Format int

const (
	// FormatText is slog's key=value text encoding.
	FormatText Format = iota
	// FormatJSON is slog's one-JSON-object-per-line encoding.
	FormatJSON
)

// Config is the operator's logging choice: how verbose, and in which
// encoding. The zero value is the default: info, text.
type Config struct {
	Level  slog.Level
	Format Format
}

// load decodes LOG_LEVEL (debug, info, warn or error; default info) and
// LOG_FORMAT (text or json; default text) from the environment. Matching is
// case-insensitive and ignores surrounding whitespace.
//
// An unset or empty variable takes its default. A MALFORMED one is a fatal
// configuration error, matching config.ParseBoolEnv's stance that a typo is an
// operator error rather than a silently ignored setting, and BOTH variables
// are checked so one error names every offender.
//
// The returned Config is fully formed even on error, with the default in
// place of each bad value, so the caller can build a logger to report the error
// with.
func load() (Config, error) {
	var cfg Config
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
		cfg.Format = FormatText
	case "json":
		cfg.Format = FormatJSON
	default:
		errs = append(errs, fmt.Errorf("invalid log format %q for LOG_FORMAT: want text or json", v))
	}

	return cfg, errors.Join(errs...)
}

// NewHandler builds the handler that writes log records to w in the format
// and at the level cfg selects.
func NewHandler(w io.Writer, cfg Config) slog.Handler {
	opts := &slog.HandlerOptions{Level: cfg.Level}
	if cfg.Format == FormatJSON {
		return slog.NewJSONHandler(w, opts)
	}
	return slog.NewTextHandler(w, opts)
}

// Init loads LOG_LEVEL and LOG_FORMAT, installs a logger writing to w as slog's
// default, and returns the config so a caller can build the same handler again
// (as the OTel tee does).
//
// The logger is installed even when load reports an error, with the default in
// place of each bad value, so the caller's report of that error is itself
// written in the requested format. Init never exits; the caller decides what a
// bad value costs. Because slog.SetDefault also routes the stdlib log package
// into the logger, anything a dependency still writes through log follows suit.
func Init(w io.Writer) (Config, error) {
	cfg, err := load()
	slog.SetDefault(slog.New(NewHandler(w, cfg)))
	return cfg, err
}
