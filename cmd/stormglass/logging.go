package main

import (
	"log/slog"
	"os"

	"github.com/jacaudi/stormglass/internal/logging"
)

// mustInitLogging installs the LOG_LEVEL/LOG_FORMAT logger on stderr as slog's
// default and returns its config, exiting 1 through fatal on a malformed value.
//
// Logs go to stderr, not stdout: that is where this process's logs have always
// gone, and a container runtime captures both streams. Callers run it before
// anything else logs, so every later record, including the startup
// diagnostics and fatal errors, honors the operator's choice; logging.Init
// installs the logger before reporting a bad value, so fatal's record does too.
func mustInitLogging() logging.Config {
	cfg, err := logging.Init(os.Stderr)
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
