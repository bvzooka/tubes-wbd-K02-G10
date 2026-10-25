// Package postgres adalah implementasi dbdriver.Driver untuk PostgreSQL (pgx).
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
)

const (
	maxConns       = 5
	connectTimeout = 5 * time.Second
	defaultTxLimit = 30 * time.Second
)

// Driver memenuhi dbdriver.Driver di atas pgxpool.
type Driver struct {
	pool *pgxpool.Pool
}

var _ dbdriver.Driver = (*Driver)(nil)

// New membangun driver untuk satu koneksi dan memastikan server terjangkau.
// Signature-nya cocok dengan connmgr.DriverFactory.
func New(ctx context.Context, c domain.Connection, password string) (dbdriver.Driver, error) {
	if c.DBMS != domain.DBMSPostgres {
		return nil, fmt.Errorf("driver postgres tidak mendukung dbms %q: %w", c.DBMS, domain.ErrUnsupported)
	}
	d, err := NewFromURL(ctx, BuildURL(c, password))
	if err != nil {
		return nil, err
	}
	return d, nil
}

// NewFromURL membuat driver dari URL koneksi dan melakukan Ping awal.
func NewFromURL(ctx context.Context, connURL string) (*Driver, error) {
	cfg, err := pgxpool.ParseConfig(connURL)
	if err != nil {
		// Pesan parse bisa memuat URL (termasuk password): jangan diteruskan.
		return nil, errors.New("konfigurasi koneksi tidak valid")
	}
	cfg.MaxConns = maxConns
	cfg.ConnConfig.ConnectTimeout = connectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, ClassifyError(err)
	}
	d := &Driver{pool: pool}
	if err := d.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return d, nil
}

// NewFromPool membungkus pool yang sudah ada (dipakai test). Pool ditutup oleh Close.
func NewFromPool(pool *pgxpool.Pool) *Driver { return &Driver{pool: pool} }

func (d *Driver) Dialect() domain.DBMS { return domain.DBMSPostgres }

func (d *Driver) Capabilities() dbdriver.Capabilities {
	return dbdriver.Capabilities{TransactionalDDL: true}
}

func (d *Driver) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := d.pool.Ping(ctx); err != nil {
		return ClassifyError(err)
	}
	return nil
}

func (d *Driver) Close() { d.pool.Close() }

type txSpec struct {
	readOnly bool
	iso      pgx.TxIsoLevel
	timeout  time.Duration
}

// runTx menjalankan fn dalam satu transaction dengan statement_timeout.
// fn nil: commit. ErrForceRollback: rollback dan nil. Lainnya: rollback dan error.
// Panic di fn tetap menghasilkan rollback.
func (d *Driver) runTx(ctx context.Context, spec txSpec, fn func(pgx.Tx) error) error {
	mode := pgx.ReadWrite
	if spec.readOnly {
		mode = pgx.ReadOnly
	}
	tx, err := d.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: mode, IsoLevel: spec.iso})
	if err != nil {
		return ClassifyError(err)
	}
	// Rollback setelah commit mengembalikan ErrTxClosed dan diabaikan.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	timeout := spec.timeout
	if timeout <= 0 {
		timeout = defaultTxLimit
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", timeout.Milliseconds())); err != nil {
		return ClassifyError(err)
	}

	ferr := fn(tx)
	if errors.Is(ferr, dbdriver.ErrForceRollback) {
		return nil // rollback dilakukan defer
	}
	if ferr != nil {
		return fmt.Errorf("tx: %w", ferr)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func (d *Driver) WithTx(ctx context.Context, opts dbdriver.TxOptions, fn func(tx dbdriver.Tx) error) error {
	return d.runTx(ctx, txSpec{readOnly: opts.ReadOnly, timeout: opts.Timeout}, func(tx pgx.Tx) error {
		return fn(pgTx{tx})
	})
}

type pgTx struct{ tx pgx.Tx }

// Exec memakai simple protocol (tanpa argumen) sehingga banyak statement boleh
// dalam satu string. Mengembalikan baris terdampak dari perintah terakhir.
func (t pgTx) Exec(ctx context.Context, sql string) (int64, error) {
	tag, err := t.tx.Exec(ctx, sql)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// Query memakai extended protocol (tepat satu statement) dengan hasil format teks.
func (t pgTx) Query(ctx context.Context, sql string) (*domain.ResultSet, error) {
	rows, err := t.tx.Query(ctx, sql, pgx.QueryExecModeExec)
	if err != nil {
		return nil, err
	}
	rs, _, err := collect(rows, -1)
	return rs, err
}

// collect membaca hasil sebagai teks. limit < 0 berarti semua baris; jika tidak,
// paling banyak limit baris dibaca dan more menandakan masih ada baris lain.
func collect(rows pgx.Rows, limit int) (rs *domain.ResultSet, more bool, err error) {
	defer rows.Close()
	rs = &domain.ResultSet{}
	for _, fd := range rows.FieldDescriptions() {
		rs.Columns = append(rs.Columns, fd.Name)
	}
	for rows.Next() {
		if limit >= 0 && len(rs.Rows) >= limit {
			more = true
			break
		}
		raw := rows.RawValues()
		row := make([]domain.Cell, len(raw))
		for i, v := range raw {
			if v == nil {
				row[i] = domain.Cell{Null: true}
			} else {
				row[i] = domain.Cell{Value: string(v)} // string() menyalin buffer
			}
		}
		rs.Rows = append(rs.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return rs, more, nil
}
