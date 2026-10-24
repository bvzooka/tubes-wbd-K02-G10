package admin

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"dbplatform/internal/domain"
)

const foreignKeyViolation = "23503"

type GroupMember struct {
	UserID   int64
	Username string
	IsActive bool
}

type GroupAccess struct {
	ConnectionID   int64
	ConnectionName string
	Level          domain.AccessLevel
}

type UserOption struct {
	ID       int64
	Username string
}

type ConnOption struct {
	ID   int64
	Name string
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation
}

func (r *GroupRepo) ListMembers(ctx context.Context, groupID int64) ([]GroupMember, error) {
	rows, err := r.db.Query(ctx, `
		SELECT u.id, u.username, u.is_active
		FROM group_members m
		JOIN users u ON u.id = m.user_id
		WHERE m.group_id = $1
		ORDER BY u.username`, groupID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()

	var members []GroupMember
	for rows.Next() {
		var m GroupMember
		if err := rows.Scan(&m.UserID, &m.Username, &m.IsActive); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

func (r *GroupRepo) MemberIDs(ctx context.Context, groupID int64) ([]int64, error) {
	rows, err := r.db.Query(ctx, `SELECT user_id FROM group_members WHERE group_id = $1 ORDER BY user_id`, groupID)
	if err != nil {
		return nil, fmt.Errorf("list member ids: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan member id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *GroupRepo) AddMember(ctx context.Context, groupID, userID int64) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		INSERT INTO group_members (group_id, user_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, groupID, userID)
	if isForeignKeyViolation(err) {
		return false, domain.ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("add member: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (r *GroupRepo) RemoveMember(ctx context.Context, groupID, userID int64) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM group_members WHERE group_id = $1 AND user_id = $2`, groupID, userID)
	if err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *GroupRepo) ListAccess(ctx context.Context, groupID int64) ([]GroupAccess, error) {
	rows, err := r.db.Query(ctx, `
		SELECT a.connection_id, c.name, a.access_level
		FROM group_database_access a
		JOIN connections c ON c.id = a.connection_id
		WHERE a.group_id = $1
		ORDER BY lower(c.name), c.id`, groupID)
	if err != nil {
		return nil, fmt.Errorf("list access: %w", err)
	}
	defer rows.Close()

	var list []GroupAccess
	for rows.Next() {
		var a GroupAccess
		var level string
		if err := rows.Scan(&a.ConnectionID, &a.ConnectionName, &level); err != nil {
			return nil, fmt.Errorf("scan access: %w", err)
		}
		a.Level = domain.AccessLevel(level)
		list = append(list, a)
	}
	return list, rows.Err()
}

func (r *GroupRepo) GrantAccess(ctx context.Context, groupID, connID int64, level domain.AccessLevel, grantedBy int64) (domain.AccessLevel, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return domain.AccessNone, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var active bool
	err = tx.QueryRow(ctx, `SELECT is_active FROM connections WHERE id = $1`, connID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AccessNone, domain.ErrNotFound
	}
	if err != nil {
		return domain.AccessNone, fmt.Errorf("check connection: %w", err)
	}
	if !active {
		return domain.AccessNone, domain.ErrConnectionInactive
	}

	var previous domain.AccessLevel
	var current string
	err = tx.QueryRow(ctx, `
		SELECT access_level
		FROM group_database_access
		WHERE group_id = $1 AND connection_id = $2
		FOR UPDATE`, groupID, connID).Scan(&current)
	switch {
	case err == nil:
		previous = domain.AccessLevel(current)
	case errors.Is(err, pgx.ErrNoRows):
	default:
		return domain.AccessNone, fmt.Errorf("read current access: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO group_database_access (group_id, connection_id, access_level, granted_by)
		VALUES ($1, $2, $3, NULLIF($4::bigint, 0))
		ON CONFLICT (group_id, connection_id) DO UPDATE
		SET access_level = EXCLUDED.access_level,
		    granted_by   = EXCLUDED.granted_by,
		    granted_at   = now()`,
		groupID, connID, string(level), grantedBy)
	if isForeignKeyViolation(err) {
		return domain.AccessNone, domain.ErrNotFound
	}
	if err != nil {
		return domain.AccessNone, fmt.Errorf("grant access: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AccessNone, fmt.Errorf("commit grant: %w", err)
	}
	return previous, nil
}

func (r *GroupRepo) RevokeAccess(ctx context.Context, groupID, connID int64) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM group_database_access WHERE group_id = $1 AND connection_id = $2`, groupID, connID)
	if err != nil {
		return fmt.Errorf("revoke access: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *GroupRepo) AvailableUsers(ctx context.Context, groupID int64) ([]UserOption, error) {
	rows, err := r.db.Query(ctx, `
		SELECT u.id, u.username
		FROM users u
		WHERE u.is_active
		  AND NOT EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = $1 AND m.user_id = u.id)
		ORDER BY u.username`, groupID)
	if err != nil {
		return nil, fmt.Errorf("available users: %w", err)
	}
	defer rows.Close()

	var list []UserOption
	for rows.Next() {
		var o UserOption
		if err := rows.Scan(&o.ID, &o.Username); err != nil {
			return nil, fmt.Errorf("scan user option: %w", err)
		}
		list = append(list, o)
	}
	return list, rows.Err()
}

func (r *GroupRepo) AvailableConnections(ctx context.Context) ([]ConnOption, error) {
	rows, err := r.db.Query(ctx, `SELECT id, name FROM connections WHERE is_active ORDER BY lower(name), id`)
	if err != nil {
		return nil, fmt.Errorf("available connections: %w", err)
	}
	defer rows.Close()

	var list []ConnOption
	for rows.Next() {
		var o ConnOption
		if err := rows.Scan(&o.ID, &o.Name); err != nil {
			return nil, fmt.Errorf("scan connection option: %w", err)
		}
		list = append(list, o)
	}
	return list, rows.Err()
}
