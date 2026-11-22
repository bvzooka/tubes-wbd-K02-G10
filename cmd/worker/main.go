// Command worker mengambil job migration dari queue dan menjalankannya di database target.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

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
	logger := telemetry.NewLogger(os.Stdout, cfg.LogLevel).With("worker_id", cfg.Worker.ID)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := telemetry.SetupTracing(ctx, "dbplatform-worker", cfg.TraceExporter, cfg.OTLPEndpoint)
	if err != nil {
		return err
	}
	defer func() { _ = shutdownTracing(context.WithoutCancel(ctx)) }() // span terakhir best-effort

	worker, cleanup, err := buildWorker(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer cleanup()

	logger.Info("worker berjalan", "event", "worker.start", "concurrency", cfg.Worker.Concurrency)
	worker.Run(ctx) // kembali setelah SIGTERM dan job berjalan selesai (atau grace habis)
	logger.Info("worker berhenti", "event", "worker.stop")
	return nil
}
