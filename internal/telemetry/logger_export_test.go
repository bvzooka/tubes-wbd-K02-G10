package telemetry

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// mencatat record yang sampai ke exporter, beserta atribut dari With
type captureHandler struct {
	mu      *sync.Mutex
	records *[]string
	attrs   []slog.Attr
}

func newCapture() captureHandler {
	return captureHandler{mu: &sync.Mutex{}, records: &[]string{}}
}

func (h captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h captureHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Level.String() + " " + r.Message)
	write := func(a slog.Attr) bool { b.WriteString(" " + a.String()); return true }
	for _, a := range h.attrs {
		write(a)
	}
	r.Attrs(write)
	h.mu.Lock()
	defer h.mu.Unlock()
	*h.records = append(*h.records, b.String())
	return nil
}

func (h captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return h
}

func (h captureHandler) WithGroup(string) slog.Handler { return h }

func (h captureHandler) all() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(*h.records, "\n")
}

func TestLogger_ExporterGetsRedactedCopy(t *testing.T) {
	export := newCapture()
	logger := NewLogger(io.Discard, "info", export).With("db_password", "Sup3rSecret!")
	ctx := WithCorrelationID(context.Background(), "req-12345678")

	logger.InfoContext(ctx, "auth.login",
		"password", "Sup3rSecret!",
		slog.Group("conn", "token", "Sup3rSecret!", "host", "targetdb"),
		"query", "ALTER ROLE a PASSWORD 'Sup3rSecret!'")

	got := export.all()
	if strings.Contains(got, "Sup3rSecret!") {
		t.Fatalf("rahasia bocor ke exporter: %s", got)
	}
	for _, want := range []string{"auth.login", "correlation_id=req-12345678", "host=targetdb", "[REDACTED]"} {
		if !strings.Contains(got, want) {
			t.Errorf("exporter tidak menerima %q: %s", want, got)
		}
	}
}

func TestLogger_ExporterFollowsLevel(t *testing.T) {
	export := newCapture()
	logger := NewLogger(io.Discard, "warn", export)

	logger.Info("tidak dikirim")
	logger.Warn("dikirim")

	if got := export.all(); got != "WARN dikirim" {
		t.Fatalf("exporter harus mengikuti LOG_LEVEL, dapat: %q", got)
	}
}

func TestLogger_NilExporterIgnored(t *testing.T) {
	var buf strings.Builder
	NewLogger(&buf, "info", nil).Info("tetap ke stdout")
	if !strings.Contains(buf.String(), "tetap ke stdout") {
		t.Fatalf("log stdout hilang: %q", buf.String())
	}
}

func TestSetupLogExport_NoneReturnsNoHandler(t *testing.T) {
	h, shutdown, err := SetupLogExport(context.Background(), "test", "none", "")
	if err != nil || h != nil {
		t.Fatalf("exporter none: handler=%v err=%v", h, err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
