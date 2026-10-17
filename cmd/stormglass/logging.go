package main

import (
	"io"
	"log/slog"
	"os"

	"github.com/jacaudi/stormglass/internal/config"
)

// newLogHandler builds the handler that writes log records to w in the format
// and at the level cfg selects.
func newLogHandler(w io.Writer, cfg config.LogConfig) slog.Handler {
	opts := &slog.HandlerOptions{Level: cfg.Level}
	if cfg.Format == config.LogFormatJSON {
		return slog.NewJSONHandler(w, opts)
	}
	return slog.NewTextHandler(w, opts)
}

// initLogging loads LOG_LEVEL and LOG_FORMAT, installs the stderr logger they
// describe as slog's default, and returns the config so configureOTel can
// build the same stderr handler beside the OTel bridge.
//
// Logs go to stderr, not stdout: that is where this process's logs have always
// gone (the default log package writes there), and a container runtime
// captures both streams. It runs before anything else logs, so every later
// record, including the startup diagnostics and fatal errors, honors the
// operator's choice.
//
// A malformed value exits 1, after the logger is installed so the error itself
// is reported in the requested format (the bad value falls back to its
// default). Because slog.SetDefault also routes the stdlib log package into
// the logger, anything a dependency still writes through log follows suit.
func initLogging() config.LogConfig {
	cfg, err := config.LoadLogging()
	slog.SetDefault(slog.New(newLogHandler(os.Stderr, cfg)))
	if err != nil {
		fatal("invalid logging configuration", "error", err)
	}
	return cfg
}

// fatal logs msg and attrs at ERROR through the default logger and exits 1.
// It is the slog form of log.Fatal and keeps its contract: exit code 1, and no
// deferred function runs, so a caller reached after main registered
// cleanupResources must not rely on that cleanup (see decideUI).
func fatal(msg string, attrs ...any) {
	slog.Error(msg, attrs...)
	os.Exit(1)
}
