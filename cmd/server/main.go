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

// titik masuk program: siapkan logger, jalankan run, kalau gagal catat errornya lalu keluar dengan kode 1
func main() {
	if err := run(); err != nil {
		slog.Error("server berhenti dengan error", "error", err)
		os.Exit(1)
	}
}

// urutan start: baca .env, load config, siapkan log dan tracing, rakit modul (buildApp), jalankan server, lalu tunggu error atau sinyal berhenti
func run() error {
	if err := config.LoadDotEnv(".env"); err != nil {
		return err
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	logger := telemetry.NewLogger(os.Stdout, cfg.LogLevel)
	slog.SetDefault(logger)

	// ctx otomatis dibatalkan saat ada Ctrl+C atau SIGTERM, jadi bisa dipakai sebagai tanda "waktunya berhenti"
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// tracing di-flush terakhir saat berhenti, kegagalannya tidak perlu menggagalkan shutdown
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

	// batas waktu supaya client yang lambat atau menggantung tidak menahan server terus-menerus
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// ListenAndServe memblokir, jadi dijalankan di goroutine (jalan paralel) supaya main tetap bisa menunggu sinyal berhenti
	errCh := make(chan error, 1)
	go func() {
		logger.Info("server berjalan", "addr", cfg.Addr, "env", cfg.Env, "stubs", cfg.UseStubs)
		errCh <- srv.ListenAndServe()
	}()

	// tunggu yang duluan terjadi: server berhenti sendiri, atau ada sinyal berhenti
	select {
	case err := <-errCh:
		// ErrServerClosed muncul setelah Shutdown dipanggil, itu bukan error sungguhan
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		logger.Info("menutup server")
		// kasih waktu 10 detik supaya request yang sedang jalan sempat selesai sebelum server dimatikan
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
