// mengambil job migration dari queue dan menjalankannya di db target
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"dbplatform/internal/config"
	"dbplatform/internal/telemetry"
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker berhenti dengan error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	if err := config.LoadDotEnv(".env"); err != nil {
		return err
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	logExport, shutdownLogs, err := telemetry.SetupLogExport(context.Background(), "dbplatform-worker", cfg.LogExporter, cfg.OTLPEndpoint)
	if err != nil {
		return err
	}
	defer telemetry.ShutdownWithin(5*time.Second, shutdownLogs) // log terakhir best-effort
	logger := telemetry.NewLogger(os.Stdout, cfg.LogLevel, logExport).With("worker_id", cfg.Worker.ID)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := telemetry.SetupTracing(ctx, "dbplatform-worker", cfg.TraceExporter, cfg.OTLPEndpoint)
	if err != nil {
		return err
	}
	defer func() { _ = shutdownTracing(context.WithoutCancel(ctx)) }() // span terakhir best-effort

	app, cleanup, err := buildWorker(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer cleanup()

	// server kecil khusus /metrics
	metrics := &http.Server{Addr: cfg.Worker.MetricsAddr, Handler: app.metrics, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := metrics.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server metrics berhenti", "event", "worker.metrics", "error", err)
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = metrics.Shutdown(shutdownCtx) // udah mau berhenti, error-nya nggak penting
	}()

	logger.Info("worker berjalan", "event", "worker.start", "concurrency", cfg.Worker.Concurrency)
	app.worker.Run(ctx) // kembali setelah SIGTERM & job berjalan selesai (atau grace habis)
	logger.Info("worker berhenti", "event", "worker.stop")
	return nil
}
