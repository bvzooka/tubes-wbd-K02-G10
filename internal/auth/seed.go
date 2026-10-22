package auth

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"dbplatform/internal/contract"
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9_.-]{3,32}$`)

// TxBeginner adalah bagian *pgxpool.Pool yang dipakai SeedAdmin.
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// SeedAdmin membuat Platform Admin pertama jika belum ada Platform Admin aktif.
// Idempoten: mengembalikan created=false jika sudah ada admin aktif.
func SeedAdmin(ctx context.Context, db TxBeginner, hasher contract.PasswordHasher, username, password string) (created bool, err error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if !usernamePattern.MatchString(username) {
		return false, fmt.Errorf("seed admin: username %q tidak valid (harus ^[a-z0-9_.-]{3,32}$)", username)
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("seed admin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback setelah commit tidak berbahaya

	// Serialisasi seed dari beberapa proses yang start bersamaan.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('seed-admin', 0))`); err != nil {
		return false, fmt.Errorf("seed admin lock: %w", err)
	}
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE is_platform_admin AND is_active)`,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("seed admin check: %w", err)
	}
	if exists {
		return false, nil
	}

	if len(password) < MinPasswordBytes || len(password) > MaxPasswordBytes {
		return false, fmt.Errorf("seed admin: SEED_ADMIN_PASSWORD harus %d sampai %d byte", MinPasswordBytes, MaxPasswordBytes)
	}
	hash, err := hasher.Hash(password)
	if err != nil {
		return false, fmt.Errorf("seed admin hash: %w", err)
	}
	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, is_platform_admin, is_active)
		VALUES ($1, $2, TRUE, TRUE)
		ON CONFLICT (username) DO NOTHING
		RETURNING id`,
		username, hash,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("seed admin: username %q sudah dipakai user non-admin atau nonaktif; pilih SEED_ADMIN_USERNAME lain", username)
	}
	if err != nil {
		return false, fmt.Errorf("seed admin insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("seed admin commit: %w", err)
	}
	return true, nil
}
