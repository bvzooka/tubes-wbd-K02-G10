package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
)

// tableDataTimeout membatasi waktu pembacaan satu halaman data
const tableDataTimeout = 10 * time.Second

// tableExistsSQL mengecek tabel ada di schema itu dan bukan tabel sistem
const tableExistsSQL = `
SELECT 1
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2 AND ` + userRel

// primaryKeySQL mengambil kolom primary key berurutan sesuai posisinya di key
const primaryKeySQL = `
SELECT a.attname::text
FROM pg_index i
JOIN pg_class c ON c.oid = i.indrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY k(attnum, ord)
JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
WHERE n.nspname = $1 AND c.relname = $2 AND i.indisprimary
ORDER BY k.ord`

// TableData membaca satu halaman isi tabel. Nama tabel dicek dulu ke katalog, jadi nama yang salah
// menghasilkan ErrNotFound, bukan error SQL.
func (d *Driver) TableData(ctx context.Context, ref domain.TableRef, page domain.PageRequest) (*domain.TableData, error) {
	page = page.Normalize()
	out := &domain.TableData{Page: page.Page, PageSize: page.PageSize}
	spec := txSpec{readOnly: true, timeout: tableDataTimeout}
	err := d.runTx(ctx, spec, func(tx pgx.Tx) error {
		if err := ensureTableExists(ctx, tx, ref); err != nil {
			return err
		}
		// urut menurut primary key supaya isi halaman konsisten antar request
		order, err := primaryKeyColumns(ctx, tx, ref)
		if err != nil {
			return err
		}

		// limit size+1 untuk tahu apakah masih ada halaman berikutnya
		sql, limit, offset := selectPageSQL(ref, order), int64(page.PageSize+1), int64(page.Offset())
		return dbdriver.RunQuery(ctx, sql, []any{limit, offset}, func(ctx context.Context) (int64, error) {
			rows, err := tx.Query(ctx, sql, pgx.QueryExecModeExec, limit, offset)
			if err != nil {
				return 0, err
			}
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

// ensureTableExists mengembalikan ErrNotFound bila tabel tidak ada atau termasuk tabel sistem
func ensureTableExists(ctx context.Context, tx pgx.Tx, ref domain.TableRef) error {
	found := false
	err := eachRow(ctx, tx, tableExistsSQL, []any{ref.Schema, ref.Table}, func(pgx.Rows) error {
		found = true
		return nil
	})
	if err == nil && !found {
		return domain.ErrNotFound
	}
	return err
}

// primaryKeyColumns mengambil kolom primary key berurutan, sudah diberi quote dan siap dipakai di ORDER BY
func primaryKeyColumns(ctx context.Context, tx pgx.Tx, ref domain.TableRef) ([]string, error) {
	var order []string
	err := eachRow(ctx, tx, primaryKeySQL, []any{ref.Schema, ref.Table}, func(rows pgx.Rows) error {
		var col string
		if err := rows.Scan(&col); err != nil {
			return err
		}
		order = append(order, pgx.Identifier{col}.Sanitize())
		return nil
	})
	return order, err
}

// selectPageSQL menyusun query satu halaman, limit dan offset diisi sebagai parameter $1 dan $2.
// Sanitize memberi quote pada nama schema dan tabel yang berasal dari URL, mencegah SQL injection.
func selectPageSQL(ref domain.TableRef, order []string) string {
	sql := "SELECT * FROM " + pgx.Identifier{ref.Schema, ref.Table}.Sanitize()
	if len(order) > 0 {
		sql += " ORDER BY " + strings.Join(order, ", ")
	}
	return sql + " LIMIT $1 OFFSET $2"
}

// unwrapNotFound mengembalikan ErrNotFound polos supaya tetap dikenali walau sudah dibungkus "tx: ..."
func unwrapNotFound(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return domain.ErrNotFound
	}
	return err
}
