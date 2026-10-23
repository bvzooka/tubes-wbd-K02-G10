package testutil

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

// PgDriver adalah dbdriver.Driver minimal di atas pgxpool, khusus untuk test
type PgDriver struct {
	Pool *pgxpool.Pool
}

var _ dbdriver.Driver = (*PgDriver)(nil)

func NewPgDriver(pool *pgxpool.Pool) dbdriver.Driver { return &PgDriver{Pool: pool} }

func (d *PgDriver) Dialect() domain.DBMS { return domain.DBMSPostgres }
func (d *PgDriver) Capabilities() dbdriver.Capabilities {
	return dbdriver.Capabilities{TransactionalDDL: true}
}
func (d *PgDriver) Ping(ctx context.Context) error { return d.Pool.Ping(ctx) }
func (d *PgDriver) Close()                         {}

func (d *PgDriver) TableData(context.Context, domain.TableRef, domain.PageRequest) (*domain.TableData, error) {
	return nil, domain.ErrUnsupported
}

// Introspect hanya mengisi tabel dan kolom, urut deterministik
func (d *PgDriver) Introspect(ctx context.Context) (*domain.Schema, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT t.table_schema, t.table_name, t.table_type,
		       c.column_name, c.ordinal_position, c.data_type, c.is_nullable = 'YES', c.column_default
		FROM information_schema.tables t
		JOIN information_schema.columns c
		  ON c.table_schema = t.table_schema AND c.table_name = t.table_name
		WHERE t.table_schema NOT IN ('pg_catalog', 'information_schema')
		  AND t.table_schema NOT LIKE 'pg\_%'
		ORDER BY t.table_schema, t.table_name, c.ordinal_position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	s := &domain.Schema{}
	for rows.Next() {
		var schema, name, ttype string
		var col domain.Column
		if err := rows.Scan(&schema, &name, &ttype, &col.Name, &col.Position, &col.DataType, &col.Nullable, &col.Default); err != nil {
			return nil, err
		}
		if n := len(s.Tables); n == 0 || s.Tables[n-1].Schema != schema || s.Tables[n-1].Name != name {
			kind := domain.TableKindTable
			if ttype == "VIEW" {
				kind = domain.TableKindView
			}
			s.Tables = append(s.Tables, domain.Table{Schema: schema, Name: name, Kind: kind, EstimatedRows: -1})
		}
		t := &s.Tables[len(s.Tables)-1]
		t.Columns = append(t.Columns, col)
	}
	return s, rows.Err()
}

func (d *PgDriver) WithTx(ctx context.Context, opts dbdriver.TxOptions, fn func(dbdriver.Tx) error) (err error) {
	mode := pgx.ReadWrite
	if opts.ReadOnly {
		mode = pgx.ReadOnly
	}
	tx, err := d.Pool.BeginTx(ctx, pgx.TxOptions{AccessMode: mode})
	if err != nil {
		return err
	}
	done := false
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
		if !done {
			_ = tx.Rollback(context.WithoutCancel(ctx))
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
	done = true
	switch {
	case errors.Is(ferr, dbdriver.ErrForceRollback):
		return tx.Rollback(ctx)
	case ferr != nil:
		_ = tx.Rollback(ctx)
		return fmt.Errorf("tx: %w", ferr)
	}
	return tx.Commit(ctx)
}

type pgTx struct{ tx pgx.Tx }

// Exec tanpa argumen memakai simple protocol sehingga banyak statement diperbolehkan
func (t pgTx) Exec(ctx context.Context, sql string) (int64, error) {
	tag, err := t.tx.Exec(ctx, sql)
	return tag.RowsAffected(), err
}

// Query mengembalikan nilai sebagai teks dari server
// Simple protocol dipakai agar hasil selalu berformat teks
func (t pgTx) Query(ctx context.Context, sql string) (*domain.ResultSet, error) {
	rows, err := t.tx.Query(ctx, sql, pgx.QueryExecModeSimpleProtocol)
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
