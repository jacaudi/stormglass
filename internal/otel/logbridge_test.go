package otel

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// capturingExporter is a real sdklog.Exporter (not a mock) that appends every
// exported Record to a slice, guarded by a mutex since Export is documented
// as never called concurrently with itself but the test reads records from
// the goroutine under test.
type capturingExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (e *capturingExporter) Export(_ context.Context, records []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.records = append(e.records, records...)
	return nil
}

func (e *capturingExporter) Shutdown(context.Context) error   { return nil }
func (e *capturingExporter) ForceFlush(context.Context) error { return nil }

func (e *capturingExporter) captured() []sdklog.Record {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.records
}

// attrValue returns the string value of the named attribute on rec, or
// ("", false) if absent.
func attrValue(rec sdklog.Record, key string) (string, bool) {
	var (
		value string
		found bool
	)
	// otel/log v0.21.0 removed log.KeyValue/log.Value and moved the attribute
	// model onto go.opentelemetry.io/otel/attribute, so WalkAttributes now hands
	// back an attribute.KeyValue. attribute.Key is its own string type, hence the
	// conversion below rather than a direct comparison.
	rec.WalkAttributes(func(kv attribute.KeyValue) bool {
		if string(kv.Key) == key {
			value = kv.Value.AsString()
			found = true
			return false
		}
		return true
	})
	return value, found
}

// TestLogBridge_EmitsRecords asserts NewSlogHandler wires a real slog.Logger
// through to OTel: the message and attribute survive the conversion, and
// (second case) an active span's trace/span IDs are attached automatically —
// this is sdk/log's Logger.Emit behavior (it reads
// trace.SpanContextFromContext(ctx)), reached because otelslog.Handler.Handle
// forwards the same ctx it is given straight through to logger.Emit.
func TestLogBridge_EmitsRecords(t *testing.T) {
	exporter := &capturingExporter{}
	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewSimpleProcessor(exporter)),
	)
	t.Cleanup(func() { _ = lp.Shutdown(t.Context()) })

	handler := NewSlogHandler(lp)
	logger := slog.New(handler)

	t.Run("message and attribute", func(t *testing.T) {
		logger.InfoContext(t.Context(), "msg", "key", "val")

		records := exporter.captured()
		if len(records) != 1 {
			t.Fatalf("got %d captured records, want 1: %+v", len(records), records)
		}
		rec := records[0]

		if got := rec.Body().AsString(); got != "msg" {
			t.Errorf("Body() = %q, want %q", got, "msg")
		}
		if got, ok := attrValue(rec, "key"); !ok || got != "val" {
			t.Errorf("attribute %q = %q (found=%v), want %q", "key", got, ok, "val")
		}
	})

	t.Run("active span attaches trace and span IDs", func(t *testing.T) {
		tp := sdktrace.NewTracerProvider()
		t.Cleanup(func() { _ = tp.Shutdown(t.Context()) })

		ctx, span := tp.Tracer("logbridge_test").Start(t.Context(), "test-span")
		defer span.End()

		logger.InfoContext(ctx, "traced msg")

		records := exporter.captured()
		last := records[len(records)-1]

		wantTraceID := span.SpanContext().TraceID()
		wantSpanID := span.SpanContext().SpanID()
		if got := last.TraceID(); got != wantTraceID {
			t.Errorf("TraceID() = %s, want %s", got, wantTraceID)
		}
		if got := last.SpanID(); got != wantSpanID {
			t.Errorf("SpanID() = %s, want %s", got, wantSpanID)
		}
	})
}
