package migration

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgconn"

	"dbplatform/internal/contract"
	"dbplatform/internal/dbdriver"
	"dbplatform/internal/dbdriver/pgmeta"
	"dbplatform/internal/domain"
)

const (
	codeSQL       = "sql_error"
	codeUncertain = "outcome_unknown"
)

var transientStates = map[string]bool{"40001": true, "40P01": true, "57P03": true, "53300": true}

// runNormal menjalankan seluruh SQL dalam satu transaction sambil memegang kunci database target
func (r *Runner) runNormal(
	lease contract.Lease, job domain.Job, e domain.Execution, driver dbdriver.Driver, a Attempt,
) domain.RunResult {
	ctx := lease.Context()
	platform := context.WithoutCancel(ctx)

	session, locked, err := lockTarget(ctx, driver)
	if err != nil {
		return r.fail(platform, job, e, a, transient(CodeConnection, err))
	}
	if !locked {
		return later(domain.ErrDatabaseBusy)
	}
	// Close tidak boleh dipanggil dua kali, padahal kadang harus dilepas lebih awal
	release := sync.OnceFunc(session.Close)
	defer release()

	if err := r.deps.Store.MarkRunning(platform, job, e.ID, a.StartedAt); err != nil {
		return later(err)
	}
	r.deps.Logger.InfoContext(ctx, "migration started",
		"event", "migration.started", "actor", e.RequestedByName, "connection_id", e.ConnectionID,
		"execution_id", e.ID, "job_id", job.ID, "attempt", job.Attempt)

	opts := dbdriver.TxOptions{Timeout: r.deps.Timeout}
	committing := false // true kalau semua perintah berhasil dan tinggal commit
	err = session.WithTx(ctx, opts, func(tx dbdriver.ArgTx) error {
		if err := pgmeta.Ensure(ctx, tx); err != nil {
			return err
		}
		done, err := pgmeta.ExecutionCommitted(ctx, tx, e.ID)
		if err != nil || done {
			// done berarti sudah pernah tercommit, SQL tidak dijalankan lagi
			return err
		}
		a.SQLExecuted = e.SQLSnapshot
		if _, err := tx.Exec(ctx, e.SQLSnapshot); err != nil {
			return err
		}
		// bukti ditulis di transaction yang sama, jadi perubahan dan buktinya tercommit bersama atau batal bersama
		err = pgmeta.RecordExecution(ctx, tx, pgmeta.Execution{
			ID: e.ID, Kind: string(e.Kind), Attempt: job.Attempt, Checksum: e.Checksum,
		})
		committing = err == nil
		return err
	})

	switch {
	case lease.Lost():
		// job sudah bukan milik worker ini, biarkan reaper yang menyelesaikan
		return later(errors.New("lease hilang saat menjalankan migration"))
	case err == nil:
		a.Outcome, a.Committed = domain.OutcomeSuccess, true
		return r.finish(platform, job, e, a, false)
	case committing:
		// errornya muncul saat commit, jadi belum tentu perubahannya batal
		release()
		return r.verify(ctx, job, e, a, err, false)
	}
	failure := rejected(codeSQL, "%w", err)
	if isTransient(err) {
		failure = transient(CodeConnection, err)
	}
	return r.fail(platform, job, e, a, failure)
}

// lockTarget mengambil satu koneksi khusus lalu mencoba kunci advisory platform tanpa menunggu
func lockTarget(ctx context.Context, driver dbdriver.Driver) (dbdriver.Session, bool, error) {
	sessioner, ok := driver.(dbdriver.Sessioner)
	if !ok {
		return nil, false, domain.ErrUnsupported
	}
	session, err := sessioner.OpenSession(ctx)
	if err != nil {
		return nil, false, err
	}
	locked, err := session.TryLock(ctx, dbdriver.PlatformMigrationLockKey)
	if err != nil || !locked {
		session.Close()
		return nil, false, err
	}
	return session, true, nil
}

// verify mencari bukti di database target saat hasil sebuah attempt tidak pasti
func (r *Runner) verify(
	ctx context.Context, job domain.Job, e domain.Execution, a Attempt, cause error, interrupted bool,
) domain.RunResult {
	platform := context.WithoutCancel(ctx)
	committed, locked, err := r.findProof(ctx, e)
	switch {
	case err != nil:
		a.Outcome, a.FailureCode = domain.OutcomeUnknown, codeUncertain
		a.FailureMessage = errors.Join(cause, err).Error()
		return r.finish(platform, job, e, a, false)
	case !locked:
		// koneksi lama masih memegang kunci, hasilnya baru bisa dipastikan setelah ia selesai
		return later(domain.ErrDatabaseBusy)
	case committed:
		a.Outcome, a.Committed = domain.OutcomeSuccess, true
		return r.finish(platform, job, e, a, false)
	}
	// kunci bisa diambil dan buktinya tidak ada, berarti pasti tidak tercommit
	failure := rejected(codeSQL, "%w", cause)
	if interrupted || isTransient(cause) {
		failure = transient(CodeConnection, cause)
	}
	return r.fail(platform, job, e, a, failure)
}

func (r *Runner) findProof(ctx context.Context, e domain.Execution) (committed, locked bool, err error) {
	driver, err := r.deps.Drivers.Get(ctx, e.ConnectionID)
	if err != nil {
		return false, false, err
	}
	session, locked, err := lockTarget(ctx, driver)
	if err != nil || !locked {
		return false, locked, err
	}
	defer session.Close()

	err = session.WithTx(ctx, dbdriver.TxOptions{}, func(tx dbdriver.ArgTx) error {
		// tabel bukti dibuat kalau belum ada supaya pencariannya tidak error
		if err := pgmeta.Ensure(ctx, tx); err != nil {
			return err
		}
		committed, err = pgmeta.ExecutionCommitted(ctx, tx, e.ID)
		return err
	})
	return committed, true, err
}

// isTransient true untuk gangguan yang bisa hilang sendiri, kesalahan SQL biasa tidak termasuk
func isTransient(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return strings.HasPrefix(pgErr.Code, "08") || transientStates[pgErr.Code]
	}
	// batas waktu eksekusi habis tidak diulang karena kemungkinan besar habis lagi
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	var netErr net.Error
	return errors.As(err, &netErr) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, domain.ErrConnectionFailed)
}
