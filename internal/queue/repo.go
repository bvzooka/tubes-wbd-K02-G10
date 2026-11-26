// Package queue adalah antrean job migration di database platform beserta worker-nya.
package queue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/appdb"
	"dbplatform/internal/clock"
	"dbplatform/internal/domain"
	"dbplatform/internal/telemetry"
)

// ErrLeaseLost: penulisan ditolak karena fence sudah dipakai worker lain.
var ErrLeaseLost = errors.New("queue: lease lost")

const defaultMaxAttempts = 3

const jobColumns = `id, execution_id, connection_id, state, attempt, max_attempts, run_after,
	fence, traceparent, correlation_id, last_error`

const lockWaitSQL = `GREATEST(0, EXTRACT(EPOCH FROM LEAST($2::timestamptz, COALESCE(
		(SELECT max(GREATEST(o.finished_at, o.heartbeat_at)) FROM migration_jobs o
		 WHERE o.connection_id = j.connection_id AND o.id <> j.id), j.run_after)) - j.run_after))::float8`

type Repo struct {
	pool  *pgxpool.Pool
	clock clock.Clock
}

func NewRepo(pool *pgxpool.Pool, clk clock.Clock) *Repo {
	return &Repo{pool: pool, clock: clk}
}

// Enqueue idempoten memanggil dua kali untuk eksekusi yang sama mengembalikan job yang sama
func (r *Repo) Enqueue(ctx context.Context, tx appdb.DBTX, in domain.EnqueueInput) (domain.Job, error) {
	if in.Traceparent == "" {
		in.Traceparent = Traceparent(ctx)
	}
	if in.CorrelationID == "" {
		in.CorrelationID = telemetry.CorrelationID(ctx)
	}
	if in.MaxAttempts <= 0 {
		in.MaxAttempts = defaultMaxAttempts
	}
	return scanJob(tx.QueryRow(ctx, `
		INSERT INTO migration_jobs (execution_id, connection_id, max_attempts, traceparent, correlation_id, run_after, enqueued_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
		ON CONFLICT (execution_id) DO UPDATE SET execution_id = EXCLUDED.execution_id
		RETURNING `+jobColumns,
		in.ExecutionID, in.ConnectionID, in.MaxAttempts, in.Traceparent, in.CorrelationID, r.clock.Now()))
}

func (r *Repo) Get(ctx context.Context, jobID string) (domain.Job, error) {
	return scanJob(r.pool.QueryRow(ctx, `SELECT `+jobColumns+` FROM migration_jobs WHERE id = $1::uuid`, jobID))
}

// Claim mengambil satu job siap jalan sekaligus mengunci koneksinya dalam satu
// transaction. Koneksi yang masih punya job running atau lock aktif dilewati, jadi satu
// database hanya menjalankan satu job pada satu waktu.
func (r *Repo) Claim(ctx context.Context, owner string, lease time.Duration, now time.Time) (domain.Job, bool, error) {
	var job domain.Job
	var waited float64
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		var err error
		job, err = scanJob(tx.QueryRow(ctx, `
			WITH next AS (
				SELECT j.id AS next_id FROM migration_jobs j
				WHERE j.state = 'queued' AND j.run_after <= $2
				  AND NOT EXISTS (SELECT 1 FROM migration_jobs r
				                  WHERE r.connection_id = j.connection_id AND r.state = 'running')
				  AND NOT EXISTS (SELECT 1 FROM connection_locks l
				                  WHERE l.connection_id = j.connection_id AND l.expires_at > $2)
				ORDER BY j.enqueued_at
				LIMIT 1
				FOR UPDATE SKIP LOCKED
			)
			UPDATE migration_jobs j
			SET state = 'running', attempt = attempt + 1, fence = nextval('connection_lock_fence_seq'),
			    lease_owner = $1, lease_expires_at = $2::timestamptz + $3::interval, heartbeat_at = $2,
			    started_at = COALESCE(started_at, $2)
			FROM next WHERE j.id = next.next_id
			RETURNING `+jobColumns+`, `+lockWaitSQL, owner, now, lease), &waited)
		if err != nil {
			return err
		}
		return acquireLock(ctx, tx, job, owner, lease, now, 0)
	})
	if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrDatabaseBusy) {
		return domain.Job{}, false, nil
	}
	if err != nil {
		return domain.Job{}, false, fmt.Errorf("claim job: %w", err)
	}
	lockWait.Observe(waited)
	return job, true, nil
}

// TakeOver dipakai reaper untuk mengambil alih job running yang lease-nya habis. Fence baru membuat semua penulisan worker lama ditolak.
func (r *Repo) TakeOver(ctx context.Context, stale domain.Job, owner string, lease time.Duration, now time.Time) (domain.Job, bool, error) {
	var job domain.Job
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		var err error
		job, err = scanJob(tx.QueryRow(ctx, `
			UPDATE migration_jobs
			SET fence = nextval('connection_lock_fence_seq'), lease_owner = $3,
			    lease_expires_at = $4::timestamptz + $5::interval, heartbeat_at = $4
			WHERE id = $1::uuid AND fence = $2 AND state = 'running' AND lease_expires_at < $4
			RETURNING `+jobColumns, stale.ID, stale.Fence, owner, now, lease))
		if err != nil {
			return err
		}
		return acquireLock(ctx, tx, job, owner, lease, now, stale.Fence)
	})
	if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrDatabaseBusy) {
		return domain.Job{}, false, nil
	}
	if err != nil {
		return domain.Job{}, false, fmt.Errorf("take over job: %w", err)
	}
	return job, true, nil
}

// Expired mengembalikan job running yang lease dan lock koneksinya sudah kedaluwarsa.
func (r *Repo) Expired(ctx context.Context, now time.Time) ([]domain.Job, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+jobColumns+`
		FROM migration_jobs j
		WHERE state = 'running' AND lease_expires_at < $1
		  AND NOT EXISTS (SELECT 1 FROM connection_locks l
		                  WHERE l.connection_id = j.connection_id AND l.expires_at >= $1)
		ORDER BY lease_expires_at`, now)
	if err != nil {
		return nil, fmt.Errorf("expired jobs: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Job, error) { return scanJob(row) })
}

// Heartbeat memperpanjang lease job dan lock koneksi dalam satu statement. false berarti fence sudah tidak cocok: lease hilang.
func (r *Repo) Heartbeat(ctx context.Context, job domain.Job, lease time.Duration, now time.Time) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		WITH j AS (
			UPDATE migration_jobs SET lease_expires_at = $3::timestamptz + $4::interval, heartbeat_at = $3
			WHERE id = $1::uuid AND fence = $2 AND state = 'running'
			RETURNING connection_id
		)
		UPDATE connection_locks l SET expires_at = $3::timestamptz + $4::interval, heartbeat_at = $3
		FROM j WHERE l.connection_id = j.connection_id AND l.fence = $2`,
		job.ID, job.Fence, now, lease)
	if err != nil {
		return false, fmt.Errorf("heartbeat: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Finish menutup job (done) dan melepas lock koneksinya.
func (r *Repo) Finish(ctx context.Context, job domain.Job, lastErr string, now time.Time) error {
	return r.release(ctx, job, `
		UPDATE migration_jobs
		SET state = 'done', finished_at = $3, last_error = $4, lease_owner = '', lease_expires_at = NULL
		WHERE id = $1::uuid AND fence = $2 AND state = 'running'`,
		job.ID, job.Fence, now, lastErr)
}

// Reschedule mengembalikan job ke antrean. countAttempt=false dipakai bila job belum sempat dijalankan (target sibuk), sehingga jatah attempt tidak berkurang.
func (r *Repo) Reschedule(ctx context.Context, job domain.Job, runAfter time.Time, lastErr string, countAttempt bool) error {
	return r.release(ctx, job, `
		UPDATE migration_jobs
		SET state = 'queued', run_after = $3, last_error = $4, lease_owner = '', lease_expires_at = NULL,
		    attempt = CASE WHEN $5 THEN attempt ELSE attempt - 1 END
		WHERE id = $1::uuid AND fence = $2 AND state = 'running'`,
		job.ID, job.Fence, runAfter, lastErr, countAttempt)
}

func (r *Repo) release(ctx context.Context, job domain.Job, update string, args ...any) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, update, args...)
		if err != nil {
			return fmt.Errorf("update job: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrLeaseLost
		}
		_, err = tx.Exec(ctx, `DELETE FROM connection_locks WHERE connection_id = $1 AND fence = $2`,
			job.ConnectionID, job.Fence)
		if err != nil {
			return fmt.Errorf("release lock: %w", err)
		}
		return nil
	})
}

// acquireLock mengambil lock koneksi bila kosong, sudah kedaluwarsa, atau masih milik
// fence lama yang sedang diambil alih (staleFence).
func acquireLock(ctx context.Context, tx pgx.Tx, job domain.Job, owner string, lease time.Duration, now time.Time, staleFence int64) error {
	var fence int64
	err := tx.QueryRow(ctx, `
		INSERT INTO connection_locks (connection_id, job_id, owner, fence, acquired_at, heartbeat_at, expires_at)
		VALUES ($1, $2::uuid, $3, $4, $5, $5, $5::timestamptz + $6::interval)
		ON CONFLICT (connection_id) DO UPDATE
		SET job_id = EXCLUDED.job_id, owner = EXCLUDED.owner, fence = EXCLUDED.fence,
		    acquired_at = EXCLUDED.acquired_at, heartbeat_at = EXCLUDED.heartbeat_at, expires_at = EXCLUDED.expires_at
		WHERE connection_locks.expires_at <= $5 OR connection_locks.fence = $7
		RETURNING fence`,
		job.ConnectionID, job.ID, owner, job.Fence, now, lease, staleFence).Scan(&fence)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrDatabaseBusy
	}
	if err != nil {
		return fmt.Errorf("acquire connection lock: %w", err)
	}
	return nil
}

// extra menampung kolom tambahan setelah jobColumns
func scanJob(row pgx.Row, extra ...any) (domain.Job, error) {
	var j domain.Job
	dest := append([]any{&j.ID, &j.ExecutionID, &j.ConnectionID, &j.State, &j.Attempt, &j.MaxAttempts,
		&j.RunAfter, &j.Fence, &j.Traceparent, &j.CorrelationID, &j.LastError}, extra...)
	err := row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, domain.ErrNotFound
	}
	if err != nil {
		return j, fmt.Errorf("scan job: %w", err)
	}
	return j, nil
}
