// logbridge.go bridges log/slog to OTel. It is kept to this one file (and
// this one function) so a bump of the still-experimental
// go.opentelemetry.io/contrib/bridges/otelslog API is a one-file change,
// matching setup.go's isolation of the experimental log signal.

package otel

import (
	"log/slog"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/log"
)

// logBridgeName is the instrumentation scope name for the slog bridge,
// mirroring meterName/tracerName/serviceName's "tempestwx" value as this
// package's identity.
const logBridgeName = "tempestwx"

// NewSlogHandler returns an slog.Handler that forwards every record to lp.
// The active span's trace and span IDs are attached automatically: otelslog
// forwards the same context it is given straight through to the underlying
// log.Logger.Emit, and it is the SDK logger (go.opentelemetry.io/otel/sdk/log)
// that reads trace.SpanContextFromContext(ctx) when building the exported
// Record — not otelslog itself.
func NewSlogHandler(lp log.LoggerProvider) slog.Handler {
	return otelslog.NewHandler(logBridgeName, otelslog.WithLoggerProvider(lp))
}
