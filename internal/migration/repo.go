package migration

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"dbplatform/internal/domain"
)

type DB interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type Repo struct{ db DB }

func NewRepo(db DB) *Repo { return &Repo{db: db} }

const foreignKeyViolation = "23503"

const selectMigration = `
	SELECT m.id, m.connection_id, m.version, m.name, %s, m.checksum, m.status, m.error_message, m.warning,
	       m.created_by, cu.username, m.created_at, m.updated_at,
	       m.executed_by, COALESCE(eu.username, ''), m.executed_at, m.finished_at
	FROM db_migrations m
	JOIN users cu ON cu.id = m.created_by
	LEFT JOIN users eu ON eu.id = m.executed_by`

var (
	selectFull    = fmt.Sprintf(selectMigration, "m.sql_text")
	selectSummary = fmt.Sprintf(selectMigration, "''::text")
)

func scanMigration(row pgx.Row) (domain.Migration, error) {
	var m domain.Migration
	var status string
	err := row.Scan(
		&m.ID, &m.ConnectionID, &m.Version, &m.Name, &m.SQL, &m.Checksum, &status, &m.ErrorMessage, &m.Warning,
		&m.CreatedBy, &m.CreatedByName, &m.CreatedAt, &m.UpdatedAt,
		&m.ExecutedBy, &m.ExecutedByName, &m.ExecutedAt, &m.FinishedAt,
	)
	m.Status = domain.MigrationStatus(status)
	return m, err
}

func lockMigrations(ctx context.Context, tx pgx.Tx, connID int64) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('migration:' || $1::bigint::text, 0))`, connID)
	if err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	return nil
}

func (r *Repo) Create(ctx context.Context, connID int64, name, sql, checksum string, createdBy int64) (domain.Migration, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return domain.Migration{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if err := lockMigrations(ctx, tx, connID); err != nil {
		return domain.Migration{}, err
	}

	var version int
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(version), 0) + 1
		FROM db_migrations
		WHERE connection_id = $1`, connID).Scan(&version)
	if err != nil {
		return domain.Migration{}, fmt.Errorf("next version: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO db_migrations (connection_id, version, name, sql_text, checksum, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		connID, version, name, sql, checksum, createdBy)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
		return domain.Migration{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Migration{}, fmt.Errorf("insert migration: %w", err)
	}

	m, err := scanMigration(tx.QueryRow(ctx, selectFull+` WHERE m.connection_id = $1 AND m.version = $2`, connID, version))
	if err != nil {
		return domain.Migration{}, fmt.Errorf("read created migration: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Migration{}, fmt.Errorf("commit: %w", err)
	}
	return m, nil
}

func (r *Repo) Get(ctx context.Context, connID int64, version int) (domain.Migration, error) {
	m, err := scanMigration(r.db.QueryRow(ctx, selectFull+` WHERE m.connection_id = $1 AND m.version = $2`, connID, version))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Migration{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Migration{}, fmt.Errorf("get migration: %w", err)
	}
	return m, nil
}

func (r *Repo) List(ctx context.Context, connID int64) ([]domain.Migration, error) {
	rows, err := r.db.Query(ctx, selectSummary+` WHERE m.connection_id = $1 ORDER BY m.version DESC`, connID)
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	defer rows.Close()

	var list []domain.Migration
	for rows.Next() {
		m, err := scanMigration(rows)
		if err != nil {
			return nil, fmt.Errorf("scan migration: %w", err)
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

func (r *Repo) UpdateDraft(ctx context.Context, connID int64, version int, name, sql, checksum string) (domain.Migration, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE db_migrations
		SET name = $3, sql_text = $4, checksum = $5, updated_at = now()
		WHERE connection_id = $1 AND version = $2 AND status = 'draft'`,
		connID, version, name, sql, checksum)
	if err != nil {
		return domain.Migration{}, fmt.Errorf("update draft: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Migration{}, r.notDraftOrMissing(ctx, connID, version)
	}
	return r.Get(ctx, connID, version)
}

func (r *Repo) Cancel(ctx context.Context, connID int64, version int) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE db_migrations
		SET status = 'cancelled', updated_at = now()
		WHERE connection_id = $1 AND version = $2 AND status = 'draft'`,
		connID, version)
	if err != nil {
		return fmt.Errorf("cancel migration: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return r.notDraftOrMissing(ctx, connID, version)
	}
	return nil
}

func (r *Repo) notDraftOrMissing(ctx context.Context, connID int64, version int) error {
	var one int
	err := r.db.QueryRow(ctx, `
		SELECT 1 FROM db_migrations WHERE connection_id = $1 AND version = $2`,
		connID, version).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("check migration: %w", err)
	}
	return domain.ErrMigrationNotDraft
}
