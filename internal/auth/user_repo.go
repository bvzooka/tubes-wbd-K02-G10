package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"dbplatform/internal/domain"
)

// UserCredential adalah data user yang dibutuhkan saat login.
type UserCredential struct {
	ID              int64
	Username        string
	PasswordHash    string
	IsPlatformAdmin bool
	IsActive        bool
}

// UserFinder mencari user berdasarkan username yang sudah dinormalisasi.
type UserFinder interface {
	FindByUsername(ctx context.Context, username string) (UserCredential, error)
}

// UserRepo membaca tabel users (hanya baca; pemilik tulis adalah modul admin).
type UserRepo struct {
	db DB
}

func NewUserRepo(db DB) *UserRepo { return &UserRepo{db: db} }

func (r *UserRepo) FindByUsername(ctx context.Context, username string) (UserCredential, error) {
	var u UserCredential
	err := r.db.QueryRow(ctx,
		`SELECT id, username, password_hash, is_platform_admin, is_active FROM users WHERE username = $1`,
		username,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.IsPlatformAdmin, &u.IsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return UserCredential{}, domain.ErrNotFound
	}
	if err != nil {
		return UserCredential{}, fmt.Errorf("find user: %w", err)
	}
	return u, nil
}
