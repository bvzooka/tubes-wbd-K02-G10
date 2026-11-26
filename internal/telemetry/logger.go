package telemetry

import (
	"context"
	"io"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// membuat logger JSON yg otomatis menambahkan trace_id, span_id, dan correlation_id dari context, serta menyamarkan atribut sensitif
// exporters adalah tujuan tambahan, tiap record dikirim ke sana dengan level minimum dan penyamaran yang sama dengan stdout
func NewLogger(w io.Writer, level string, exporters ...slog.Handler) *slog.Logger {
	minLevel := parseLevel(level)
	handlers := []slog.Handler{slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       minLevel,
		ReplaceAttr: redactAttr,
	})}
	for _, e := range exporters {
		if e != nil {
			handlers = append(handlers, exportHandler{Handler: e, minLevel: minLevel})
		}
	}
	return slog.New(contextHandler{slog.NewMultiHandler(handlers...)})
}

func parseLevel(s string) slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(strings.ToUpper(s))); err != nil {
		return slog.LevelInfo
	}
	return l
}

type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := CorrelationID(ctx); id != "" {
		r.AddAttrs(slog.String("correlation_id", id))
	}
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}

// memberi exporter level minimum dan penyamaran yg sama dgn handler JSON, karena exporter tidak mengenal HandlerOptions.ReplaceAttr
type exportHandler struct {
	slog.Handler
	minLevel slog.Level
}

func (h exportHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.minLevel && h.Handler.Enabled(ctx, l)
}

func (h exportHandler) Handle(ctx context.Context, r slog.Record) error {
	clean := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		clean.AddAttrs(redactNested(a))
		return true
	})
	return h.Handler.Handle(ctx, clean)
}

func (h exportHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = redactNested(a)
	}
	return exportHandler{Handler: h.Handler.WithAttrs(clean), minLevel: h.minLevel}
}

func (h exportHandler) WithGroup(name string) slog.Handler {
	return exportHandler{Handler: h.Handler.WithGroup(name), minLevel: h.minLevel}
}

// redactNested menerapkan redactAttr ke atribut dan seluruh isi group-nya.
func redactNested(a slog.Attr) slog.Attr {
	a.Value = a.Value.Resolve()
	if a.Value.Kind() != slog.KindGroup {
		return redactAttr(nil, a)
	}
	members := a.Value.Group()
	clean := make([]any, len(members))
	for i, m := range members {
		clean[i] = redactNested(m)
	}
	return slog.Group(a.Key, clean...)
}
