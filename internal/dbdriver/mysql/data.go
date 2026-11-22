package mysql

import (
	"context"
	"database/sql"
	"dbplatform/internal/domain"
	"errors"
	gomysql "github.com/go-sql-driver/mysql"
	"strings"
	"time"
)

// cek tabel ada, di MySQL schema sama dengan nama database
const tableExistsSQL = `
SELECT TABLE_NAME
FROM information_schema.TABLES
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND TABLE_TYPE IN ('BASE TABLE', 'VIEW', 'SYSTEM VERSIONED')`

const primaryKeySQL = `
SELECT COLUMN_NAME
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND INDEX_NAME = 'PRIMARY'
ORDER BY SEQ_IN_INDEX`

// TableData membaca satu halaman isi tabel, urut menurut primary key supaya isi halaman konsisten
func (d *Driver) TableData(ctx context.Context, ref domain.TableRef, page domain.PageRequest) (*domain.TableData, error) {
	page = page.Normalize()
	out := &domain.TableData{Page: page.Page, PageSize: page.PageSize}
	if ref.Schema != d.dbName {
		return nil, domain.ErrNotFound
	}

	spec := txSpec{readOnly: true, timeout: 10 * time.Second}
	err := d.runTx(ctx, d.ro, spec, func(ctx context.Context, tx *sql.Tx) error {
		var canonical string
		err := tx.QueryRowContext(ctx, tableExistsSQL, ref.Schema, ref.Table).Scan(&canonical)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		// nama di MySQL bisa tidak peka huruf besar-kecil, hanya yang persis sama dianggap ada
		if canonical != ref.Table {
			return domain.ErrNotFound
		}

		pk, err := primaryKeyColumns(ctx, tx, ref)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, selectSQL(ref, pk), page.PageSize+1, page.Offset())
		if err != nil {
			return notFoundOnMissing(err)
		}
		defer rows.Close()
		rs, more, err := collect(rows, page.PageSize)
		if err != nil {
			return err
		}
		out.ResultSet = *rs
		out.HasNext = more
		return nil
	})
	if err != nil {
		return nil, unwrapNotFound(err)
	}
	return out, nil
}

// primaryKeyColumns mengambil kolom primary key berurutan
func primaryKeyColumns(ctx context.Context, tx *sql.Tx, ref domain.TableRef) ([]string, error) {
	rows, err := tx.QueryContext(ctx, primaryKeySQL, ref.Schema, ref.Table)
	if err != nil {
		return nil, err
	}
	var pk []string
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			rows.Close()
			return nil, err
		}
		pk = append(pk, col)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	return pk, nil
}

// selectSQL menyusun query halaman, limit dan offset diisi sebagai parameter
func selectSQL(ref domain.TableRef, pk []string) string {
	var b strings.Builder
	b.WriteString("SELECT * FROM ")
	b.WriteString(quoteIdent(ref.Schema))
	b.WriteString(".")
	b.WriteString(quoteIdent(ref.Table))
	if len(pk) > 0 {
		b.WriteString(" ORDER BY ")
		b.WriteString(quoteIdents(pk))
	}
	b.WriteString(" LIMIT ? OFFSET ?")
	return b.String()
}

// notFoundOnMissing mengubah error tabel tidak ada jadi ErrNotFound
func notFoundOnMissing(err error) error {
	var me *gomysql.MySQLError
	if errors.As(err, &me) && me.Number == erNoSuchTable {
		return domain.ErrNotFound
	}
	return err
}

// jaga ErrNotFound biar tetap dikenali
func unwrapNotFound(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return domain.ErrNotFound
	}
	return err
}
