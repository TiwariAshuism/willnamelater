// Package telemetry wires OpenTelemetry tracing for the backend. It exposes a
// single Setup entry point that installs a global tracer provider exporting
// over OTLP/gRPC, and a Provider whose Shutdown flushes buffered spans on exit.
//
// Tracing is optional: when no endpoint is configured, Setup installs an
// explicit no-op provider so that instrumentation elsewhere in the codebase is
// always safe to call without nil checks.
package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// Config controls tracer provider setup.
type Config struct {
	// Endpoint is the OTLP/gRPC collector address, e.g. "otel-collector:4317".
	// When empty, tracing is disabled and Setup installs a no-op provider.
	Endpoint string
	// Insecure disables transport security for the exporter connection. It is
	// intended for local collectors reached over a trusted network only.
	Insecure bool
	// ServiceName and ServiceVersion identify this service in the trace backend.
	ServiceName    string
	ServiceVersion string
	// SampleRatio is the fraction of root traces to sample, in [0,1]. A
	// non-positive value samples nothing; a value >= 1 samples everything.
	// Sampling is parent-based, so a sampled inbound trace is always continued.
	SampleRatio float64
}

// Provider owns the lifecycle of the installed tracer provider. Its zero value
// is usable and its Shutdown is a no-op, so a Provider is always safe to defer.
type Provider struct {
	shutdown func(context.Context) error
}

// Setup installs the global tracer provider and text-map propagator according
// to cfg and returns a Provider to shut it down.
//
// A returned error means the exporter or resource could not be built; in that
// case no global provider is changed and the caller should treat startup as
// failed. When cfg.Endpoint is empty the no-op provider is installed and the
// returned error is always nil.
func Setup(ctx context.Context, cfg Config) (*Provider, error) {
	if cfg.Endpoint == "" {
		otel.SetTracerProvider(tracenoop.NewTracerProvider())
		return &Provider{}, nil
	}

	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.Endpoint)}
	if cfg.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}

	exporter, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("telemetry: create otlp exporter: %w", err)
	}

	res, err := resource.New(ctx, resource.WithAttributes(
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion(cfg.ServiceVersion),
	))
	if err != nil {
		// Best-effort close so a failed setup does not leak the gRPC connection.
		_ = exporter.Shutdown(ctx)
		return nil, fmt.Errorf("telemetry: build resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return &Provider{shutdown: tp.Shutdown}, nil
}

// Shutdown flushes and releases the tracer provider. It is safe to call on a
// nil Provider and on a Provider from a disabled (no-op) setup.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p == nil || p.shutdown == nil {
		return nil
	}
	return p.shutdown(ctx)
}
