package otel

import (
	"errors"
	"testing"
	"time"

	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
)

// TestSetup_ReturnsShutdown exercises Setup against an unreachable OTLP
// endpoint (nothing listens on localhost:4317 in the test environment). The
// OTLP gRPC exporters connect lazily (grpc.NewClient never dials at
// construction time), so Setup itself must return cleanly with no error even
// though the collector is unreachable.
//
// shutdown's first call may legitimately report an error: the meter
// provider's periodic reader always attempts one final collect+export on
// Shutdown (unlike the trace/log processors, which skip export when nothing
// is queued — see setup.go), and that export fails fast against an
// unreachable collector. The invariant under test is idempotency: a second
// call must not repeat the network attempt (must return near-instantly) and
// must not panic or double-close anything.
func TestSetup_ReturnsShutdown(t *testing.T) {
	ctx := t.Context()

	shutdown, err := Setup(ctx, Config{
		Endpoint:       "localhost:4317",
		ServiceVersion: "v0.0.0-test",
		Serial:         "TEST-001",
	})
	if err != nil {
		t.Fatalf("Setup() returned unexpected error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Setup() returned a nil shutdown func")
	}

	firstErr := shutdown(ctx)

	start := time.Now()
	secondErr := shutdown(ctx)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("second shutdown(ctx) call took %s; want near-instant (idempotent, no re-flush)", elapsed)
	}
	if !errors.Is(secondErr, firstErr) {
		t.Fatalf("second shutdown(ctx) call returned %v, want the cached first-call result %v", secondErr, firstErr)
	}
}

// TestResourceAttributes asserts the built Resource carries the required
// identifying attributes: service.name, service.version, host.name, and
// stormglass.serial (Contract B's resource-attribute list).
func TestResourceAttributes(t *testing.T) {
	ctx := t.Context()

	res, err := newResource(ctx, Config{
		Endpoint:       "localhost:4317",
		ServiceVersion: "v1.2.3",
		Serial:         "ST-00012345",
	})
	if err != nil {
		t.Fatalf("newResource() returned unexpected error: %v", err)
	}

	set := res.Set()

	name, ok := set.Value(semconv.ServiceNameKey)
	if !ok || name.AsString() != "stormglass" {
		t.Errorf("service.name = %v (ok=%v), want %q", name, ok, "stormglass")
	}

	version, ok := set.Value(semconv.ServiceVersionKey)
	if !ok || version.AsString() != "v1.2.3" {
		t.Errorf("service.version = %v (ok=%v), want %q", version, ok, "v1.2.3")
	}

	serial, ok := set.Value("stormglass.serial")
	if !ok || serial.AsString() != "ST-00012345" {
		t.Errorf("stormglass.serial = %v (ok=%v), want %q", serial, ok, "ST-00012345")
	}

	host, ok := set.Value(semconv.HostNameKey)
	if !ok || host.AsString() == "" {
		t.Errorf("host.name = %v (ok=%v), want a non-empty value", host, ok)
	}
}
