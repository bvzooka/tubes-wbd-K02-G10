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

// titik masuk program: prepare logger, jalankan run, kalo gagal catat errornya lalu keluar dengan kode 1
func main() {
	if err := run(); err != nil {
		slog.Error("server berhenti dengan error", "error", err)
		os.Exit(1)
	}
}

// urutan start: baca .env, load config, prepare log & tracing, rakit modul (buildApp), jalankan server, lalu tunggu error atau sinyal berhenti
func run() error {
	if err := config.LoadDotEnv(".env"); err != nil {
		return err
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	// log selalu ke stdout, dengan OTEL_LOGS_EXPORTER=otlp salinannya jga dikirim ke Collector
	// di-defer paling awal biar berjalan paling akhir dan log terakhir ikut terkirim
	logExport, shutdownLogs, err := telemetry.SetupLogExport(context.Background(), "dbplatform-api", cfg.LogExporter, cfg.OTLPEndpoint)
	if err != nil {
		return err
	}
	defer telemetry.ShutdownWithin(5*time.Second, shutdownLogs)
	logger := telemetry.NewLogger(os.Stdout, cfg.LogLevel, logExport)
	slog.SetDefault(logger)

	// ctx otomatis dibatalkan kalo ada Ctrl+C atau SIGTERM, jadi bisa dipakai sebagai tanda "waktunya berhenti"
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// tracing di-flush terakhir saat berhenti, kegagalannya ga perlu menggagalkan shutdown
	shutdownTracing, err := telemetry.SetupTracing(ctx, "dbplatform-api", cfg.TraceExporter, cfg.OTLPEndpoint)
	if err != nil {
		return err
	}
	defer func() { _ = shutdownTracing(context.WithoutCancel(ctx)) }()

	// buildApp merakit semua modul, cleanup menutup koneksi database saat server berhenti
	handler, cleanup, err := buildApp(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer cleanup()

	// batas waktu biar client yg lambat atau menggantung tidak menahan server terus-menerus
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("server berjalan", "addr", cfg.Addr, "env", cfg.Env, "stubs", cfg.UseStubs)
		errCh <- srv.ListenAndServe()
	}()

	// tunggu yg duluan terjadi: server berhenti sendiri, atau ada sinyal berhenti
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		logger.Info("menutup server")
		// kasih waktu 10 detik biar request yang lagi jalan sempat selesai sebelum server dimatikan
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
