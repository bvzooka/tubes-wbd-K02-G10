package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"

	"dbplatform/internal/dbdriver"
)

// menjalankan sql lewat dbdriver.RunQuery supaya tercatat, lalu memanggil scan untuk setiap baris hasilnya
func eachRow(ctx context.Context, tx pgx.Tx, sql string, args []any, scan func(pgx.Rows) error) error {
	return dbdriver.RunQuery(ctx, sql, args, func(ctx context.Context) (int64, error) {
		rows, err := tx.Query(ctx, sql, args...)
		if err != nil {
			return 0, err
		}
		defer rows.Close()
		var n int64
		for rows.Next() {
			if err := scan(rows); err != nil {
				return n, err
			}
			n++
		}
		return n, rows.Err()
	})
}
