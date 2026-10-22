// Package authtest berisi helper integration test untuk modul auth, access, dan explain.
// Dipakai sampai internal/testutil (Aurel) tersedia; setelah itu helper ini boleh diganti.
package authtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
)

const defaultAdminURL = "postgres://postgres:postgres@localhost:5434/postgres?sslmode=disable"

// NewAppDB membuat database platform sementara dengan skema internal dari internal/appdb/sql.
func NewAppDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := newTempDB(t)
	dir := filepath.Join(repoRoot(t), "internal", "appdb", "sql")
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no internal sql files in %s: %v", dir, err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, pool, string(b))
	}
	return pool
}

// NewTargetDB membuat database target sementara dan menjalankan initSQL.
func NewTargetDB(t *testing.T, initSQL ...string) *pgxpool.Pool {
	t.Helper()
	pool := newTempDB(t)
	for _, s := range initSQL {
		mustExec(t, pool, s)
	}
	return pool
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

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string) {
	t.Helper()
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	// Simple protocol: satu string boleh berisi banyak statement.
	if _, err := conn.Conn().PgConn().Exec(context.Background(), sql).ReadAll(); err != nil {
		t.Fatalf("exec sql: %v", err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

// SeedUser menyisipkan user langsung ke tabel users dengan passwordHash apa adanya.
func SeedUser(t *testing.T, pool *pgxpool.Pool, username, passwordHash string, admin bool) domain.User {
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
		t.Fatal(err)
	}
}

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

func SeedConnection(t *testing.T, pool *pgxpool.Pool, name string, active bool) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `
		INSERT INTO connections (name, dbms, host, port, database_name, db_username, db_password_enc, is_active)
		VALUES ($1, 'postgres', 'localhost', 5432, 'sampledb', 'postgres', '\x00', $2) RETURNING id`,
		name, active).Scan(&id)
	if err != nil {
		t.Fatalf("seed connection: %v", err)
	}
	return id
}

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

// PgDriver adalah dbdriver.Driver minimal di atas pgxpool, hanya untuk test (WithTx dan Tx).
type PgDriver struct {
	Pool *pgxpool.Pool
}

var _ dbdriver.Driver = (*PgDriver)(nil)

func (d *PgDriver) Dialect() domain.DBMS { return domain.DBMSPostgres }
func (d *PgDriver) Capabilities() dbdriver.Capabilities {
	return dbdriver.Capabilities{TransactionalDDL: true}
}
func (d *PgDriver) Ping(ctx context.Context) error { return d.Pool.Ping(ctx) }
func (d *PgDriver) Introspect(context.Context) (*domain.Schema, error) {
	return nil, domain.ErrUnsupported
}
func (d *PgDriver) TableData(context.Context, domain.TableRef, domain.PageRequest) (*domain.TableData, error) {
	return nil, domain.ErrUnsupported
}
func (d *PgDriver) Close() {}

func (d *PgDriver) WithTx(ctx context.Context, opts dbdriver.TxOptions, fn func(dbdriver.Tx) error) (err error) {
	mode := pgx.ReadWrite
	if opts.ReadOnly {
		mode = pgx.ReadOnly
	}
	tx, err := d.Pool.BeginTx(ctx, pgx.TxOptions{AccessMode: mode})
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", timeout.Milliseconds())); err != nil {
		return err
	}
	ferr := fn(pgTx{tx})
	if ferr == dbdriver.ErrForceRollback {
		return tx.Rollback(ctx)
	}
	if ferr != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("tx: %w", ferr)
	}
	return tx.Commit(ctx)
}

type pgTx struct{ tx pgx.Tx }

func (t pgTx) Exec(ctx context.Context, sql string) (int64, error) {
	tag, err := t.tx.Exec(ctx, sql)
	return tag.RowsAffected(), err
}

// Query mengembalikan nilai sebagai teks mentah dari server (json tetap berupa teks JSON).
func (t pgTx) Query(ctx context.Context, sql string) (*domain.ResultSet, error) {
	rows, err := t.tx.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rs := &domain.ResultSet{}
	for _, fd := range rows.FieldDescriptions() {
		rs.Columns = append(rs.Columns, fd.Name)
	}
	for rows.Next() {
		raw := rows.RawValues()
		row := make([]domain.Cell, len(raw))
		for i, v := range raw {
			if v == nil {
				row[i] = domain.Cell{Null: true}
			} else {
				row[i] = domain.Cell{Value: string(v)}
			}
		}
		rs.Rows = append(rs.Rows, row)
	}
	return rs, rows.Err()
}
