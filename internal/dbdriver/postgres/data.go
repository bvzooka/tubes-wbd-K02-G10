package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

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
		rows, err := tx.Query(ctx, selectPageSQL(ref, order), pgx.QueryExecModeExec,
			int64(page.PageSize+1), int64(page.Offset()))
		if err != nil {
			return err
		}
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

// ensureTableExists mengembalikan ErrNotFound bila tabel tidak ada atau termasuk tabel sistem
func ensureTableExists(ctx context.Context, tx pgx.Tx, ref domain.TableRef) error {
	var one int
	err := tx.QueryRow(ctx, tableExistsSQL, ref.Schema, ref.Table).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

// primaryKeyColumns mengambil kolom primary key berurutan, sudah diberi quote dan siap dipakai di ORDER BY
func primaryKeyColumns(ctx context.Context, tx pgx.Tx, ref domain.TableRef) ([]string, error) {
	rows, err := tx.Query(ctx, primaryKeySQL, ref.Schema, ref.Table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var order []string
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return nil, err
		}
		order = append(order, pgx.Identifier{col}.Sanitize())
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return order, nil
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
