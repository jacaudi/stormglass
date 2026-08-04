// Package prometheus is the deprecated bespoke metrics path: a pushgateway
// writer (writer.go) and a scrape server (server.go), both slated for
// removal in favor of ENABLE_OTEL (see deprecationMsg below). It emits a
// one-time WARN the first time either is constructed.
package prometheus

import (
	"log/slog"
	"sync"
)

// deprecationMsg is the one-time warning emitted when the bespoke
// Prometheus path (pushgateway writer and/or scrape server) is used. This is
// deprecate-then-remove (design O4): the Prometheus writers keep working for
// one more release; ENABLE_OTEL is the replacement.
const deprecationMsg = "ENABLE_PROMETHEUS_* is deprecated; migrate to ENABLE_OTEL — removal in the next release"

// newDeprecationWarner returns a func that logs deprecationMsg at WARN via
// logger exactly once, no matter how many times it's called. It exists as a
// seam so tests can exercise "exactly once" with a fresh sync.OnceFunc
// closure and a buffer-backed logger, instead of depending on the shared
// package-level warnDeprecated below (whose sync.OnceFunc state persists
// across the whole test binary).
func newDeprecationWarner(logger *slog.Logger) func() {
	return sync.OnceFunc(func() { logger.Warn(deprecationMsg) })
}

// warnDeprecated is called from both NewPrometheusWriter and
// NewMetricsServer, so it fires exactly once even when both the pushgateway
// writer and the scrape server are enabled.
//
// It resolves slog.Default() at first-call time (not package-init time) so it
// honors whatever logger main installs via slog.SetDefault, rather than
// capturing the built-in default at package initialization. NOTE: main
// registers the Prometheus writers before it installs the OTel slog bridge, so
// in practice this warning is emitted to the pre-bridge stderr default (visible
// in container logs) and is not routed through the OTel log pipeline — an
// accepted limitation for a transitional deprecation notice on a path slated
// for removal in the next release.
var warnDeprecated = sync.OnceFunc(func() { slog.Default().Warn(deprecationMsg) })
