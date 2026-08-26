package otel

import (
	"context"
	"errors"
	"testing"

	"github.com/jacaudi/stormglass/internal/tempestudp"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// spansByName indexes ended spans by name for lookup in assertions. Test
// span names are distinct constants, so collisions cannot occur.
func spansByName(spans []sdktrace.ReadOnlySpan) map[string]sdktrace.ReadOnlySpan {
	byName := make(map[string]sdktrace.ReadOnlySpan, len(spans))
	for _, s := range spans {
		byName[s.Name()] = s
	}
	return byName
}

// TestSpans_UDPIngestChain asserts the udp.receive -> report.parse ->
// sink.write span chain TracedIngest produces, using a real SpanRecorder
// (not mocks) so parent/child links reflect actual SDK behavior.
func TestSpans_UDPIngestChain(t *testing.T) {
	t.Run("parse and send succeed", func(t *testing.T) {
		sr := tracetest.NewSpanRecorder()
		otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))

		fakeReport := &tempestudp.HubStatusReport{}
		parse := func(b []byte) (tempestudp.Report, error) { return fakeReport, nil }
		sendCalled := false
		send := func(ctx context.Context, r tempestudp.Report) error {
			sendCalled = true
			if r != fakeReport {
				t.Errorf("send received report %#v, want the parsed report %#v", r, fakeReport)
			}
			return nil
		}

		if err := TracedIngest(t.Context(), []byte("raw"), parse, send); err != nil {
			t.Fatalf("TracedIngest() = %v, want nil", err)
		}
		if !sendCalled {
			t.Fatal("send was not called; want it invoked after a successful parse")
		}

		spans := sr.Ended()
		if len(spans) != 3 {
			t.Fatalf("got %d ended spans, want 3: %+v", len(spans), spans)
		}

		byName := spansByName(spans)
		receive, ok := byName[spanUDPReceive]
		if !ok {
			t.Fatalf("missing %s span", spanUDPReceive)
		}
		parseSpan, ok := byName[spanReportParse]
		if !ok {
			t.Fatalf("missing %s span", spanReportParse)
		}
		sendSpan, ok := byName[spanSinkWrite]
		if !ok {
			t.Fatalf("missing %s span", spanSinkWrite)
		}

		receiveSpanID := receive.SpanContext().SpanID()
		if got := parseSpan.Parent().SpanID(); got != receiveSpanID {
			t.Errorf("%s.Parent().SpanID() = %s, want %s (%s)", spanReportParse, got, receiveSpanID, spanUDPReceive)
		}
		if got := sendSpan.Parent().SpanID(); got != receiveSpanID {
			t.Errorf("%s.Parent().SpanID() = %s, want %s (%s)", spanSinkWrite, got, receiveSpanID, spanUDPReceive)
		}
	})

	t.Run("parse error skips send", func(t *testing.T) {
		sr := tracetest.NewSpanRecorder()
		otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))

		parseErr := errors.New("malformed report")
		parse := func(b []byte) (tempestudp.Report, error) { return nil, parseErr }
		sendCalled := false
		send := func(ctx context.Context, r tempestudp.Report) error {
			sendCalled = true
			return nil
		}

		err := TracedIngest(t.Context(), []byte("raw"), parse, send)
		if !errors.Is(err, parseErr) {
			t.Fatalf("TracedIngest() = %v, want an error wrapping %v", err, parseErr)
		}
		if sendCalled {
			t.Fatal("send was called after a parse error; want parse failure to skip send")
		}

		byName := spansByName(sr.Ended())
		if _, ok := byName[spanSinkWrite]; ok {
			t.Fatalf("%s span exists; want it skipped after a parse error", spanSinkWrite)
		}

		parseSpan, ok := byName[spanReportParse]
		if !ok {
			t.Fatalf("missing %s span", spanReportParse)
		}
		if got := parseSpan.Status().Code; got != codes.Error {
			t.Errorf("%s span status = %v, want %v", spanReportParse, got, codes.Error)
		}
	})
}
