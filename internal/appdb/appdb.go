package appdb

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed sql/*.sql
var sqlFS embed.FS

// migrateLockID adalah kunci advisory lock agar dua proses tidak menjalankan runner bersamaan
const migrateLockID int64 = 7_310_001

// Open membuka pool ke database platform dan memastikan koneksinya hidup
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("appdb: buka pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("appdb: ping database platform: %w", err)
	}
	return pool, nil
}

// Migrate menjalankan file sql/*.sql yang belum tercatat di app_schema_versions
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	return migrateFS(ctx, pool, sqlFS, "sql")
}

func migrateFS(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, dir string) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return fmt.Errorf("appdb: baca file sql: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	// Satu koneksi khusus agar advisory lock (level session) dipegang sampai selesai
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("appdb: ambil koneksi: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrateLockID); err != nil {
		return fmt.Errorf("appdb: ambil lock: %w", err)
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, migrateLockID) }()

	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS app_schema_versions (
			version    TEXT        PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("appdb: buat app_schema_versions: %w", err)
	}

	applied := map[string]bool{}
	rows, err := conn.Query(ctx, `SELECT version FROM app_schema_versions`)
	if err != nil {
		return fmt.Errorf("appdb: baca versi: %w", err)
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, name := range names {
		version := strings.TrimSuffix(name, ".sql")
		if applied[version] {
			continue
		}
		body, err := fs.ReadFile(fsys, dir+"/"+name)
		if err != nil {
			return fmt.Errorf("appdb: baca %s: %w", name, err)
		}
		if err := apply(ctx, conn, version, string(body)); err != nil {
			return fmt.Errorf("appdb: jalankan %s: %w", name, err)
		}
	}
	return nil
}

// Tiap file dijalanin dalam satu transaction bersama pencatatannya
func apply(ctx context.Context, conn *pgxpool.Conn, version, body string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// Tanpa argumen, pgx memakai simple protocol sehingga satu file boleh berisi banyak statement
	if _, err := tx.Exec(ctx, body); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO app_schema_versions (version) VALUES ($1)`, version); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
