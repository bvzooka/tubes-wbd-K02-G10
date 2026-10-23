// Package testutil berisi helper test bersama: database sementara, driver palsu, dan seed data
package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/appdb"
)

const defaultAdminURL = "postgres://postgres:postgres@localhost:5434/postgres?sslmode=disable"

// NewAppDB membuat database platform sementara (test_<acak>) lengkap dengan skema internal, lalu hapus saat test selesai
func NewAppDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := newTempDB(t)
	if err := appdb.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("migrate app db: %v", err)
	}
	return pool
}

// NewTargetDB membuat database target sementara tanpa skema internal dan menjalankan initSQL (boleh berisi banyak statement) secara berurutan
func NewTargetDB(t *testing.T, initSQL ...string) *pgxpool.Pool {
	t.Helper()
	pool := newTempDB(t)
	for _, s := range initSQL {
		MustExec(t, pool, s)
	}
	return pool
}

// MustExec menjalankan SQL (tanpa argumen, banyak statement diperbolehkan) atau menggagalkan test
func MustExec(t *testing.T, pool *pgxpool.Pool, sql string) {
	t.Helper()
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Conn().PgConn().Exec(context.Background(), sql).ReadAll(); err != nil {
		t.Fatalf("exec sql: %v", err)
	}
}

func newTempDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	adminURL := os.Getenv("TEST_ADMIN_URL")
	if adminURL == "" {
		adminURL = defaultAdminURL
	}
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect TEST_ADMIN_URL (jalankan `make up`): %v", err)
	}
	defer admin.Close(ctx)

	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "test_" + hex.EncodeToString(b)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database: %v", err)
	}

	cfg, err := pgxpool.ParseConfig(adminURL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		c, err := pgx.Connect(context.Background(), adminURL)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	return pool
}
