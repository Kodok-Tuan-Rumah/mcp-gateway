// Package tracing sets up OpenTelemetry distributed tracing for the MCP gateway.
//
// It provides:
// - TracerProvider with OTLP gRPC exporter (optional, controlled by env vars)
// - HTTP middleware for automatic span creation and trace context propagation
// - Helper functions for creating child spans in handlers
package tracing

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	// ServiceName is the name used in traces.
	ServiceName = "mcp-gateway"

	// DefaultOTLPEndpoint is the default OTLP gRPC endpoint.
	DefaultOTLPEndpoint = "localhost:4317"

	// TraceIDHeader is the HTTP header used for trace ID propagation.
	TraceIDHeader = "X-Trace-ID"

	// RequestIDHeader is the HTTP header used for request ID (existing).
	RequestIDHeader = "X-Request-ID"
)

// Config holds tracing configuration.
type Config struct {
	Enabled     bool
	OTLPEndpoint string
	ServiceName  string
}

// LoadConfig reads tracing configuration from environment variables.
func LoadConfig() Config {
	cfg := Config{
		Enabled:      false,
		OTLPEndpoint: DefaultOTLPEndpoint,
		ServiceName:  ServiceName,
	}

	if v := os.Getenv("OTEL_ENABLED"); v == "true" || v == "1" {
		cfg.Enabled = true
	}
	if v := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); v != "" {
		cfg.OTLPEndpoint = v
	}
	if v := os.Getenv("OTEL_SERVICE_NAME"); v != "" {
		cfg.ServiceName = v
	}

	return cfg
}

// InitTracer sets up the global TracerProvider with OTLP gRPC exporter.
// Returns a shutdown function that must be called on application exit.
func InitTracer(cfg Config) (func(context.Context) error, error) {
	if !cfg.Enabled {
		return func(context.Context) error { return nil }, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Strip protocol prefix for gRPC endpoint (expects host:port)
	endpoint := cfg.OTLPEndpoint
	endpoint = strings.TrimPrefix(endpoint, "http://")
	endpoint = strings.TrimPrefix(endpoint, "https://")
	// Default to gRPC port if URL port is HTTP port
	if strings.HasSuffix(endpoint, ":4318") {
		endpoint = strings.TrimSuffix(endpoint, ":4318") + ":4317"
	}

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}

// Tracer returns the global tracer for the given name.
func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}

// SpanFromContext returns the current span from context.
func SpanFromContext(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}

// TraceIDFromContext extracts the trace ID from the current span.
func TraceIDFromContext(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().HasTraceID() {
		return ""
	}
	return span.SpanContext().TraceID().String()
}

// SpanIDFromContext extracts the span ID from the current span.
func SpanIDFromContext(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().HasSpanID() {
		return ""
	}
	return span.SpanContext().SpanID().String()
}

// NewSpan creates a new child span with the given name.
func NewSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	tracer := otel.Tracer(ServiceName)
	ctx, span := tracer.Start(ctx, name)
	for _, a := range attrs {
		span.SetAttributes(a)
	}
	return ctx, span
}

// AddEvent adds an event to the current span.
func AddEvent(ctx context.Context, name string, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	span.AddEvent(name, trace.WithAttributes(attrs...))
}

// SetError records an error on the current span.
func SetError(ctx context.Context, err error) {
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
	span.SetAttributes(attribute.Bool("error", true))
}