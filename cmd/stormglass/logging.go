package main

import (
	"log/slog"
	"os"
)

// fatal logs msg and attrs at ERROR through the default logger and exits 1.
// It is the slog form of log.Fatal and keeps its contract: exit code 1, and no
// deferred function runs, so a caller reached after main registered
// cleanupResources must not rely on that cleanup (see decideUI).
func fatal(msg string, attrs ...any) {
	slog.Error(msg, attrs...)
	os.Exit(1)
}
