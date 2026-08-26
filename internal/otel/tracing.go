// tracing.go orchestrates the trace spans around one UDP datagram's ingest
// (TracedIngest) and one API-export batch send (TraceExportBatch). Both are
// unit-testable here without a UDP socket or a real sink/API client: main.go
// binds the socket and owns the sink, so parse/send are injected as plain
// funcs matching tempestudp.ParseReport and sink.MetricsSink.SendReport's
// signatures. internal/otel already imports tempestudp; it deliberately does
// NOT import internal/sink, to avoid an import cycle (sink depends on the
// writers this package implements) — hence the injection.

package otel

import (
	"context"
	"fmt"

	"github.com/jacaudi/stormglass/internal/tempestudp"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// tracerName is the instrumentation scope name for every span this package
// starts. It intentionally mirrors meterName/serviceName's value ("stormglass")
// as this package's identity, but is declared separately per each file's
// existing pattern of naming its own scope constant.
const tracerName = "stormglass"

// Span name constants — exact names asserted by tracing_test.go and expected
// by the trace backend.
const (
	spanUDPReceive  = "udp.receive"
	spanReportParse = "report.parse"
	spanSinkWrite   = "sink.write"
	spanExportBatch = "export.batch"
)

// recordSpanError records err as a span event and marks the span's status as
// Error, the two-call sequence go.opentelemetry.io/otel/trace.Span requires
// (RecordError alone does not change status).
func recordSpanError(span trace.Span, err error) {
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

// TracedIngest wraps one UDP datagram's ingest in a three-span chain:
// udp.receive (parent) -> report.parse (child, around parse) and sink.write
// (child, around send). Both children are started from udp.receive's
// context, so they are siblings under it rather than nested.
//
// A parse error is recorded on report.parse, and send is skipped entirely —
// mirroring the caller's existing log-and-continue behavior for malformed
// datagrams (see main.go's listenAndPushWithSink). The error is wrapped with
// its stage ("parse report"/"send report") and returned rather than
// swallowed, so the caller can still log it; it is never silently dropped.
func TracedIngest(
	ctx context.Context,
	raw []byte,
	parse func([]byte) (tempestudp.Report, error),
	send func(context.Context, tempestudp.Report) error,
) error {
	tracer := otel.Tracer(tracerName)

	ctx, receiveSpan := tracer.Start(ctx, spanUDPReceive)
	defer receiveSpan.End()

	_, parseSpan := tracer.Start(ctx, spanReportParse)
	report, err := parse(raw)
	if err != nil {
		wrapped := fmt.Errorf("parse report: %w", err)
		recordSpanError(parseSpan, wrapped)
		parseSpan.End()
		return wrapped
	}
	parseSpan.End()

	sendCtx, sendSpan := tracer.Start(ctx, spanSinkWrite)
	defer sendSpan.End()
	if err := send(sendCtx, report); err != nil {
		wrapped := fmt.Errorf("send report: %w", err)
		recordSpanError(sendSpan, wrapped)
		return wrapped
	}
	return nil
}

// TraceExportBatch wraps a single API-export batch send in an export.batch
// span, mirroring TracedIngest's sink.write child for the UDP path so export
// batches show up in the trace backend the same way. fn is injected
// (matching TracedIngest's send-injection pattern) so this stays free of a
// sink import; the caller (main.go's exportWithSink) supplies a closure over
// its already-configured sink.
func TraceExportBatch(ctx context.Context, fn func(context.Context) error) error {
	tracer := otel.Tracer(tracerName)

	ctx, span := tracer.Start(ctx, spanExportBatch)
	defer span.End()

	if err := fn(ctx); err != nil {
		recordSpanError(span, err)
		return err
	}
	return nil
}
