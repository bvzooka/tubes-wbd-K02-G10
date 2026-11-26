package config

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func validKey() string { return base64.StdEncoding.EncodeToString(make([]byte, 32)) }

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{"APP_SECRET_KEY": validKey()}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Env != EnvDevelopment || cfg.Addr != ":8080" || cfg.Secure() {
		t.Errorf("default tidak sesuai: %+v", cfg)
	}
	if cfg.Session.IdleTimeout != 30*time.Minute || cfg.Session.AbsoluteTimeout != 12*time.Hour ||
		cfg.Session.RotationInterval != 15*time.Minute || cfg.Session.GracePeriod != 30*time.Second {
		t.Errorf("default session tidak sesuai: %+v", cfg.Session)
	}
	if cfg.Worker.Concurrency != 4 || cfg.Worker.Lease != 30*time.Second || cfg.Worker.Heartbeat != 10*time.Second ||
		cfg.Worker.MaxAttempts != 3 || cfg.Worker.RetryBase != 5*time.Second || cfg.Worker.ID == "" {
		t.Errorf("default worker tidak sesuai: %+v", cfg.Worker)
	}
	if len(cfg.SecretKey) != 32 || cfg.SeedAdminUsername != "admin" || cfg.UseStubs {
		t.Errorf("default lain tidak sesuai: %+v", cfg)
	}
}

func TestLoad_Production(t *testing.T) {
	cfg, err := Load(env(map[string]string{"APP_SECRET_KEY": validKey(), "APP_ENV": "production"}))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Secure() || cfg.UseStubs {
		t.Errorf("production tidak terbaca: %+v", cfg)
	}
}

func TestLoad_StubsBolehDiDevelopment(t *testing.T) {
	cfg, err := Load(env(map[string]string{"APP_SECRET_KEY": validKey(), "APP_STUBS": "true"}))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.UseStubs {
		t.Errorf("APP_STUBS tidak terbaca: %+v", cfg)
	}
}

func TestLoad_Invalid(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"secret kosong", map[string]string{}, "APP_SECRET_KEY"},
		{"secret bukan base64", map[string]string{"APP_SECRET_KEY": "!!!"}, "APP_SECRET_KEY"},
		{"secret terlalu pendek", map[string]string{"APP_SECRET_KEY": base64.StdEncoding.EncodeToString([]byte("pendek"))}, "32 byte"},
		{"env salah", map[string]string{"APP_SECRET_KEY": validKey(), "APP_ENV": "staging"}, "APP_ENV"},
		{"durasi salah", map[string]string{"APP_SECRET_KEY": validKey(), "SESSION_IDLE_TIMEOUT": "abc"}, "SESSION_IDLE_TIMEOUT"},
		{"durasi nol", map[string]string{"APP_SECRET_KEY": validKey(), "SESSION_IDLE_TIMEOUT": "0s"}, "SESSION_IDLE_TIMEOUT"},
		{"grace >= rotasi", map[string]string{"APP_SECRET_KEY": validKey(), "SESSION_GRACE_PERIOD": "20m"}, "SESSION_GRACE_PERIOD"},
		{"stubs di production", map[string]string{"APP_SECRET_KEY": validKey(), "APP_ENV": "production", "APP_STUBS": "true"}, "APP_STUBS"},
		{"heartbeat terlalu lama", map[string]string{"APP_SECRET_KEY": validKey(), "JOB_HEARTBEAT": "20s"}, "JOB_HEARTBEAT"},
		{"concurrency nol", map[string]string{"APP_SECRET_KEY": validKey(), "WORKER_CONCURRENCY": "0"}, "WORKER_CONCURRENCY"},
		{"exporter salah", map[string]string{"APP_SECRET_KEY": validKey(), "OTEL_TRACES_EXPORTER": "zipkin"}, "OTEL_TRACES_EXPORTER"},
		{"log exporter salah", map[string]string{"APP_SECRET_KEY": validKey(), "OTEL_LOGS_EXPORTER": "console"}, "OTEL_LOGS_EXPORTER"},
		{"stubs salah", map[string]string{"APP_SECRET_KEY": validKey(), "APP_STUBS": "mungkin"}, "APP_STUBS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(env(tt.env))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, harus memuat %q", err, tt.want)
			}
		})
	}
}

func TestLoad_ErrorTidakMembocorkanSecret(t *testing.T) {
	_, err := Load(env(map[string]string{"APP_SECRET_KEY": "rahasia-pendek-123"}))
	if err == nil || strings.Contains(err.Error(), "rahasia-pendek-123") {
		t.Fatalf("error harus ada dan tidak memuat nilai secret: %v", err)
	}
}
