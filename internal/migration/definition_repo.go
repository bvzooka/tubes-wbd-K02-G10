package migration

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"dbplatform/internal/appdb"
	"dbplatform/internal/domain"
)

type Definition struct {
	DraftInput
	ID            int64
	ConnectionID  int64
	Version       int
	Revision      int
	Checksum      string
	State         domain.MigrationState
	CreatedBy     int64
	CreatedByName string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// dua %s diisi SQL up dan down, daftar mengisinya teks kosong supaya tetap ringan
const selectDefinitionTemplate = `
	SELECT m.id, m.connection_id, m.version, m.revision, m.name, %s, %s,
	       m.irreversible, m.irreversible_reason, m.mode, m.batch_config,
	       m.checksum, m.state, m.created_by, u.username, m.created_at, m.updated_at
	FROM db_migrations m
	JOIN users u ON u.id = m.created_by`

var (
	selectDefinitionFull    = fmt.Sprintf(selectDefinitionTemplate, "m.sql_text", "m.sql_down")
	selectDefinitionSummary = fmt.Sprintf(selectDefinitionTemplate, "''::text", "''::text")
)

// urutan Scan harus sama persis dengan urutan kolom di query
func scanDefinition(row pgx.Row) (Definition, error) {
	var d Definition
	err := row.Scan(
		&d.ID, &d.ConnectionID, &d.Version, &d.Revision, &d.Name, &d.UpSQL, &d.DownSQL,
		&d.Irreversible, &d.IrreversibleReason, &d.Mode, &d.Batch,
		&d.Checksum, &d.State, &d.CreatedBy, &d.CreatedByName, &d.CreatedAt, &d.UpdatedAt,
	)
	return d, err
}

// getDefinition menerima pool atau transaction
func getDefinition(ctx context.Context, q appdb.DBTX, connID int64, version int) (Definition, error) {
	d, err := scanDefinition(q.QueryRow(ctx,
		selectDefinitionFull+` WHERE m.connection_id = $1 AND m.version = $2`, connID, version))
	if errors.Is(err, pgx.ErrNoRows) {
		return Definition{}, domain.ErrNotFound
	}
	if err != nil {
		return Definition{}, fmt.Errorf("get definition: %w", err)
	}
	return d, nil
}

// CreateDefinition menyimpan draft baru dengan versi berikutnya
func (r *Repo) CreateDefinition(ctx context.Context, connID int64, in DraftInput, createdBy int64) (Definition, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return Definition{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// dikunci per database supaya dua orang tidak dapat nomor versi yang sama
	if err := lockMigrations(ctx, tx, connID); err != nil {
		return Definition{}, err
	}
	var version int
	err = tx.QueryRow(ctx, `
		INSERT INTO db_migrations (
			connection_id, version, name, sql_text, sql_down, irreversible, irreversible_reason,
			mode, batch_config, checksum, created_by)
		VALUES (
			$1, (SELECT COALESCE(MAX(version), 0) + 1 FROM db_migrations WHERE connection_id = $1),
			$2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING version`,
		connID, in.Name, in.UpSQL, in.DownSQL, in.Irreversible, in.IrreversibleReason,
		in.Mode, in.Batch, draftChecksum(in), createdBy,
	).Scan(&version)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
		return Definition{}, domain.ErrNotFound
	}
	if err != nil {
		return Definition{}, fmt.Errorf("insert definition: %w", err)
	}

	d, err := getDefinition(ctx, tx, connID, version)
	if err != nil {
		return Definition{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Definition{}, fmt.Errorf("commit definition: %w", err)
	}
	return d, nil
}

// GetDefinition mengambil satu migration lengkap dengan SQL nya
func (r *Repo) GetDefinition(ctx context.Context, connID int64, version int) (Definition, error) {
	return getDefinition(ctx, r.db, connID, version)
}

// ListDefinitions mengurutkan dari versi terkecil tanpa membawa SQL
func (r *Repo) ListDefinitions(ctx context.Context, connID int64) ([]Definition, error) {
	rows, err := r.db.Query(ctx,
		selectDefinitionSummary+` WHERE m.connection_id = $1 ORDER BY m.version`, connID)
	if err != nil {
		return nil, fmt.Errorf("list definitions: %w", err)
	}
	defer rows.Close()

	var list []Definition
	for rows.Next() {
		d, err := scanDefinition(rows)
		if err != nil {
			return nil, fmt.Errorf("scan definition: %w", err)
		}
		list = append(list, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read definitions: %w", err)
	}
	return list, nil
}

// UpdateDefinition hanya berhasil kalau masih draft dan revisinya sama dengan yang dibaca user, jadi perubahan orang lain tidak tertimpa
func (r *Repo) UpdateDefinition(ctx context.Context, connID int64, version, revision int, in DraftInput) (Definition, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE db_migrations
		SET name = $4, sql_text = $5, sql_down = $6, irreversible = $7, irreversible_reason = $8,
		    mode = $9, batch_config = $10, checksum = $11, revision = revision + 1, updated_at = now()
		WHERE connection_id = $1 AND version = $2 AND revision = $3 AND state = 'draft'`,
		connID, version, revision, in.Name, in.UpSQL, in.DownSQL, in.Irreversible, in.IrreversibleReason,
		in.Mode, in.Batch, draftChecksum(in))
	if err != nil {
		return Definition{}, fmt.Errorf("update definition: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// dibaca ulang untuk membedakan tidak ada dengan sudah berubah
		if _, err := r.GetDefinition(ctx, connID, version); err != nil {
			return Definition{}, err
		}
		return Definition{}, refuse(domain.ErrConflict, "Versi %d sudah berubah atau bukan draft lagi, muat ulang halamannya.", version)
	}
	return r.GetDefinition(ctx, connID, version)
}

// MoveDefinition mengganti status hanya kalau status dan revisinya masih sama seperti saat dicek dan tidak ada permintaan yang belum selesai
func (r *Repo) MoveDefinition(ctx context.Context, d Definition, to domain.MigrationState) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// dikunci supaya tidak ada permintaan baru yang masuk di antara pengecekan dan ganti status
	if err := lockMigrations(ctx, tx, d.ConnectionID); err != nil {
		return err
	}
	open, err := hasOpenExecution(ctx, tx, d.ID)
	if err != nil {
		return err
	}
	if open {
		return refuse(domain.ErrConflict, "Versi %d masih punya permintaan yang belum selesai.", d.Version)
	}

	// kembali jadi draft dihitung revisi baru supaya persetujuan lama tidak dianggap berlaku
	tag, err := tx.Exec(ctx, `
		UPDATE db_migrations
		SET state = $4::text, updated_at = now(),
		    revision = revision + CASE WHEN $4::text = 'draft' THEN 1 ELSE 0 END
		WHERE id = $1 AND state = $2 AND revision = $3`,
		d.ID, d.State, d.Revision, to)
	if err != nil {
		return fmt.Errorf("move definition: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return refuse(domain.ErrConflict, "Versi %d sudah berubah, muat ulang halamannya.", d.Version)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit move: %w", err)
	}
	return nil
}

// hasOpenExecution true kalau masih ada permintaan yang menunggu keputusan atau sedang berjalan
func hasOpenExecution(ctx context.Context, q appdb.DBTX, migrationID int64) (bool, error) {
	var open bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM migration_executions
			WHERE migration_id = $1 AND status IN ('pending_review', 'approved', 'queued', 'running'))`,
		migrationID).Scan(&open)
	if err != nil {
		return false, fmt.Errorf("check open execution: %w", err)
	}
	return open, nil
}
