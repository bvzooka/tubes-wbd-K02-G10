package migration

import (
	"context"
	"errors"
	"sync"
	"time"

	"dbplatform/internal/contract"
	"dbplatform/internal/dbdriver"
	"dbplatform/internal/dbdriver/pgmeta"
	"dbplatform/internal/domain"
	"dbplatform/internal/telemetry"
)

const batchTimeout = 2 * time.Minute

type batchRun struct {
	runner  *Runner
	session dbdriver.Session
	job     domain.Job
	e       domain.Execution
	table   batchTable
	chain   []int64
	since   time.Time // checkpoint rangkaian ini tidak mungkin lebih tua dari ini
	attempt *Attempt
	// true kalau isi transaction sudah selesai dan error yang muncul berasal dari commit
	committing bool
}

// runBatch memegang kunci database sepanjang job, termasuk di sela commit antar batch
func (r *Runner) runBatch(
	lease contract.Lease, job domain.Job, e domain.Execution, driver dbdriver.Driver, a Attempt,
) domain.RunResult {
	ctx := lease.Context()
	platform := context.WithoutCancel(ctx)

	if e.Batch == nil {
		return r.fail(platform, job, e, a, rejected(CodeRevalidation, "pengaturan batch tidak ada"))
	}
	root := e.ID
	if e.RootExecutionID != nil {
		root = *e.RootExecutionID
	}
	chain, err := r.deps.Store.ChainIDs(platform, root)
	if err != nil {
		return later(err)
	}
	first, err := r.deps.Store.GetExecution(platform, root)
	if err != nil {
		return later(err)
	}
	sqlCtx := targetContext(ctx, job, e)
	session, locked, err := lockTarget(sqlCtx, driver)
	if err != nil {
		return r.fail(platform, job, e, a, transient(CodeConnection, err))
	}
	if !locked {
		return later(domain.ErrDatabaseBusy)
	}
	release := sync.OnceFunc(session.Close)
	defer release()

	if err := r.deps.Store.MarkRunning(platform, job, e.ID, a.StartedAt); err != nil {
		return later(err)
	}
	r.deps.Logger.InfoContext(ctx, "migration started",
		"event", "migration.started", "actor", e.RequestedByName, "connection_id", e.ConnectionID,
		"execution_id", e.ID, "job_id", job.ID, "attempt", job.Attempt, "mode", e.Mode)

	run := &batchRun{
		runner: r, session: session, job: job, e: e, table: newBatchTable(*e.Batch), chain: chain, since: proofSince(first), attempt: &a,
	}
	err = run.execute(sqlCtx, lease, root)

	switch {
	case lease.Lost():
		return later(errors.New("lease hilang saat menjalankan batch"))
	case err == nil:
		a.Outcome, a.Committed = domain.OutcomeSuccess, true
		return r.finish(platform, job, e, a, false)
	case run.committing:
		release()
		return r.verifyBatch(ctx, job, e, a, err, false)
	}
	return r.stopBatch(platform, job, e, a, classify(err))
}

// execute memeriksa tabel, menjalankan batch satu per satu, lalu menulis bukti selesai
func (b *batchRun) execute(ctx context.Context, lease contract.Lease, root int64) error {
	var cursor, scopeMax int64
	finished := false
	err := b.tx(ctx, func(tx dbdriver.ArgTx) error {
		if err := pgmeta.Ensure(ctx, tx); err != nil {
			return err
		}
		if err := checkProofOwner(ctx, tx, b.e, b.chain, b.since); err != nil {
			return err
		}
		if err := checkBatchKey(ctx, tx, b.table, b.e.Batch.KeyColumn); err != nil {
			return err
		}
		var err error
		// percobaan ulang melanjutkan hitungan dari checkpoint yang sudah ada
		if b.attempt.Batches, b.attempt.Rows, err = ownProgress(ctx, tx, b.e); err != nil {
			return err
		}
		if b.e.Kind == domain.KindRollback {
			return nil
		}
		cursor, scopeMax, finished, err = b.scope(ctx, tx, root)
		return err
	})
	if err != nil {
		return err
	}
	b.attempt.SQLExecuted = b.e.SQLSnapshot

	for !finished {
		if err := ctx.Err(); err != nil || lease.Lost() {
			return errors.Join(errors.New("batch dihentikan"), err)
		}
		if b.e.Kind == domain.KindRollback {
			finished, err = b.revertNext(ctx)
		} else {
			finished, err = b.forwardNext(ctx, &cursor, scopeMax)
		}
		if err != nil {
			return err
		}
	}
	return b.tx(ctx, func(tx dbdriver.ArgTx) error {
		return pgmeta.RecordExecution(ctx, tx, pgmeta.Execution{
			ID: b.e.ID, Kind: string(b.e.Kind), Attempt: b.job.Attempt, Checksum: b.e.Checksum,
		})
	})
}

// scope menentukan batas akhir sekali di awal, jadi baris yang masuk belakangan tidak ikut diproses
func (b *batchRun) scope(ctx context.Context, tx dbdriver.ArgTx, root int64) (cursor, scopeMax int64, empty bool, err error) {
	cursor, resumed, err := pgmeta.ResumeCursor(ctx, tx, b.chain)
	if err != nil {
		return 0, 0, false, err
	}
	// resume mulai dari batch terakhir yang tercommit supaya tidak ada yang diulang
	if resumed {
		scopeMax, found, err := pgmeta.Scope(ctx, tx, root)
		if err == nil && !found {
			err = errors.New("batas akhir batch tidak ditemukan di database target")
		}
		return cursor, scopeMax, false, err
	}
	start, scopeMax, empty, err := b.table.bounds(ctx, tx)
	if err != nil || empty {
		return 0, 0, empty, err
	}
	// batas yang tersisa tanpa satu pun checkpoint tidak berarti apa-apa, jadi diganti
	_, err = tx.ExecArgs(ctx, `DELETE FROM `+pgmeta.Schema+`.batch_scopes WHERE execution_id = $1`, root)
	if err != nil {
		return 0, 0, false, err
	}
	return start, scopeMax, false, pgmeta.SetScope(ctx, tx, root, scopeMax)
}

// forwardNext menjalankan satu batch dan checkpointnya dalam satu transaction, done true kalau sudah tidak ada baris
func (b *batchRun) forwardNext(ctx context.Context, cursor *int64, scopeMax int64) (done bool, err error) {
	var step pgmeta.Batch
	batchCtx := b.batchContext(ctx)
	err = b.tx(batchCtx, func(tx dbdriver.ArgTx) error {
		ctx := batchCtx
		hi, found, err := b.table.next(ctx, tx, *cursor, scopeMax, b.e.Batch.BatchSize)
		if err != nil || !found {
			done = !found
			return err
		}
		step, err = b.apply(ctx, tx, pgmeta.Forward, *cursor, hi)
		return err
	})
	if err != nil || done {
		return done, err
	}
	*cursor = step.HiKey
	return false, b.committed(ctx, step, scopeMax)
}

// revertNext membatalkan batch dari yang paling besar ke yang paling kecil
func (b *batchRun) revertNext(ctx context.Context) (done bool, err error) {
	var step pgmeta.Batch
	batchCtx := b.batchContext(ctx)
	err = b.tx(batchCtx, func(tx dbdriver.ArgTx) error {
		ctx := batchCtx
		lo, hi, found, err := nextUnreverted(ctx, tx, b.chain)
		if err != nil || !found {
			done = !found
			return err
		}
		step, err = b.apply(ctx, tx, pgmeta.Revert, lo, hi)
		return err
	})
	if err != nil || done {
		return done, err
	}
	return false, b.committed(ctx, step, 0)
}

// batchContext menambahkan nomor batch yang sedang dikerjakan ke log SQLnya
func (b *batchRun) batchContext(ctx context.Context) context.Context {
	return telemetry.WithLogAttrs(ctx, "batch", b.attempt.Batches+1)
}

func (b *batchRun) apply(ctx context.Context, tx dbdriver.ArgTx, direction string, lo, hi int64) (pgmeta.Batch, error) {
	rows, err := tx.ExecArgs(ctx, b.e.SQLSnapshot, lo, hi)
	if err != nil {
		return pgmeta.Batch{}, err
	}
	step := pgmeta.Batch{
		ExecutionID: b.e.ID, BatchNo: b.attempt.Batches + 1, Direction: direction,
		LoKey: lo, HiKey: hi, RowsAffected: rows,
	}
	return step, pgmeta.RecordBatch(ctx, tx, step)
}

func (b *batchRun) tx(ctx context.Context, fn func(tx dbdriver.ArgTx) error) error {
	b.committing = false
	return b.session.WithTx(ctx, dbdriver.TxOptions{Timeout: batchTimeout}, func(tx dbdriver.ArgTx) error {
		err := fn(tx)
		b.committing = err == nil
		return err
	})
}

// committed mencatat kemajuan setelah satu batch tercommit
func (b *batchRun) committed(ctx context.Context, step pgmeta.Batch, scopeMax int64) error {
	b.attempt.Batches++
	b.attempt.Rows += step.RowsAffected
	b.runner.deps.Logger.InfoContext(ctx, "batch committed",
		"event", "migration.batch_committed",
		"batch", step.BatchNo, "direction", step.Direction, "lo", step.LoKey, "hi", step.HiKey, "rows", step.RowsAffected)

	err := b.runner.deps.Store.SaveProgress(context.WithoutCancel(ctx), b.job, b.e.ID, Progress{
		Batches: b.attempt.Batches, Rows: b.attempt.Rows, Cursor: step.HiKey, ScopeMax: scopeMax,
	})
	if errors.Is(err, ErrStaleFence) {
		return err
	}
	// progres di platform hanya untuk tampilan, sumber kebenarannya checkpoint di target
	if err != nil {
		b.runner.deps.Logger.WarnContext(ctx, "save progress failed",
			"event", "migration.progress_failed", "error", err)
	}
	return nil
}

// stopBatch mencatat pekerjaan yang berhenti, kalau sudah ada batch tercommit statusnya setengah jalan dan tidak diulang otomatis
func (r *Runner) stopBatch(ctx context.Context, job domain.Job, e domain.Execution, a Attempt, f *Failure) domain.RunResult {
	if a.Batches == 0 {
		return r.fail(ctx, job, e, a, f)
	}
	a.Outcome = domain.OutcomePartial
	if e.Kind == domain.KindRollback {
		a.Outcome = domain.OutcomePartiallyReverted
	}
	a.Committed, a.FailureCode, a.FailureMessage = true, f.Code, f.Error()
	return r.finish(ctx, job, e, a, false)
}

// verifyBatch menghitung ulang dari checkpoint di target saat hasil pekerjaan tidak pasti
func (r *Runner) verifyBatch(
	ctx context.Context, job domain.Job, e domain.Execution, a Attempt, cause error, interrupted bool,
) domain.RunResult {
	platform := context.WithoutCancel(ctx)
	unknown := func(err error) domain.RunResult {
		a.Outcome, a.FailureCode = domain.OutcomeUnknown, codeUncertain
		a.FailureMessage = errors.Join(cause, err).Error()
		return r.finish(platform, job, e, a, false)
	}
	driver, err := r.deps.Drivers.Get(ctx, e.ConnectionID)
	if err != nil {
		return unknown(err)
	}
	sqlCtx := targetContext(ctx, job, e)
	session, locked, err := lockTarget(sqlCtx, driver)
	if err != nil {
		return unknown(err)
	}
	if !locked {
		return later(domain.ErrDatabaseBusy)
	}
	defer session.Close()

	var done bool
	err = session.WithTx(sqlCtx, dbdriver.TxOptions{}, func(tx dbdriver.ArgTx) error {
		ctx := sqlCtx
		if err := pgmeta.Ensure(ctx, tx); err != nil {
			return err
		}
		if done, err = ownProof(ctx, tx, e); err != nil {
			return err
		}
		a.Batches, a.Rows, err = ownProgress(ctx, tx, e)
		return err
	})
	if err != nil {
		return unknown(err)
	}
	if done {
		a.Outcome, a.Committed = domain.OutcomeSuccess, true
		return r.finish(platform, job, e, a, false)
	}
	failure := classify(cause)
	if interrupted {
		failure = transient(CodeConnection, cause)
	}
	return r.stopBatch(platform, job, e, a, failure)
}
