package migration_test

import (
	"context"
	"testing"
	"time"

	"dbplatform/internal/domain"
	"dbplatform/internal/migration"
)

// 25 baris dengan ukuran batch 10 menghasilkan 3 batch
const batchTableSQL = `
	CREATE TABLE items (id int PRIMARY KEY, val int NOT NULL CHECK (val BETWEEN 0 AND 99));
	INSERT INTO items SELECT g, 0 FROM generate_series(1, 25) g`

type batchEnv struct {
	runEnv
	d migration.Definition
}

func newBatchEnv(t *testing.T) batchEnv {
	t.Helper()
	r := newRunEnv(t)
	r.targetExec(t, batchTableSQL)
	return batchEnv{runEnv: r, d: r.readyBatch(t, "items", "id")}
}

func (r runEnv) targetExec(t *testing.T, sql string) {
	t.Helper()
	if _, err := r.target.Exec(context.Background(), sql); err != nil {
		t.Fatal(err)
	}
}

func (r runEnv) readyBatch(t *testing.T, table, key string) migration.Definition {
	t.Helper()
	ctx := context.Background()
	d, err := r.service.Create(ctx, r.editor, r.conn, migration.DraftInput{
		Name:    "tambah val",
		UpSQL:   "UPDATE items SET val = val + 1 WHERE id > $1 AND id <= $2",
		DownSQL: "UPDATE items SET val = val - 1 WHERE id > $1 AND id <= $2",
		Mode:    domain.ModeBatch,
		Batch:   &domain.BatchConfig{Table: table, KeyColumn: key, BatchSize: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	d, err = r.service.Freeze(ctx, r.editor, r.conn, d.Version, d.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// do menjalankan satu permintaan dari pengajuan sampai job ditutup
func (b batchEnv) do(t *testing.T, kind domain.ExecutionKind, resume bool) (domain.Execution, domain.RunResult) {
	t.Helper()
	ctx := context.Background()
	job := b.claim(t, b.d, kind, resume)
	result := b.run(job)
	if err := b.queue().Finish(ctx, job, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	e, err := b.repo.GetExecution(ctx, job.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	return e, result
}

// sumVal menjumlahkan kolom val di rentang id, nilainya menunjukkan berapa kali baris diproses
func (b batchEnv) sumVal(t *testing.T, from, to int) int {
	t.Helper()
	var sum int
	err := b.target.QueryRow(context.Background(),
		`SELECT COALESCE(sum(val), 0) FROM items WHERE id BETWEEN $1 AND $2`, from, to).Scan(&sum)
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

func (b batchEnv) want(t *testing.T, e domain.Execution, status domain.ExecutionStatus, state domain.MigrationState, batches int, rows int64) {
	t.Helper()
	d, err := b.repo.GetDefinition(context.Background(), b.conn, b.d.Version)
	if err != nil {
		t.Fatal(err)
	}
	if e.Status != status || d.State != state || e.BatchesCommitted != batches || e.RowsCommitted != rows {
		t.Errorf("permintaan %s, migration %s, batch %d, baris %d (%s); mau %s, %s, %d, %d",
			e.Status, d.State, e.BatchesCommitted, e.RowsCommitted, e.FailureMessage, status, state, batches, rows)
	}
}

func TestBatch_Integration_ApplyThenRollback(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	b := newBatchEnv(t)

	applied, _ := b.do(t, domain.KindApply, false)
	b.want(t, applied, domain.ExecSuccess, domain.StateApplied, 3, 25)
	if got := b.sumVal(t, 1, 25); got != 25 {
		t.Errorf("tiap baris harus diproses sekali, jumlah = %d", got)
	}

	back, _ := b.do(t, domain.KindRollback, false)
	b.want(t, back, domain.ExecSuccess, domain.StateRolledBack, 3, 25)
	if got := b.sumVal(t, 1, 25); got != 0 {
		t.Errorf("setelah rollback jumlah = %d", got)
	}
	// batch terakhir harus dibatalkan paling dulu
	var firstReverted int
	err := b.target.QueryRow(context.Background(),
		`SELECT hi_key FROM _dbplatform.batches WHERE direction = 'revert' AND batch_no = 1`).Scan(&firstReverted)
	if err != nil || firstReverted != 25 {
		t.Errorf("urutan pembatalan mulai dari %d, %v", firstReverted, err)
	}

	again, _ := b.do(t, domain.KindForward, false)
	b.want(t, again, domain.ExecSuccess, domain.StateApplied, 3, 25)
}

func TestBatch_Integration_PartialThenResume(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	b := newBatchEnv(t)
	// baris 15 akan melanggar CHECK di batch kedua
	b.targetExec(t, `UPDATE items SET val = 99 WHERE id = 15`)

	partial, result := b.do(t, domain.KindApply, false)
	b.want(t, partial, domain.ExecPartial, domain.StatePartial, 1, 10)
	if result.Retryable || b.sumVal(t, 1, 10) != 10 || b.sumVal(t, 16, 25) != 0 {
		t.Errorf("hanya batch pertama yang boleh tercommit: %+v", result)
	}
	// database setengah jalan menahan migration lain
	other := b.readyWith(t, "CREATE TABLE lain (id int)", "DROP TABLE lain")
	if _, err := b.executions.Request(context.Background(), b.editor, b.conn, other.Version, domain.KindApply, false); err == nil {
		t.Error("migration lain harus ditolak selama database terblok")
	}

	b.targetExec(t, `UPDATE items SET val = 0 WHERE id = 15`)
	resumed, _ := b.do(t, domain.KindForward, true)
	b.want(t, resumed, domain.ExecSuccess, domain.StateApplied, 2, 15)
	if got := b.sumVal(t, 1, 25); got != 25 {
		t.Errorf("batch lama tidak boleh diulang, jumlah = %d", got)
	}

	// rollback membatalkan batch dari kedua eksekusi
	back, _ := b.do(t, domain.KindRollback, false)
	b.want(t, back, domain.ExecSuccess, domain.StateRolledBack, 3, 25)
	if got := b.sumVal(t, 1, 25); got != 0 {
		t.Errorf("setelah rollback jumlah = %d", got)
	}
}

func TestBatch_Integration_PartiallyRevertedThenContinue(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	b := newBatchEnv(t)
	b.do(t, domain.KindApply, false)
	// pembatalan batch pertama (diproses terakhir) akan membuat val negatif
	b.targetExec(t, `UPDATE items SET val = 0 WHERE id = 5`)

	half, _ := b.do(t, domain.KindRollback, false)
	b.want(t, half, domain.ExecPartiallyReverted, domain.StatePartiallyReverted, 2, 15)

	b.targetExec(t, `UPDATE items SET val = 1 WHERE id = 5`)
	rest, _ := b.do(t, domain.KindRollback, false)
	b.want(t, rest, domain.ExecSuccess, domain.StateRolledBack, 1, 10)
	if got := b.sumVal(t, 1, 25); got != 0 {
		t.Errorf("tidak boleh ada yang dibatalkan dua kali, jumlah = %d", got)
	}
}

func TestBatch_Integration_FirstBatchFails(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	b := newBatchEnv(t)
	b.targetExec(t, `UPDATE items SET val = 99 WHERE id = 3`)

	failed, _ := b.do(t, domain.KindApply, false)

	b.want(t, failed, domain.ExecFailed, domain.StateFailed, 0, 0)
}

func TestBatch_Integration_KeyMustBeUniqueInteger(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	b := newBatchEnv(t)
	b.d = b.readyBatch(t, "items", "val")
	// versi 1 dibatalkan supaya versi 2 boleh diajukan
	b.exec(t, `UPDATE db_migrations SET state = 'cancelled' WHERE version = 1`)

	e, _ := b.do(t, domain.KindApply, false)

	if e.Status != domain.ExecFailed || e.FailureCode != migration.CodeRevalidation || b.sumVal(t, 1, 25) != 0 {
		t.Errorf("permintaan = %s %s %s", e.Status, e.FailureCode, e.FailureMessage)
	}
}

// worker mati setelah satu batch tercommit
func TestBatch_Integration_ReconcileCountsCheckpoints(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	b := newBatchEnv(t)
	ctx := context.Background()
	job := b.claim(t, b.d, domain.KindApply, false)
	if err := b.repo.MarkRunning(ctx, job, job.ExecutionID, time.Now()); err != nil {
		t.Fatal(err)
	}
	b.targetExec(t, `CREATE SCHEMA _dbplatform;
		CREATE TABLE _dbplatform.executions (execution_id BIGINT PRIMARY KEY, kind TEXT, attempt INT,
			checksum TEXT, committed_at TIMESTAMPTZ DEFAULT now());
		CREATE TABLE _dbplatform.batches (execution_id BIGINT, batch_no INT, direction TEXT, lo_key BIGINT,
			hi_key BIGINT, rows_affected BIGINT, committed_at TIMESTAMPTZ DEFAULT now(), PRIMARY KEY (execution_id, batch_no))`)
	if _, err := b.target.Exec(ctx,
		`INSERT INTO _dbplatform.batches VALUES ($1, 1, 'forward', 0, 10, 10)`, job.ExecutionID); err != nil {
		t.Fatal(err)
	}

	b.runner().Reconcile(ctx, job)

	e, err := b.repo.GetExecution(ctx, job.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	b.want(t, e, domain.ExecPartial, domain.StatePartial, 1, 10)
}
