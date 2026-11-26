package migration

import (
	"context"
	"fmt"
	"time"

	"dbplatform/internal/dbdriver"
	"dbplatform/internal/dbdriver/pgmeta"
	"dbplatform/internal/domain"
)

// CodeTargetConflict berarti database target menyimpan bukti bernomor sama yang bukan milik permintaan ini
const CodeTargetConflict = "target_conflict"

// kelonggaran selisih jam antara server platform dan server database target
const clockSkew = time.Minute

// proofSince adalah waktu paling awal sebuah bukti masih mungkin milik permintaan ini
func proofSince(e domain.Execution) time.Time {
	return e.RequestedAt.Add(-clockSkew)
}

// ownProof true kalau ada bukti selesai yang nomor, jenis, isi, dan waktunya cocok
// nomor saja tidak cukup karena nomor mulai dari satu lagi kalau database platform di-reset
func ownProof(ctx context.Context, tx dbdriver.ArgTx, e domain.Execution) (bool, error) {
	_, found, err := tx.QueryInt(ctx, `
		SELECT 1 FROM `+pgmeta.Schema+`.executions
		WHERE execution_id = $1 AND checksum = $2 AND kind = $3 AND committed_at >= $4`,
		e.ID, e.Checksum, string(e.Kind), proofSince(e))
	if err != nil {
		return false, fmt.Errorf("check proof %d: %w", e.ID, err)
	}
	return found, nil
}

// checkProofOwner menolak menjalankan apa pun kalau ada sisa bukti asing, supaya tidak dikira sudah pernah jalan
func checkProofOwner(ctx context.Context, tx dbdriver.ArgTx, e domain.Execution, chain []int64, since time.Time) error {
	foreign, _, err := tx.QueryInt(ctx, `
		SELECT (SELECT count(*) FROM `+pgmeta.Schema+`.executions
		        WHERE execution_id = ANY($1)
		          AND (committed_at < $2 OR (execution_id = $3 AND (checksum <> $4 OR kind <> $5))))
		     + (SELECT count(*) FROM `+pgmeta.Schema+`.batches
		        WHERE execution_id = ANY($1) AND committed_at < $2)`,
		chain, since, e.ID, e.Checksum, string(e.Kind))
	if err != nil {
		return fmt.Errorf("check proof owner: %w", err)
	}
	if foreign > 0 {
		return rejected(CodeTargetConflict,
			"database target menyimpan bukti eksekusi #%d dari platform lain atau dari sebelum database platform di-reset, "+
				"hapus schema %s di database target kalau datanya sudah tidak dipakai", e.ID, pgmeta.Schema)
	}
	return nil
}
