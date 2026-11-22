package pgmeta

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"dbplatform/internal/dbdriver"
)

//  nama schema meta di database target. Introspect dan viewer menyembunyikannya.
const Schema = "_dbplatform"

//  kode error PostgreSQL saat primary key atau nilai unik sudah ada
const sqlstateUniqueViolation = "23505"

// arah batch: forward untuk migration biasa, revert untuk pembatalan batch (rollback)
const (
	Forward = "forward"
	Revert = "revert"
)

// ErrDuplicateExecution dikembalikan RecordExecution bila execution_id itu sudah tercatat.
// Artinya eksekusi yang sama sudah pernah ter-commit, jadi pemanggil tidak boleh commit lagi.
var ErrDuplicateExecution = errors.New("pgmeta: eksekusi sudah tercatat ter-commit")

// ensureDDL membuat schema dan tiga tabel meta. IF NOT EXISTS membuatnya aman dipanggil berulang.
const ensureDDL = `
CREATE SCHEMA IF NOT EXISTS ` + Schema + `;

CREATE TABLE IF NOT EXISTS ` + Schema + `.executions (
    execution_id BIGINT      PRIMARY KEY,
    kind         TEXT        NOT NULL,
    attempt      INTEGER     NOT NULL,
    checksum     TEXT        NOT NULL,
    committed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS ` + Schema + `.batch_scopes (
    execution_id BIGINT PRIMARY KEY,
    scope_max    BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS ` + Schema + `.batches (
    execution_id  BIGINT      NOT NULL,
    batch_no      INTEGER     NOT NULL,
    direction     TEXT        NOT NULL CHECK (direction IN ('forward', 'revert')),
    lo_key        BIGINT      NOT NULL,
    hi_key        BIGINT      NOT NULL,
    rows_affected BIGINT      NOT NULL,
    committed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (execution_id, batch_no)
)`

// bukti satu eksekusi yang ter-commit di database target
type Execution struct {
	ID int64
	Kind string
	Attempt  int
	Checksum string
}

// checkpoint satu batch, LoKey eksklusif dan HiKey inklusif, sama dengan placeholder $1 dan $2.
type Batch struct {
	ExecutionID  int64
	BatchNo int
	Direction string
	LoKey int64
	HiKey int64
	RowsAffected int64
}

// membuat schema dan tabel meta bila belum ada. Dipanggil runner sebelum SQL migration dijalankan
// kalau gagal (biasanya karena user database target tidak punya hak CREATE), error-nya jelas dan migration tidak boleh dilanjutkan
// Pemanggil harus sudah memegang advisory lock database itu karena CREATE ... IF NOT EXISTS ga aman kalau dijalankan bersamaan di dua sesi
func Ensure(ctx context.Context, tx dbdriver.Tx) error {
	if _, err := tx.Exec(ctx, ensureDDL); err != nil {
		return fmt.Errorf("buat tabel meta %s di database target (user butuh hak CREATE): %w", Schema, err)
	}
	return nil
}

// mencatat bukti commit satu eksekusi, dipanggil di transaction yg sama dengan SQL migration
// execution_id udah ada berarti ErrDuplicateExecution, dan transaction pemanggil harus di-rollback
func RecordExecution(ctx context.Context, tx dbdriver.ArgTx, e Execution) error {
	_, err := tx.ExecArgs(ctx,
		`INSERT INTO `+Schema+`.executions (execution_id, kind, attempt, checksum) VALUES ($1, $2, $3, $4)`,
		e.ID, e.Kind, e.Attempt, e.Checksum)
	if isUniqueViolation(err) {
		return ErrDuplicateExecution
	}
	if err != nil {
		return fmt.Errorf("record execution %d: %w", e.ID, err)
	}
	return nil
}

// memeriksa apakah eksekusi sudah tercatat, dipakai rekonsiliasi saat hasil commit tidak pasti.
func ExecutionCommitted(ctx context.Context, tx dbdriver.ArgTx, executionID int64) (bool, error) {
	_, found, err := tx.QueryInt(ctx,
		`SELECT 1 FROM `+Schema+`.executions WHERE execution_id = $1`, executionID)
	if err != nil {
		return false, fmt.Errorf("check execution %d: %w", executionID, err)
	}
	return found, nil
}

// menyimpan batas atas kunci yang diproses eksekusi batch. Ditulis sekali di awal, jadi baris yang masuk setelahnya ga ikut diproses
// dipanggil lagi untuk id yang sama berarti error
func SetScope(ctx context.Context, tx dbdriver.ArgTx, executionID, scopeMax int64) error {
	_, err := tx.ExecArgs(ctx,
		`INSERT INTO `+Schema+`.batch_scopes (execution_id, scope_max) VALUES ($1, $2)`, executionID, scopeMax)
	if err != nil {
		return fmt.Errorf("set batch scope of execution %d: %w", executionID, err)
	}
	return nil
}

// membaca batas atas yang disimpan SetScope. found false bila eksekusi itu belum menulisnya.
func Scope(ctx context.Context, tx dbdriver.ArgTx, executionID int64) (scopeMax int64, found bool, err error) {
	scopeMax, found, err = tx.QueryInt(ctx,
		`SELECT scope_max FROM `+Schema+`.batch_scopes WHERE execution_id = $1`, executionID)
	if err != nil {
		return 0, false, fmt.Errorf("read batch scope of execution %d: %w", executionID, err)
	}
	return scopeMax, found, nil
}

// menulis checkpoint satu batch. Harus dipanggil di transaction yang sama dengan perubahan datanya, supaya checkpoint ada jika dan hanya jika batch itu ter-commit.
func RecordBatch(ctx context.Context, tx dbdriver.ArgTx, b Batch) error {
	_, err := tx.ExecArgs(ctx,
		`INSERT INTO `+Schema+`.batches (execution_id, batch_no, direction, lo_key, hi_key, rows_affected)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		b.ExecutionID, b.BatchNo, b.Direction, b.LoKey, b.HiKey, b.RowsAffected)
	if err != nil {
		return fmt.Errorf("record batch %d of execution %d: %w", b.BatchNo, b.ExecutionID, err)
	}
	return nil
}

// mengembalikan hi_key terbesar dari batch forward pada rantai eksekusi (root, resume, dst), yaitu titik lanjut untuk resume. found false kalau belum ada batch ter-commit
// chain berisi semua execution_id pada rantai, dikumpulkan dari db platform lewat root_execution_id
func ResumeCursor(ctx context.Context, tx dbdriver.ArgTx, chain []int64) (cursor int64, found bool, err error) {
	cursor, found, err = tx.QueryInt(ctx,
		`SELECT max(hi_key) FROM `+Schema+`.batches WHERE direction = 'forward' AND execution_id = ANY($1)`, chain)
	if err != nil {
		return 0, false, fmt.Errorf("read resume cursor: %w", err)
	}
	return cursor, found, nil
}

//  jumlah batch dan baris forward yang sudah ter-commit pada sebuah rantai eksekusi
type Progress struct {
	Batches int64
	Rows int64
}

// menghitung progres dari checkpoint di target. sumber kebenaran progres, dipake rekonsiliasi kalau penulisan progres ke db platform gagal
func ForwardProgress(ctx context.Context, tx dbdriver.ArgTx, chain []int64) (Progress, error) {
	var p Progress
	var err error
	// ::bigint karena sum() di PostgreSQL menghasilkan numeric, sedangkan QueryInt membaca int8
	p.Batches, _, err = tx.QueryInt(ctx,
		`SELECT count(*) FROM `+Schema+`.batches WHERE direction = 'forward' AND execution_id = ANY($1)`, chain)
	if err != nil {
		return Progress{}, fmt.Errorf("count forward batches: %w", err)
	}
	p.Rows, _, err = tx.QueryInt(ctx,
		`SELECT COALESCE(sum(rows_affected), 0)::bigint FROM `+Schema+`.batches
		 WHERE direction = 'forward' AND execution_id = ANY($1)`, chain)
	if err != nil {
		return Progress{}, fmt.Errorf("sum forward rows: %w", err)
	}
	return p, nil
}

// true kalau err (atau error yang dibungkusnya) adalah pelanggaran unique/primary key
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == sqlstateUniqueViolation
}
