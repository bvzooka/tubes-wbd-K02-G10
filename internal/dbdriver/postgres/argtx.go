package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"dbplatform/internal/dbdriver"
)

//  menambahkan eksekusi berparameter ke pgTx biar memenuhi dbdriver.ArgTx
type pgArgTx struct{ pgTx }

var _ dbdriver.ArgTx = pgArgTx{}

// menjalankan tepat satu statement dgn argumen ($1, $2, ...) lewat extended protocol
// SQL banyak statement ga bisa memakai argumen, untuk itu pakai Exec
func (t pgArgTx) ExecArgs(ctx context.Context, sql string, args ...any) (int64, error) {
	tag, err := t.tx.Exec(ctx, sql, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// membaca satu kolom int8 dari baris pertama. found false kalau ga ada baris atau nilainya NULL
func (t pgArgTx) QueryInt(ctx context.Context, sql string, args ...any) (int64, bool, error) {
	// pointer dipakai karena pgx mengisinya nil untuk NULL
	var v *int64
	err := t.tx.QueryRow(ctx, sql, args...).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if v == nil {
		return 0, false, nil
	}
	return *v, true, nil
}
