// Package otel configures the unified OpenTelemetry (OTLP) backbone: a
// Resource identifying this process, plus a MeterProvider, TracerProvider,
// and LoggerProvider each wired to an OTLP gRPC exporter. It is the single
// place OTel is set up, so a later SDK bump (particularly the still-
// experimental log signal) is a one-file change.
//
// Setup registers the MeterProvider and TracerProvider as OTel's global
// providers (otel.SetMeterProvider / otel.SetTracerProvider) and the
// LoggerProvider as the global log provider
// (go.opentelemetry.io/otel/log/global.SetLoggerProvider). Callers obtain
// them afterward via the standard OTel accessors: otel.GetMeterProvider(),
// otel.GetTracerProvider(), and log/global.GetLoggerProvider() — no
// additional accessor is exposed here.
package otel

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	logglobal "go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
)

// serviceName is the fixed OTel service.name attribute for this application.
const serviceName = "stormglass"

// Config configures Setup.
type Config struct {
	// Endpoint is the OTLP gRPC collector address (OTEL_EXPORTER_OTLP_ENDPOINT).
	// Empty means the caller decides whether to call Setup at all; main gates
	// on this being set.
	Endpoint string
	// ServiceVersion is the running binary's version, recorded as the
	// service.version resource attribute.
	ServiceVersion string
	// Serial is the Tempest station serial number, recorded as the
	// stormglass.serial resource attribute.
	Serial string
}

// newResource builds the Resource identifying this process: service.name,
// service.version, host.name, and the station's stormglass.serial.
//
// resource.WithHost() adds host.name via a Detector (os.Hostname()), which
// can return an error wrapping resource.ErrPartialResource on the rare
// system where the hostname lookup fails — per resource.New's documented
// contract, that error still carries a usable (partial) Resource, so it is
// tolerated here rather than treated as fatal; any other error still fails
// Setup, matching the pre-existing behavior.
func newResource(ctx context.Context, cfg Config) (*resource.Resource, error) {
	res, err := resource.New(ctx,
		resource.WithHost(),
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
			attribute.String("stormglass.serial", cfg.Serial),
		),
	)
	if err != nil && !errors.Is(err, resource.ErrPartialResource) {
		return nil, fmt.Errorf("build otel resource: %w", err)
	}
	return res, nil
}

// Setup configures the global MeterProvider, TracerProvider, and
// LoggerProvider, each exporting via OTLP gRPC to cfg.Endpoint, and returns
// an idempotent shutdown function that flushes and stops all three,
// aggregating their Shutdown errors via errors.Join.
//
// The OTLP gRPC exporters connect lazily: construction (this call) does not
// dial cfg.Endpoint. The connection is established in the background on
// first export, so Setup returns successfully even if no collector is
// listening yet.
//
// Note on shutdown and an unreachable collector: the metric SDK's periodic
// reader always performs one final collect-and-export on Shutdown, even if
// no instrument ever recorded a value (go.opentelemetry.io/otel/sdk/metric's
// PeriodicReader.Shutdown has no empty-data short-circuit). The trace and
// log processors, by contrast, skip their export call when nothing is
// queued. So if the collector is unreachable, the returned shutdown
// function's first call can legitimately report a metrics-upload error —
// that error is real and is not swallowed. What shutdown does guarantee is
// idempotency: it is wrapped in sync.Once, so a second call never repeats
// the network attempt and always returns the same cached result.
func Setup(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	res, err := newResource(ctx, cfg)
	if err != nil {
		return nil, err
	}

	traceExporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.Endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("create otlp trace exporter: %w", err)
	}
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tracerProvider)

	metricExporter, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(cfg.Endpoint),
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("create otlp metric exporter: %w", err)
	}
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(meterProvider)

	// The OTLP log signal is experimental (see package doc); its wiring is
	// isolated to this block so an API bump only touches this file.
	logExporter, err := otlploggrpc.New(ctx,
		otlploggrpc.WithEndpoint(cfg.Endpoint),
		otlploggrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("create otlp log exporter: %w", err)
	}
	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
		sdklog.WithResource(res),
	)
	logglobal.SetLoggerProvider(loggerProvider)

	var (
		once        sync.Once
		shutdownErr error
	)
	return func(ctx context.Context) error {
		once.Do(func() {
			shutdownErr = errors.Join(
				tracerProvider.Shutdown(ctx),
				meterProvider.Shutdown(ctx),
				loggerProvider.Shutdown(ctx),
			)
		})
		return shutdownErr
	}, nil
}
