package telemetry

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// SetupTracing memacer provider global. exporter: console, otlp, atau none.
// Fungsi yang diasang trkembalikan wajib dipanggil saat proses berhenti agar span terakhir terkirim.
func SetupTracing(ctx context.Context, service, exporter, endpoint string) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	if exporter == "none" {
		return func(context.Context) error { return nil }, nil
	}

	var exp sdktrace.SpanExporter
	var err error
	switch exporter {
	case "otlp":
		opts := []otlptracehttp.Option{otlptracehttp.WithInsecure()}
		if endpoint != "" {
			opts = append(opts, otlptracehttp.WithEndpointURL(signalURL(endpoint, "traces")))
		}
		exp, err = otlptracehttp.New(ctx, opts...)
	default:
		exp, err = stdouttrace.New(stdouttrace.WithWriter(os.Stdout))
	}
	if err != nil {
		return nil, fmt.Errorf("trace exporter %s: %w", exporter, err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(serviceResource(service)),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// menandai span dan log dengan nama service yang sama (service.name).
func serviceResource(service string) *resource.Resource {
	return resource.NewSchemaless(semconv.ServiceName(service))
}

// signalURL melengkapi endpoint OTLP/HTTP dengan path sinyalnya:
// http://localhost:4318 + traces -> http://localhost:4318/v1/traces
func signalURL(endpoint, signal string) string {
	endpoint = strings.TrimRight(endpoint, "/")
	path := "/v1/" + signal
	if strings.HasSuffix(endpoint, path) {
		return endpoint
	}
	return endpoint + path
}
