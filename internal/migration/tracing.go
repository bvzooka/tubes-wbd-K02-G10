package migration

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// startSpan membuka span milik runner, span query database yang memakai context hasilnya otomatis jadi anaknya
func startSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer("dbplatform/migration").Start(ctx, name, trace.WithAttributes(attrs...))
}

// endSpan menutup span, kalau ada error span ditandai gagal supaya terlihat di trace
func endSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}
