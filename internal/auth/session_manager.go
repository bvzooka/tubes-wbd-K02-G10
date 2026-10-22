package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"dbplatform/internal/clock"
	"dbplatform/internal/domain"
)

// Manager menggabungkan penyimpanan, policy, dan clock. Memenuhi contract.SessionAdmin.
type Manager struct {
	store  SessionStore
	policy Policy
	clock  clock.Clock
	logger *slog.Logger
}

func NewManager(store SessionStore, policy Policy, clk clock.Clock, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{store: store, policy: policy, clock: clk, logger: logger}
}

// Started adalah hasil Start: nilai cookie mentah dan waktu kedaluwarsa mutlaknya.
type Started struct {
	Token     string
	ExpiresAt time.Time
}

// Resolved adalah hasil Resolve untuk session yang valid.
type Resolved struct {
	SessionID int64
	Principal domain.Principal
	CSRFToken string
	ExpiresAt time.Time
	// NewToken terisi jika session baru saja dirotasi; cookie harus diganti dengan nilai ini.
	NewToken string
}

// Start membuat session baru untuk user (dipanggil setelah login sukses).
func (m *Manager) Start(ctx context.Context, userID int64) (Started, error) {
	raw, err := NewToken()
	if err != nil {
		return Started{}, fmt.Errorf("new session token: %w", err)
	}
	csrf, err := NewCSRFToken()
	if err != nil {
		return Started{}, fmt.Errorf("new csrf token: %w", err)
	}
	now := m.clock.Now()
	expires := now.Add(m.policy.AbsoluteTimeout)
	if _, err := m.store.Create(ctx, NewSession{
		UserID:            userID,
		TokenHash:         HashToken(raw),
		CSRFToken:         csrf,
		CreatedAt:         now,
		AbsoluteExpiresAt: expires,
	}); err != nil {
		return Started{}, err
	}
	return Started{Token: raw, ExpiresAt: expires}, nil
}

// Resolve memvalidasi nilai cookie. Session tidak valid menghasilkan domain.ErrUnauthenticated.
func (m *Manager) Resolve(ctx context.Context, raw string) (*Resolved, error) {
	if raw == "" {
		return nil, domain.ErrUnauthenticated
	}
	now := m.clock.Now()
	hash := HashToken(raw)
	s, err := m.store.FindByTokenHash(ctx, hash, now)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, domain.ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}

	d := Evaluate(now, s, m.policy)
	if !d.Valid {
		switch {
		case d.RevokeAllForUser:
			if err := m.store.RevokeAllForUser(ctx, s.UserID, now, d.RevokeReason); err != nil {
				m.logger.ErrorContext(ctx, "revoke user sessions failed", "user_id", s.UserID, "err", err)
			}
		case d.Revoke:
			if err := m.store.Revoke(ctx, s.ID, now, d.RevokeReason); err != nil {
				m.logger.ErrorContext(ctx, "revoke session failed", "session_id", s.ID, "err", err)
			}
		}
		return nil, domain.ErrUnauthenticated
	}

	res := &Resolved{
		SessionID: s.ID,
		Principal: domain.Principal{UserID: s.UserID, Username: s.Username, IsPlatformAdmin: s.IsPlatformAdmin},
		CSRFToken: s.CSRFToken,
		ExpiresAt: s.AbsoluteExpiresAt,
	}
	switch {
	case d.Rotate:
		newRaw, err := NewToken()
		if err != nil {
			return nil, fmt.Errorf("new session token: %w", err)
		}
		ok, err := m.store.Rotate(ctx, s.ID, hash, HashToken(newRaw), now, now.Add(m.policy.GracePeriod))
		if err != nil {
			return nil, err
		}
		// ok false: request paralel sudah merotasi lebih dulu; token ini masih berlaku dalam grace.
		if ok {
			res.NewToken = newRaw
		}
	case d.Touch:
		if err := m.store.Touch(ctx, s.ID, now); err != nil {
			m.logger.WarnContext(ctx, "touch session failed", "session_id", s.ID, "err", err)
		}
	}
	return res, nil
}

// End mencabut session milik nilai cookie (logout). Token yang tidak dikenal diabaikan.
func (m *Manager) End(ctx context.Context, raw string) error {
	return m.revokeToken(ctx, raw, ReasonLogout)
}

func (m *Manager) revokeToken(ctx context.Context, raw, reason string) error {
	if raw == "" {
		return nil
	}
	now := m.clock.Now()
	s, err := m.store.FindByTokenHash(ctx, HashToken(raw), now)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return m.store.Revoke(ctx, s.ID, now, reason)
}

// RevokeAllForUser memenuhi contract.SessionAdmin.
func (m *Manager) RevokeAllForUser(ctx context.Context, userID int64) error {
	return m.store.RevokeAllForUser(ctx, userID, m.clock.Now(), ReasonAdmin)
}

// MarkRotationNeeded memenuhi contract.SessionAdmin.
func (m *Manager) MarkRotationNeeded(ctx context.Context, userIDs ...int64) error {
	return m.store.MarkRotationNeeded(ctx, userIDs)
}

// RunCleanup menghapus session kedaluwarsa setiap interval sampai ctx selesai.
func (m *Manager) RunCleanup(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		n, err := m.store.DeleteExpired(ctx, m.clock.Now())
		if err != nil {
			m.logger.ErrorContext(ctx, "session cleanup failed", "err", err)
		} else if n > 0 {
			m.logger.InfoContext(ctx, "session cleanup", "deleted", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
