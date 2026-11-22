package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"dbplatform/internal/config"
)

// titik masuk program: siapkan logger, jalankan run, kalau gagal catat errornya lalu keluar dengan kode 1
func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		logger.Error("server berhenti dengan error", "error", err)
		os.Exit(1)
	}
}

// urutan start: baca .env, load config, rakit modul (buildApp), jalankan server, lalu tunggu error atau sinyal berhenti
func run(logger *slog.Logger) error {
	if err := loadDotEnv(".env"); err != nil {
		return err
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	// ctx otomatis dibatalkan saat ada Ctrl+C atau SIGTERM, jadi bisa dipakai sebagai tanda "waktunya berhenti"
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

// Load .env, variabel yang udah ada ga ditimpa
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	// tidak ada file .env itu normal
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("baca %s: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		// baris kosong dan baris komentar (#) dilewati
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// pisahkan KEY=value di tanda sama dengan yang pertama
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		// variabel yang sudah diset di lingkungan menang atas isi .env
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
	return sc.Err()
}
