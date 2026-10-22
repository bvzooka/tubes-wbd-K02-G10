// Package auth menangani login, password, session, CSRF, dan middleware autentikasi.
package auth

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/clock"
	"dbplatform/internal/contract"
)

// Config adalah konfigurasi modul auth (diisi dari env oleh wire.go).
type Config struct {
	Policy Policy
	// Secure mengaktifkan atribut Secure pada cookie (APP_ENV=production).
	Secure     bool
	BcryptCost int
}

// Deps adalah dependency modul auth dari modul lain.
type Deps struct {
	Pool       *pgxpool.Pool
	Clock      clock.Clock
	Render     Renderer
	HandleErr  contract.ErrorHandler
	Authorizer contract.Authorizer
	Logger     *slog.Logger
}

// Module berisi semua komponen auth yang sudah dirakit.
type Module struct {
	Hasher     *Hasher     // contract.PasswordHasher
	Sessions   *Manager    // contract.SessionAdmin
	Middleware *Middleware // contract.AuthMiddleware
	Handler    *Handler
}

// New merakit modul auth.
func New(cfg Config, d Deps) *Module {
	if cfg.Policy.TouchInterval == 0 {
		cfg.Policy.TouchInterval = DefaultPolicy().TouchInterval
	}
	hasher := NewHasher(cfg.BcryptCost)
	sessions := NewManager(NewRepo(d.Pool), cfg.Policy, d.Clock, d.Logger)
	mw := NewMiddleware(sessions, d.Authorizer, d.HandleErr, cfg.Secure)
	h := NewHandler(HandlerDeps{
		Sessions:  sessions,
		Users:     NewUserRepo(d.Pool),
		Verifier:  hasher,
		Render:    d.Render,
		HandleErr: d.HandleErr,
		MW:        mw,
		Clock:     d.Clock,
		Secure:    cfg.Secure,
		Logger:    d.Logger,
	})
	return &Module{Hasher: hasher, Sessions: sessions, Middleware: mw, Handler: h}
}

var (
	_ contract.PasswordHasher = (*Hasher)(nil)
	_ contract.SessionAdmin   = (*Manager)(nil)
)
