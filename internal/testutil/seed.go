package testutil

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/domain"
)

// SeedUser menyisipkan user langsung ke tabel users (tanpa melewati service)
// password_hash diisi placeholder. Pakai SeedUserWithHash bila test perlu login sungguhan
func SeedUser(t *testing.T, pool *pgxpool.Pool, username string, admin bool) domain.User {
	t.Helper()
	return SeedUserWithHash(t, pool, username, "stub:x", admin)
}

func SeedUserWithHash(t *testing.T, pool *pgxpool.Pool, username, passwordHash string, admin bool) domain.User {
	t.Helper()
	u := domain.User{Username: username, IsPlatformAdmin: admin, IsActive: true}
	err := pool.QueryRow(context.Background(), `
		INSERT INTO users (username, password_hash, is_platform_admin) VALUES ($1, $2, $3)
		RETURNING id, created_at`, username, passwordHash, admin).Scan(&u.ID, &u.CreatedAt)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u
}

func SetUserActive(t *testing.T, pool *pgxpool.Pool, userID int64, active bool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE users SET is_active = $2 WHERE id = $1`, userID, active); err != nil {
		t.Fatalf("set user active: %v", err)
	}
}

// SeedGroup membuat group dan memasukkan anggota (id user) ke dalamnya
func SeedGroup(t *testing.T, pool *pgxpool.Pool, name string, members ...int64) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO user_groups (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	for _, m := range members {
		if _, err := pool.Exec(ctx, `INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)`, id, m); err != nil {
			t.Fatalf("seed member: %v", err)
		}
	}
	return id
}

// SeedConnection menyisipkan baris connections palsu (password terenkripsi diisi byte kosong)
func SeedConnection(t *testing.T, pool *pgxpool.Pool, name string, active bool) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `
		INSERT INTO connections (name, dbms, host, port, database_name, db_username, db_password_enc, is_active)
		VALUES ($1, 'postgres', 'localhost', 5433, 'sampledb', 'postgres', '\x00', $2) RETURNING id`,
		name, active).Scan(&id)
	if err != nil {
		t.Fatalf("seed connection: %v", err)
	}
	return id
}

// Grant memberi (atau mengubah) akses group ke koneksi
func Grant(t *testing.T, pool *pgxpool.Pool, groupID, connID int64, level domain.AccessLevel) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO group_database_access (group_id, connection_id, access_level) VALUES ($1, $2, $3)
		ON CONFLICT (group_id, connection_id) DO UPDATE SET access_level = EXCLUDED.access_level`,
		groupID, connID, string(level))
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
}
