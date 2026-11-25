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

// SetupTracing memasang tracer provider global. exporter: console, otlp, atau none.
// Fungsi yang dikembalikan wajib dipanggil saat proses berhenti agar span terakhir terkirim.
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
			opts = append(opts, otlptracehttp.WithEndpointURL(tracesURL(endpoint)))
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
		sdktrace.WithResource(resource.NewSchemaless(semconv.ServiceName(service))),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// http://localhost:4318 -> http://localhost:4318/v1/traces
func tracesURL(endpoint string) string {
	endpoint = strings.TrimRight(endpoint, "/")
	if strings.HasSuffix(endpoint, "/v1/traces") {
		return endpoint
	}
	return endpoint + "/v1/traces"
}
