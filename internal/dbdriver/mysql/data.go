package mysql

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	gomysql "github.com/go-sql-driver/mysql"

	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
)

// membatasi waktu pembacaan satu halaman data
const tableDataTimeout = 10 * time.Second

// cek tabel ada, di MySQL schema sama dengan nama database
const tableExistsSQL = `
SELECT TABLE_NAME
FROM information_schema.TABLES
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND TABLE_TYPE IN ('BASE TABLE', 'VIEW', 'SYSTEM VERSIONED')`

// primaryKeySQL mengambil kolom primary key berurutan sesuai posisinya di key
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

	spec := txSpec{readOnly: true, timeout: tableDataTimeout}
	err := d.runTx(ctx, d.ro, spec, func(ctx context.Context, tx *sql.Tx) error {
		var canonical string
		if err := eachRow(ctx, tx, tableExistsSQL, []any{ref.Schema, ref.Table}, scanInto(&canonical)); err != nil {
			return err
		}
		// tabel yang tidak ada membuat canonical tetap kosong. Nama di MySQL bisa tidak peka
		// huruf besar-kecil, jadi hanya yang persis sama dianggap ada
		if canonical == "" || canonical != ref.Table {
			return domain.ErrNotFound
		}

		pk, err := primaryKeyColumns(ctx, tx, ref)
		if err != nil {
			return err
		}
		query, args := selectSQL(ref, pk), []any{page.PageSize + 1, page.Offset()}
		return dbdriver.RunQuery(ctx, query, args, func(ctx context.Context) (int64, error) {
			rows, err := tx.QueryContext(ctx, query, args...)
			if err != nil {
				return 0, notFoundOnMissing(err)
			}
			defer rows.Close()
			rs, more, err := collect(rows, page.PageSize)
			if err != nil {
				return 0, err
			}
			out.ResultSet = *rs
			out.HasNext = more
			return int64(len(rs.Rows)), nil
		})
	})
	if err != nil {
		return nil, unwrapNotFound(err)
	}
	return out, nil
}

// primaryKeyColumns mengambil kolom primary key berurutan
func primaryKeyColumns(ctx context.Context, tx *sql.Tx, ref domain.TableRef) ([]string, error) {
	var pk []string
	err := eachRow(ctx, tx, primaryKeySQL, []any{ref.Schema, ref.Table}, func(rows *sql.Rows) error {
		var col string
		if err := rows.Scan(&col); err != nil {
			return err
		}
		pk = append(pk, col)
		return nil
	})
	return pk, err
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
