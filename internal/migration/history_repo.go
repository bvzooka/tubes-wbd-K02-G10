package migration

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"dbplatform/internal/appdb"
	"dbplatform/internal/domain"
)

// ErrStaleFence berarti job sudah diambil alih worker lain, jadi hasil worker ini tidak boleh ditulis
var ErrStaleFence = errors.New("migration: job sudah dipegang worker lain")

// RecordAttempt menulis riwayat, status permintaan, dan status migration dalam satu transaction
func (r *Repo) RecordAttempt(ctx context.Context, job domain.Job, e domain.Execution, a Attempt) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if err := holdFence(ctx, tx, job); err != nil {
		return err
	}
	result := settle(e, a)
	if err := insertHistory(ctx, tx, e, a, result.history); err != nil {
		return err
	}
	if err := saveProgress(ctx, tx, e.ID, a, result.execution); err != nil {
		return err
	}
	if result.state != "" {
		_, err := tx.Exec(ctx, `UPDATE db_migrations SET state = $2, updated_at = $3 WHERE id = $1`,
			e.MigrationID, result.state, a.FinishedAt)
		if err != nil {
			return fmt.Errorf("set migration state: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit attempt: %w", err)
	}
	return nil
}

// MarkRunning menandai permintaan sedang dijalankan oleh pemegang job yang sah
func (r *Repo) MarkRunning(ctx context.Context, job domain.Job, executionID int64, at time.Time) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if err := holdFence(ctx, tx, job); err != nil {
		return err
	}
	if err := r.SetExecutionStatus(ctx, tx, executionID, domain.ExecQueued, domain.ExecRunning, at); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit running: %w", err)
	}
	return nil
}

// holdFence mengunci baris job sampai commit dan memastikan nomor fence belum berubah
func holdFence(ctx context.Context, q appdb.DBTX, job domain.Job) error {
	var fence int64
	err := q.QueryRow(ctx, `SELECT fence FROM migration_jobs WHERE id = $1 FOR UPDATE`, job.ID).Scan(&fence)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("check fence: %w", err)
	}
	if fence != job.Fence {
		return ErrStaleFence
	}
	return nil
}

func insertHistory(ctx context.Context, q appdb.DBTX, e domain.Execution, a Attempt, status string) error {
	_, err := q.Exec(ctx, `
		INSERT INTO migration_history (
			execution_id, attempt, migration_id, connection_id, version, revision, kind, executed_by, status,
			committed, sql_executed, rolls_back_execution_id, batches_committed, rows_committed,
			failure_code, failure_message, correlation_id, trace_id, started_at, finished_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)`,
		e.ID, a.Number, e.MigrationID, e.ConnectionID, e.Version, e.Revision, e.Kind, e.RequestedBy, status,
		a.Committed, a.SQLExecuted, e.RollsBackExecutionID, a.Batches, a.Rows,
		a.FailureCode, a.FailureMessage, e.CorrelationID, a.TraceID, a.StartedAt, a.FinishedAt)
	if err != nil {
		return fmt.Errorf("insert history: %w", err)
	}
	return nil
}

func saveProgress(ctx context.Context, q appdb.DBTX, executionID int64, a Attempt, status domain.ExecutionStatus) error {
	_, err := q.Exec(ctx, `
		UPDATE migration_executions
		SET status = $2::text, failure_code = $3, failure_message = $4, batches_committed = $5, rows_committed = $6,
		    started_at  = COALESCE(started_at, $7),
		    finished_at = CASE WHEN $2::text = 'queued' THEN NULL ELSE $8::timestamptz END
		WHERE id = $1`,
		executionID, status, a.FailureCode, a.FailureMessage, a.Batches, a.Rows, a.StartedAt, a.FinishedAt)
	if err != nil {
		return fmt.Errorf("save execution progress: %w", err)
	}
	return nil
}

// ListHistory mengurutkan dari yang paling lama, satu baris per attempt
func (r *Repo) ListHistory(ctx context.Context, connID int64, f HistoryFilter, limit, offset int) ([]domain.HistoryEntry, error) {
	// hide rollback menyembunyikan rollback yang sukses dan eksekusi yang dibatalkannya, pasangannya lewat ID bukan nama
	rows, err := r.db.Query(ctx, `
		WITH undone AS (
			SELECT rolls_back_execution_id AS id
			FROM migration_history
			WHERE connection_id = $1 AND kind = 'rollback' AND status = 'success'
		)
		SELECT h.id, h.execution_id, h.migration_id, h.connection_id, h.attempt, h.version, h.revision, h.kind,
		       h.executed_by, u.username, h.status, h.committed, h.sql_executed, h.rolls_back_execution_id,
		       h.batches_committed, h.rows_committed, h.failure_code, h.failure_message, h.correlation_id,
		       h.trace_id, h.started_at, h.finished_at
		FROM migration_history h
		JOIN users u ON u.id = h.executed_by
		WHERE h.connection_id = $1
		  AND ($2::bigint = 0 OR h.execution_id = $2)
		  AND ($3::text = '' OR h.status = $3)
		  AND NOT ($4::boolean AND h.status <> 'failed' AND (
		        (h.kind = 'rollback' AND h.status = 'success')
		        OR h.execution_id IN (SELECT id FROM undone WHERE id IS NOT NULL)))
		ORDER BY h.id
		LIMIT $5 OFFSET $6`,
		connID, f.ExecutionID, f.Status, f.HideRollback, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list history: %w", err)
	}
	defer rows.Close()

	var list []domain.HistoryEntry
	for rows.Next() {
		var h domain.HistoryEntry
		if err := rows.Scan(
			&h.ID, &h.ExecutionID, &h.MigrationID, &h.ConnectionID, &h.Attempt, &h.Version, &h.Revision, &h.Kind,
			&h.ExecutedBy, &h.ExecutedByName, &h.Status, &h.Committed, &h.SQLExecuted, &h.RollsBackExecutionID,
			&h.BatchesCommitted, &h.RowsCommitted, &h.FailureCode, &h.FailureMessage, &h.CorrelationID,
			&h.TraceID, &h.StartedAt, &h.FinishedAt,
		); err != nil {
			return nil, fmt.Errorf("scan history: %w", err)
		}
		list = append(list, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read history: %w", err)
	}
	return list, nil
}
