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

const selectExecution = `
	SELECT e.id, e.migration_id, e.connection_id, e.version, e.revision, e.kind, e.mode, e.batch_config,
	       e.sql_snapshot, e.checksum, e.rolls_back_execution_id, e.resumes_execution_id, e.root_execution_id,
	       e.status, e.requested_by, u.username, e.requested_at, e.queued_at, e.started_at, e.finished_at,
	       e.failure_code, e.failure_message, e.scope_max, e.cursor_value, e.batches_committed,
	       e.rows_committed, e.correlation_id, e.legacy
	FROM migration_executions e
	JOIN users u ON u.id = e.requested_by`

// urutan Scan harus sama persis dengan urutan kolom di query
func scanExecution(row pgx.Row) (domain.Execution, error) {
	var e domain.Execution
	err := row.Scan(
		&e.ID, &e.MigrationID, &e.ConnectionID, &e.Version, &e.Revision, &e.Kind, &e.Mode, &e.Batch,
		&e.SQLSnapshot, &e.Checksum, &e.RollsBackExecutionID, &e.ResumesExecutionID, &e.RootExecutionID,
		&e.Status, &e.RequestedBy, &e.RequestedByName, &e.RequestedAt, &e.QueuedAt, &e.StartedAt, &e.FinishedAt,
		&e.FailureCode, &e.FailureMessage, &e.ScopeMax, &e.CursorValue, &e.BatchesCommitted,
		&e.RowsCommitted, &e.CorrelationID, &e.Legacy,
	)
	return e, err
}

// oneExecution dipakai semua query yang hasilnya paling banyak satu baris
func oneExecution(ctx context.Context, q appdb.DBTX, where string, args ...any) (domain.Execution, error) {
	e, err := scanExecution(q.QueryRow(ctx, selectExecution+" "+where, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Execution{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Execution{}, fmt.Errorf("read execution: %w", err)
	}
	return e, nil
}

// InConnectionTx menjalankan fn dalam satu transaction yang memegang kunci database itu, jadi pengecekan aturan dan penulisannya tidak bisa disela
func (r *Repo) InConnectionTx(ctx context.Context, connID int64, fn func(q appdb.DBTX) error) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if err := lockMigrations(ctx, tx, connID); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// DefinitionIn membaca migration dari dalam transaction yang sedang jalan
func (r *Repo) DefinitionIn(ctx context.Context, q appdb.DBTX, connID int64, version int) (Definition, error) {
	return getDefinition(ctx, q, connID, version)
}

// VersionStates mengurutkan dari versi terkecil karena checkOrder melaporkan penghalang pertama
func (r *Repo) VersionStates(ctx context.Context, q appdb.DBTX, connID int64) ([]versionState, error) {
	rows, err := q.Query(ctx, `
		SELECT version, state
		FROM db_migrations
		WHERE connection_id = $1
		ORDER BY version`, connID)
	if err != nil {
		return nil, fmt.Errorf("list version states: %w", err)
	}
	states, err := pgx.CollectRows(rows, pgx.RowToStructByPos[versionState])
	if err != nil {
		return nil, fmt.Errorf("read version states: %w", err)
	}
	return states, nil
}

// InsertExecution menyimpan permintaan baru berstatus pending_review beserta salinan SQL yang akan disetujui
func (r *Repo) InsertExecution(ctx context.Context, q appdb.DBTX, e domain.Execution) (domain.Execution, error) {
	var id int64
	err := q.QueryRow(ctx, `
		INSERT INTO migration_executions (
			migration_id, connection_id, version, revision, kind, mode, batch_config, sql_snapshot, checksum,
			rolls_back_execution_id, resumes_execution_id, root_execution_id, requested_by, requested_at, correlation_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING id`,
		e.MigrationID, e.ConnectionID, e.Version, e.Revision, e.Kind, e.Mode, e.Batch, e.SQLSnapshot, e.Checksum,
		e.RollsBackExecutionID, e.ResumesExecutionID, e.RootExecutionID, e.RequestedBy, e.RequestedAt, e.CorrelationID,
	).Scan(&id)
	if err != nil {
		return domain.Execution{}, fmt.Errorf("insert execution: %w", err)
	}
	return r.ExecutionIn(ctx, q, id)
}

func (r *Repo) GetExecution(ctx context.Context, id int64) (domain.Execution, error) {
	return r.ExecutionIn(ctx, r.db, id)
}

func (r *Repo) ExecutionIn(ctx context.Context, q appdb.DBTX, id int64) (domain.Execution, error) {
	return oneExecution(ctx, q, `WHERE e.id = $1`, id)
}

// LatestForwardExecution mencari apply atau forward terakhir dengan salah satu status itu, itu yang akan dibatalkan atau dilanjutkan
func (r *Repo) LatestForwardExecution(ctx context.Context, q appdb.DBTX, migrationID int64, statuses ...domain.ExecutionStatus) (domain.Execution, error) {
	return oneExecution(ctx, q, `
		WHERE e.migration_id = $1 AND e.status = ANY($2) AND e.kind IN ('apply', 'forward')
		ORDER BY e.id DESC
		LIMIT 1`, migrationID, statuses)
}

// HasOpenExecution membuka pengecekan permintaan yang belum selesai untuk service
func (r *Repo) HasOpenExecution(ctx context.Context, q appdb.DBTX, migrationID int64) (bool, error) {
	return hasOpenExecution(ctx, q, migrationID)
}

// SetExecutionStatus hanya mengubah kalau status asalnya masih sama, jadi dua klik bersamaan tidak sama-sama berhasil
func (r *Repo) SetExecutionStatus(ctx context.Context, q appdb.DBTX, id int64, from, to domain.ExecutionStatus, at time.Time) error {
	tag, err := q.Exec(ctx, `
		UPDATE migration_executions
		SET status = $3::text,
		    approved_at = CASE WHEN $3::text = 'approved' THEN $4::timestamptz ELSE approved_at END,
		    queued_at   = CASE WHEN $3::text = 'queued' THEN $4::timestamptz ELSE queued_at END,
		    finished_at = CASE WHEN $3::text = 'cancelled' THEN $4::timestamptz ELSE finished_at END
		WHERE id = $1 AND status = $2`,
		id, from, to, at)
	if err != nil {
		return fmt.Errorf("set execution status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return refuse(domain.ErrConflict, "Status permintaan sudah berubah, muat ulang halamannya.")
	}
	return nil
}

// JobID mencari tiket antrean milik satu permintaan
func (r *Repo) JobID(ctx context.Context, q appdb.DBTX, executionID int64) (string, error) {
	var id string
	err := q.QueryRow(ctx, `SELECT id::text FROM migration_jobs WHERE execution_id = $1`, executionID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("find job: %w", err)
	}
	return id, nil
}
