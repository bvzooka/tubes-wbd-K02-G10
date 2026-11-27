package mysql

import (
	"context"
	"database/sql"

	"dbplatform/internal/dbdriver"
)

// menjalankan query lewat dbdriver.RunQuery supaya tercatat, lalu memanggil scan untuk tiap baris hasilnya
func eachRow(ctx context.Context, tx *sql.Tx, query string, args []any, scan func(*sql.Rows) error) error {
	return dbdriver.RunQuery(ctx, query, args, func(ctx context.Context) (int64, error) {
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return 0, err
		}
		var n int64
		for rows.Next() {
			if err := scan(rows); err != nil {
				rows.Close()
				return n, err
			}
			n++
		}
		return n, closeRows(rows)
	})
}

// scan untuk query satu kolom yang hasilnya cukup disimpan ke dest
func scanInto(dest any) func(*sql.Rows) error {
	return func(rows *sql.Rows) error { return rows.Scan(dest) }
}
