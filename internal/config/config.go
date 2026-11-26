package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// nilai APP_ENV yang diterima, production mengaktifkan cookie Secure dan melarang APP_STUBS
const (
	EnvDevelopment = "development"
	EnvProduction  = "production"
)

// batas waktu session, diisi dari env SESSION_*
type SessionConfig struct {
	IdleTimeout      time.Duration
	AbsoluteTimeout  time.Duration
	RotationInterval time.Duration
	GracePeriod      time.Duration
}

// WorkerConfig adalah pengaturan worker: berapa job paralel, seberapa sering mengecek antrean, dan batas lease, heartbeat, serta percobaan ulang
type WorkerConfig struct {
	ID           string
	Concurrency  int
	PollInterval time.Duration
	Lease        time.Duration
	Heartbeat    time.Duration
	MaxAttempts  int
	RetryBase    time.Duration
	MetricsAddr  string // alamat HTTP /metrics milik worker
}

// semua pengaturan aplikasi yang dibaca dari environment sekali saat start
type Config struct {
	Env         string
	Addr        string
	DatabaseURL string
	SecretKey   []byte // SecretKey adalah 32 byte hasil decode APP_SECRET_KEY
	Session     SessionConfig

	SeedAdminUsername string
	SeedAdminPassword string

	UseStubs bool

	LogLevel      string
	TraceExporter string
	LogExporter   string // none: log cuma ke stdout; otlp: salinannya jga dikirim ke Collector
	OTLPEndpoint  string
	Worker        WorkerConfig
	WebDistDir    string
}

// cookie ditandai Secure hanya di production karena butuh HTTPS
func (c Config) Secure() bool { return c.Env == EnvProduction }

// Load membaca konfigurasi lewat getenv
func Load(getenv func(string) string) (Config, error) {
	var errs []error
	str := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}
	num := func(key, def string) int {
		n, err := strconv.Atoi(str(key, def))
		if err != nil || n <= 0 {
			errs = append(errs, fmt.Errorf("%s: harus bilangan bulat positif", key))
		}
		return n
	}
	dur := func(key, def string) time.Duration {
		d, err := time.ParseDuration(str(key, def))
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("%s: durasi tidak valid (contoh 30m)", key))
		}
		return d
	}

	cfg := Config{
		Env:         str("APP_ENV", EnvDevelopment),
		Addr:        str("APP_ADDR", ":8080"),
		DatabaseURL: str("APP_DATABASE_URL", "postgres://postgres:postgres@localhost:5432/dbplatform?sslmode=disable"),
		Session: SessionConfig{
			IdleTimeout:      dur("SESSION_IDLE_TIMEOUT", "30m"),
			AbsoluteTimeout:  dur("SESSION_ABSOLUTE_TIMEOUT", "12h"),
			RotationInterval: dur("SESSION_ROTATION_INTERVAL", "15m"),
			GracePeriod:      dur("SESSION_GRACE_PERIOD", "30s"),
		},
		SeedAdminUsername: str("SEED_ADMIN_USERNAME", "admin"),
		SeedAdminPassword: getenv("SEED_ADMIN_PASSWORD"),
		LogLevel:          str("LOG_LEVEL", "info"),
		TraceExporter:     str("OTEL_TRACES_EXPORTER", "console"),
		LogExporter:       str("OTEL_LOGS_EXPORTER", "none"),
		OTLPEndpoint:      str("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		WebDistDir:        str("WEB_DIST_DIR", ""),
		Worker: WorkerConfig{
			ID:           str("WORKER_ID", defaultWorkerID()),
			MetricsAddr:  str("METRICS_ADDR", ":9091"),
			Concurrency:  num("WORKER_CONCURRENCY", "4"),
			PollInterval: dur("WORKER_POLL_INTERVAL", "1s"),
			Lease:        dur("JOB_LEASE", "30s"),
			Heartbeat:    dur("JOB_HEARTBEAT", "10s"),
			MaxAttempts:  num("JOB_MAX_ATTEMPTS", "3"),
			RetryBase:    dur("JOB_RETRY_BASE", "5s"),
		},
	}

	if cfg.Env != EnvDevelopment && cfg.Env != EnvProduction {
		errs = append(errs, fmt.Errorf("APP_ENV: harus %q atau %q", EnvDevelopment, EnvProduction))
	}

	if raw := strings.TrimSpace(getenv("APP_SECRET_KEY")); raw == "" {
		errs = append(errs, errors.New("APP_SECRET_KEY: wajib diisi (openssl rand -base64 32)"))
	} else if key, err := base64.StdEncoding.DecodeString(raw); err != nil || len(key) != 32 {
		errs = append(errs, errors.New("APP_SECRET_KEY: harus base64 dari tepat 32 byte"))
	} else {
		cfg.SecretKey = key
	}

	if v := strings.TrimSpace(getenv("APP_STUBS")); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, errors.New("APP_STUBS: harus true atau false"))
		}
		cfg.UseStubs = b
	}

	// APP_STUBS mematikan login dan otorisasi, jadi dilarang di production
	if cfg.UseStubs && cfg.Env == EnvProduction {
		errs = append(errs, errors.New("APP_STUBS: tidak boleh true saat APP_ENV=production"))
	}

	if cfg.Session.GracePeriod >= cfg.Session.RotationInterval {
		errs = append(errs, errors.New("SESSION_GRACE_PERIOD: harus lebih kecil dari SESSION_ROTATION_INTERVAL"))
	}

	// heartbeat harus sempat gagal dua kali sebelum lease habis
	if cfg.Worker.Heartbeat*3 > cfg.Worker.Lease {
		errs = append(errs, errors.New("JOB_HEARTBEAT: maksimal sepertiga JOB_LEASE"))
	}
	switch cfg.TraceExporter {
	case "console", "otlp", "none":
	default:
		errs = append(errs, errors.New("OTEL_TRACES_EXPORTER: harus console, otlp, atau none"))
	}
	switch cfg.LogExporter {
	case "otlp", "none":
	default:
		errs = append(errs, errors.New("OTEL_LOGS_EXPORTER: harus otlp atau none"))
	}

	// Semua error config dilaporin sekaligus
	if err := errors.Join(errs...); err != nil {
		return Config{}, fmt.Errorf("konfigurasi tidak valid: %w", err)
	}
	return cfg, nil
}

func defaultWorkerID() string {
	host, _ := os.Hostname()
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}
