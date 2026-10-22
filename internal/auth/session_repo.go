package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"dbplatform/internal/domain"
)

// DB adalah bagian dari *pgxpool.Pool yang dipakai repo di package ini.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// NewSession adalah data untuk membuat baris session baru.
type NewSession struct {
	UserID            int64
	TokenHash         []byte
	CSRFToken         string
	CreatedAt         time.Time
	AbsoluteExpiresAt time.Time
}

// SessionStore adalah penyimpanan session. Repo adalah implementasi PostgreSQL-nya.
type SessionStore interface {
	Create(ctx context.Context, s NewSession) (int64, error)
	// FindByTokenHash mencari lewat token_hash, lalu lewat prev_token_hash yang masih dalam grace.
	// Mengembalikan domain.ErrNotFound jika tidak ada.
	FindByTokenHash(ctx context.Context, hash []byte, now time.Time) (Session, error)
	Touch(ctx context.Context, id int64, now time.Time) error
	// Rotate mengganti token_hash jika token_hash saat ini masih oldHash. false jika sudah dirotasi request lain.
	Rotate(ctx context.Context, id int64, oldHash, newHash []byte, now, prevValidUntil time.Time) (bool, error)
	Revoke(ctx context.Context, id int64, now time.Time, reason string) error
	RevokeAllForUser(ctx context.Context, userID int64, now time.Time, reason string) error
	MarkRotationNeeded(ctx context.Context, userIDs []int64) error
	DeleteExpired(ctx context.Context, now time.Time) (int64, error)
}

// Repo mengakses tabel sessions (pemilik tulis: Kurt).
type Repo struct {
	db DB
}

func NewRepo(db DB) *Repo { return &Repo{db: db} }

func (r *Repo) Create(ctx context.Context, s NewSession) (int64, error) {
	var id int64
	err := r.db.QueryRow(ctx, `
		INSERT INTO sessions (user_id, token_hash, csrf_token, created_at, absolute_expires_at, last_seen_at, rotated_at)
		VALUES ($1, $2, $3, $4, $5, $4, $4)
		RETURNING id`,
		s.UserID, s.TokenHash, s.CSRFToken, s.CreatedAt, s.AbsoluteExpiresAt,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create session: %w", err)
	}
	return id, nil
}

func (r *Repo) FindByTokenHash(ctx context.Context, hash []byte, now time.Time) (Session, error) {
	var s Session
	err := r.db.QueryRow(ctx, `
		SELECT s.id, s.user_id, s.csrf_token, s.rotate_required, s.created_at, s.absolute_expires_at,
		       s.last_seen_at, s.rotated_at, s.revoked_at, s.prev_valid_until, s.token_hash <> $1,
		       u.username, u.is_platform_admin, u.is_active
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1
		   OR (s.prev_token_hash = $1 AND s.prev_valid_until > $2)
		ORDER BY (s.token_hash = $1) DESC
		LIMIT 1`,
		hash, now,
	).Scan(&s.ID, &s.UserID, &s.CSRFToken, &s.RotateRequired, &s.CreatedAt, &s.AbsoluteExpiresAt,
		&s.LastSeenAt, &s.RotatedAt, &s.RevokedAt, &s.PrevValidUntil, &s.MatchedPrevious,
		&s.Username, &s.IsPlatformAdmin, &s.UserActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, domain.ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("find session: %w", err)
	}
	return s, nil
}

func (r *Repo) Touch(ctx context.Context, id int64, now time.Time) error {
	_, err := r.db.Exec(ctx,
		`UPDATE sessions SET last_seen_at = $2 WHERE id = $1 AND revoked_at IS NULL AND last_seen_at < $2`,
		id, now)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

func (r *Repo) Rotate(ctx context.Context, id int64, oldHash, newHash []byte, now, prevValidUntil time.Time) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE sessions
		SET prev_token_hash = token_hash,
		    prev_valid_until = $4,
		    token_hash = $3,
		    rotated_at = $5,
		    last_seen_at = $5,
		    rotate_required = FALSE
		WHERE id = $1 AND token_hash = $2 AND revoked_at IS NULL`,
		id, oldHash, newHash, prevValidUntil, now)
	if err != nil {
		return false, fmt.Errorf("rotate session: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *Repo) Revoke(ctx context.Context, id int64, now time.Time, reason string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE sessions SET revoked_at = $2, revoked_reason = $3 WHERE id = $1 AND revoked_at IS NULL`,
		id, now, reason)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

func (r *Repo) RevokeAllForUser(ctx context.Context, userID int64, now time.Time, reason string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE sessions SET revoked_at = $2, revoked_reason = $3 WHERE user_id = $1 AND revoked_at IS NULL`,
		userID, now, reason)
	if err != nil {
		return fmt.Errorf("revoke user sessions: %w", err)
	}
	return nil
}

func (r *Repo) MarkRotationNeeded(ctx context.Context, userIDs []int64) error {
	if len(userIDs) == 0 {
		return nil
	}
	_, err := r.db.Exec(ctx,
		`UPDATE sessions SET rotate_required = TRUE WHERE user_id = ANY($1) AND revoked_at IS NULL`,
		userIDs)
	if err != nil {
		return fmt.Errorf("mark rotation needed: %w", err)
	}
	return nil
}

// revokedRetention adalah lama session yang dicabut disimpan sebelum dihapus.
const revokedRetention = 7 * 24 * time.Hour

func (r *Repo) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	tag, err := r.db.Exec(ctx,
		`DELETE FROM sessions WHERE absolute_expires_at < $1 OR revoked_at < $2`,
		now, now.Add(-revokedRetention))
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}
