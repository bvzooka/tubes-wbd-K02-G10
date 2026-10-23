package postgres

import (
	"context"
	"errors"
	"strings"
	"time"
	"github.com/jackc/pgx/v5"
	"dbplatform/internal/domain"
)

const tableExistsSQL = `
SELECT 1
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2 AND ` + userRel

const primaryKeySQL = `
SELECT a.attname::text
FROM pg_index i
JOIN pg_class c ON c.oid = i.indrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY k(attnum, ord)
JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
WHERE n.nspname = $1 AND c.relname = $2 AND i.indisprimary
ORDER BY k.ord`

// baca satu halaman data tabel, nama tabel divalidasi 
func (d *Driver) TableData(ctx context.Context, ref domain.TableRef, page domain.PageRequest) (*domain.TableData, error) {
	page = page.Normalize()
	out := &domain.TableData{Page: page.Page, PageSize: page.PageSize}
	spec := txSpec{readOnly: true, timeout: 10 * time.Second}
	err := d.runTx(ctx, spec, func(tx pgx.Tx) error {
		var one int
		if err := tx.QueryRow(ctx, tableExistsSQL, ref.Schema, ref.Table).Scan(&one); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrNotFound
			}
			return err
		}

		pkRows, err := tx.Query(ctx, primaryKeySQL, ref.Schema, ref.Table)
		if err != nil {
			return err
		}
		var order []string
		for pkRows.Next() {
			var col string
			if err := pkRows.Scan(&col); err != nil {
				pkRows.Close()
				return err
			}
			order = append(order, pgx.Identifier{col}.Sanitize())
		}
		pkRows.Close()
		if err := pkRows.Err(); err != nil {
			return err
		}

		sql := "SELECT * FROM " + pgx.Identifier{ref.Schema, ref.Table}.Sanitize()
		if len(order) > 0 {
			sql += " ORDER BY " + strings.Join(order, ", ")
		}
		sql += " LIMIT $1 OFFSET $2"

		// limit size+1 utk tau ada halaman berikutnya ga
		rows, err := tx.Query(ctx, sql, pgx.QueryExecModeExec, int64(page.PageSize+1), int64(page.Offset()))
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

// jaga ErrNotFound biar tetap dikenali
func unwrapNotFound(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return domain.ErrNotFound
	}
	return err
}
