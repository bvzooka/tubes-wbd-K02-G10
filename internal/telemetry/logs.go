package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// menyiapkan pengiriman salinan log ke OpenTelemetry Collector lewat OTLP/HTTP
// Handler yang dikembalikan diteruskan ke NewLogger dan bernilai nil bila exporter none
// fungsi shutdown wajib dipanggil saat proses berhenti agar log terakhir terkirim
func SetupLogExport(ctx context.Context, service, exporter, endpoint string) (slog.Handler, func(context.Context) error, error) {
	if exporter != "otlp" {
		return nil, func(context.Context) error { return nil }, nil
	}

	opts := []otlploghttp.Option{otlploghttp.WithInsecure()}
	if endpoint != "" {
		opts = append(opts, otlploghttp.WithEndpointURL(signalURL(endpoint, "logs")))
	}
	exp, err := otlploghttp.New(ctx, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("log exporter otlp: %w", err)
	}

	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)),
		sdklog.WithResource(serviceResource(service)),
	)
	return otelslog.NewHandler(service, otelslog.WithLoggerProvider(lp)), lp.Shutdown, nil
}

// memanggil shutdown dengan batas waktu supaya Collector yg ga terjangkau ga menahan proses berhenti. errornya diabaikan karena prosesnya memang sedang berhenti.
func ShutdownWithin(limit time.Duration, shutdown func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	_ = shutdown(ctx)
}
