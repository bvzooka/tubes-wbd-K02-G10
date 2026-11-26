package migration

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DB adalah bagian dari pool yang dipakai repo, supaya mudah diganti di test
type DB interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Repo menyimpan migration di tabel db_migrations
type Repo struct{ db DB }

// NewRepo dibuat dengan koneksi database platform
func NewRepo(db DB) *Repo { return &Repo{db: db} }

// kode error PostgreSQL untuk rujukan yang tidak ada
const foreignKeyViolation = "23503"

// Kunci per database biar pembuatan dan perubahan status ga balapan
func lockMigrations(ctx context.Context, tx pgx.Tx, connID int64) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('migration:' || $1::bigint::text, 0))`, connID)
	if err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	return nil
}
