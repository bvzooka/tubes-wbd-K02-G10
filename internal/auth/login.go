package auth

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"dbplatform/internal/clock"
	"dbplatform/internal/domain"
)

const (
	loginMaxFailures   = 5
	loginFailureWindow = 15 * time.Minute

	msgInvalidCredentials = "Username atau password salah."
	msgFormExpired        = "Formulir login kedaluwarsa. Silakan coba lagi."
	msgTooManyAttempts    = "Terlalu banyak percobaan login gagal. Coba lagi dalam beberapa menit."
)

var (
	ErrInvalidCredentials = &domain.MessageError{Err: domain.ErrUnauthenticated, Message: msgInvalidCredentials}
	ErrTooManyAttempts    = &domain.MessageError{Err: domain.ErrRateLimited, Message: msgTooManyAttempts}
	ErrLoginCSRF          = &domain.MessageError{Err: domain.ErrForbidden, Message: msgFormExpired}
)

// credentialVerifier adalah bagian Hasher yang dipakai saat login.
type credentialVerifier interface {
	Verify(hash, plain string) bool
	VerifyDummy(plain string) bool
}

type LoginInput struct {
	Username      string
	Password      string
	IP            string
	PreviousToken string // cookie session lama, dicabut saat login ulang
}

type LoginResult struct {
	Session   Started
	Principal domain.Principal
}

// Authenticator memeriksa kredensial dan membuat session. Dipakai bersama oleh login
// HTML dan login API sehingga rate limit dan pesan error-nya sama.
type Authenticator struct {
	sessions *Manager
	users    UserFinder
	verifier credentialVerifier
	limiter  *loginLimiter
	logger   *slog.Logger
}

func NewAuthenticator(sessions *Manager, users UserFinder, verifier credentialVerifier, clk clock.Clock, logger *slog.Logger) *Authenticator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Authenticator{
		sessions: sessions,
		users:    users,
		verifier: verifier,
		limiter:  newLoginLimiter(clk, loginMaxFailures, loginFailureWindow),
		logger:   logger,
	}
}

func NormalizeUsername(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func (a *Authenticator) Login(ctx context.Context, in LoginInput) (LoginResult, error) {
	username := NormalizeUsername(in.Username)
	if a.limiter.Blocked(username, in.IP) {
		a.logger.WarnContext(ctx, "login rate limited", "event", "auth.login", "status", "rate_limited", "actor", username, "ip", in.IP)
		return LoginResult{}, ErrTooManyAttempts
	}

	user, err := a.users.FindByUsername(ctx, username)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return LoginResult{}, err
	}
	// Selalu jalankan bcrypt agar waktu respons tidak membocorkan username yang ada.
	var ok bool
	if err == nil && user.IsActive {
		ok = a.verifier.Verify(user.PasswordHash, in.Password)
	} else {
		ok = a.verifier.VerifyDummy(in.Password)
	}
	if !ok {
		a.limiter.Fail(username, in.IP)
		a.logger.InfoContext(ctx, "login failed", "event", "auth.login", "status", "failed", "actor", username, "ip", in.IP)
		return LoginResult{}, ErrInvalidCredentials
	}
	a.limiter.Reset(username, in.IP)

	if in.PreviousToken != "" {
		if err := a.sessions.revokeToken(ctx, in.PreviousToken, ReasonRelogin); err != nil {
			return LoginResult{}, err
		}
	}
	started, err := a.sessions.Start(ctx, user.ID)
	if err != nil {
		return LoginResult{}, err
	}
	a.logger.InfoContext(ctx, "login succeeded", "event", "auth.login", "status", "success", "actor", user.Username)
	return LoginResult{
		Session:   started,
		Principal: domain.Principal{UserID: user.ID, Username: user.Username, IsPlatformAdmin: user.IsPlatformAdmin},
	}, nil
}
