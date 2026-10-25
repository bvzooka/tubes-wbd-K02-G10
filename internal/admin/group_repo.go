package admin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"dbplatform/internal/domain"
)

type GroupRow struct {
	ID          int64
	Name        string
	Description string
	MemberCount int
	AccessCount int
	CreatedAt   time.Time
}

type GroupRepo struct{ db DB }

func NewGroupRepo(db DB) *GroupRepo { return &GroupRepo{db: db} }

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}

func (r *GroupRepo) Create(ctx context.Context, name, description string) (domain.Group, error) {
	g := domain.Group{Name: name, Description: description}
	err := r.db.QueryRow(ctx, `
		INSERT INTO user_groups (name, description)
		VALUES ($1, $2)
		RETURNING id, created_at`,
		name, description,
	).Scan(&g.ID, &g.CreatedAt)
	if isUniqueViolation(err) {
		return domain.Group{}, domain.ErrConflict
	}
	if err != nil {
		return domain.Group{}, fmt.Errorf("insert group: %w", err)
	}
	return g, nil
}

func (r *GroupRepo) List(ctx context.Context) ([]GroupRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT g.id, g.name, g.description, g.created_at,
		       (SELECT count(*) FROM group_members m WHERE m.group_id = g.id),
		       (SELECT count(*) FROM group_database_access a WHERE a.group_id = g.id)
		FROM user_groups g
		ORDER BY lower(g.name), g.id`)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	defer rows.Close()

	var groups []GroupRow
	for rows.Next() {
		var g GroupRow
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt, &g.MemberCount, &g.AccessCount); err != nil {
			return nil, fmt.Errorf("scan group: %w", err)
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func (r *GroupRepo) Get(ctx context.Context, id int64) (domain.Group, error) {
	g := domain.Group{ID: id}
	err := r.db.QueryRow(ctx, `
		SELECT name, description, created_at
		FROM user_groups
		WHERE id = $1`, id,
	).Scan(&g.Name, &g.Description, &g.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Group{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Group{}, fmt.Errorf("get group: %w", err)
	}
	return g, nil
}

func (r *GroupRepo) Update(ctx context.Context, id int64, name, description string) (domain.Group, error) {
	g := domain.Group{ID: id}
	err := r.db.QueryRow(ctx, `
		UPDATE user_groups
		SET name = $2, description = $3
		WHERE id = $1
		RETURNING name, description, created_at`,
		id, name, description,
	).Scan(&g.Name, &g.Description, &g.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Group{}, domain.ErrNotFound
	}
	if isUniqueViolation(err) {
		return domain.Group{}, domain.ErrConflict
	}
	if err != nil {
		return domain.Group{}, fmt.Errorf("update group: %w", err)
	}
	return g, nil
}

func (r *GroupRepo) Delete(ctx context.Context, id int64) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var exists int
	err = tx.QueryRow(ctx, `SELECT 1 FROM user_groups WHERE id = $1 FOR UPDATE`, id).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock group: %w", err)
	}

	var members, grants int
	err = tx.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM group_members WHERE group_id = $1),
		       (SELECT count(*) FROM group_database_access WHERE group_id = $1)`, id,
	).Scan(&members, &grants)
	if err != nil {
		return fmt.Errorf("count group usage: %w", err)
	}
	if members > 0 || grants > 0 {
		return ErrGroupNotEmpty
	}

	if _, err := tx.Exec(ctx, `DELETE FROM user_groups WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete group: %w", err)
	}
	return tx.Commit(ctx)
}
