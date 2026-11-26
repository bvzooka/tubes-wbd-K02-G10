package migration

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"dbplatform/internal/dbdriver"
	"dbplatform/internal/dbdriver/pgmeta"
	"dbplatform/internal/domain"
)

type batchTable struct{ table, key string }

func newBatchTable(c domain.BatchConfig) batchTable {
	return batchTable{
		table: pgx.Identifier{c.Schema, c.Table}.Sanitize(),
		key:   pgx.Identifier{c.KeyColumn}.Sanitize(),
	}
}

// checkBatchKey memastikan kolom kunci bertipe integer dan unik sendirian, tanpa itu rentang batch bisa melewatkan atau mengulang baris
func checkBatchKey(ctx context.Context, tx dbdriver.ArgTx, t batchTable, column string) error {
	found, _, err := tx.QueryInt(ctx, `
		SELECT count(*)
		FROM pg_index i
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = i.indkey[0]
		WHERE i.indrelid = to_regclass($1) AND i.indisunique AND i.indnkeyatts = 1 AND i.indpred IS NULL
		  AND a.attname = $2 AND a.atttypid IN ('int2'::regtype, 'int4'::regtype, 'int8'::regtype)`,
		t.table, column)
	if err != nil {
		return fmt.Errorf("check batch key: %w", err)
	}
	if found == 0 {
		return rejected(CodeRevalidation, "kolom %s di tabel %s harus ada, bertipe integer, dan primary key atau unique", column, t.table)
	}
	return nil
}

// bounds mengambil titik awal dan batas akhir pekerjaan, empty true kalau tabelnya kosong
func (t batchTable) bounds(ctx context.Context, tx dbdriver.ArgTx) (start, scopeMax int64, empty bool, err error) {
	scopeMax, found, err := tx.QueryInt(ctx, fmt.Sprintf(`SELECT max(%s)::bigint FROM %s`, t.key, t.table))
	if err != nil || !found {
		return 0, 0, !found, err
	}
	// batas bawah eksklusif, jadi mulai satu di bawah kunci terkecil
	start, _, err = tx.QueryInt(ctx, fmt.Sprintf(`SELECT min(%s)::bigint - 1 FROM %s`, t.key, t.table))
	return start, scopeMax, false, err
}

// next mencari kunci terakhir dari size baris berikutnya, jadi tiap batch tetap berisi size baris walau nomornya bolong
func (t batchTable) next(ctx context.Context, tx dbdriver.ArgTx, after, scopeMax int64, size int) (int64, bool, error) {
	query := fmt.Sprintf(`
		SELECT max(k)::bigint
		FROM (SELECT %[1]s AS k FROM %[2]s WHERE %[1]s > $1 AND %[1]s <= $2 ORDER BY %[1]s LIMIT $3) s`,
		t.key, t.table)
	return tx.QueryInt(ctx, query, after, scopeMax, size)
}

// nextUnreverted mencari batch maju terbesar di rantai yang belum punya pasangan pembatalan
func nextUnreverted(ctx context.Context, tx dbdriver.ArgTx, chain []int64) (lo, hi int64, found bool, err error) {
	hi, found, err = tx.QueryInt(ctx, `
		SELECT max(f.hi_key)
		FROM `+pgmeta.Schema+`.batches f
		WHERE f.direction = 'forward' AND f.execution_id = ANY($1)
		  AND NOT EXISTS (
		      SELECT 1 FROM `+pgmeta.Schema+`.batches r
		      WHERE r.direction = 'revert' AND r.execution_id = ANY($1) AND r.hi_key = f.hi_key)`, chain)
	if err != nil || !found {
		return 0, 0, false, err
	}
	lo, found, err = tx.QueryInt(ctx, `
		SELECT lo_key FROM `+pgmeta.Schema+`.batches
		WHERE direction = 'forward' AND execution_id = ANY($1) AND hi_key = $2`, chain, hi)
	return lo, hi, found, err
}

// ownProgress menghitung batch dan baris yang tercommit oleh satu eksekusi, dibaca dari checkpoint di target
func ownProgress(ctx context.Context, tx dbdriver.ArgTx, e domain.Execution) (batches int, rows int64, err error) {
	const mine = `.batches WHERE execution_id = $1 AND committed_at >= $2`
	count, _, err := tx.QueryInt(ctx, `SELECT count(*) FROM `+pgmeta.Schema+mine, e.ID, proofSince(e))
	if err != nil {
		return 0, 0, fmt.Errorf("count batches: %w", err)
	}
	rows, _, err = tx.QueryInt(ctx,
		`SELECT COALESCE(sum(rows_affected), 0)::bigint FROM `+pgmeta.Schema+mine, e.ID, proofSince(e))
	if err != nil {
		return 0, 0, fmt.Errorf("sum batch rows: %w", err)
	}
	return int(count), rows, nil
}
