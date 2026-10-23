// Package config membaca konfigurasi aplikasi dari environment variable
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	EnvDevelopment = "development"
	EnvProduction  = "production"
)

// SessionConfig adalah parameter session
type SessionConfig struct {
	IdleTimeout      time.Duration
	AbsoluteTimeout  time.Duration
	RotationInterval time.Duration
	GracePeriod      time.Duration
}

type Config struct {
	Env         string
	Addr        string
	DatabaseURL string
	SecretKey []byte // SecretKey adalah 32 byte hasil decode APP_SECRET_KEY
	Session   SessionConfig

	SeedAdminUsername string
	SeedAdminPassword string

	// UseStubs mengganti dengan authstub
	UseStubs bool
}

// Secure true jika cookie harus memakai atribut Secure
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

	if cfg.Session.GracePeriod >= cfg.Session.RotationInterval {
		errs = append(errs, errors.New("SESSION_GRACE_PERIOD: harus lebih kecil dari SESSION_ROTATION_INTERVAL"))
	}

	if err := errors.Join(errs...); err != nil {
		return Config{}, fmt.Errorf("konfigurasi tidak valid: %w", err)
	}
	return cfg, nil
}
