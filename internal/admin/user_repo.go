package admin

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

type UserRepo struct{ db DB }

func NewUserRepo(db DB) *UserRepo { return &UserRepo{db: db} }

const (
	uniqueViolation = "23505"
	adminLockKey    = "platform-admins"
)

func (r *UserRepo) Create(ctx context.Context, username, passwordHash string, isAdmin bool) (domain.User, error) {
	u := domain.User{Username: username, IsPlatformAdmin: isAdmin, IsActive: true}
	err := r.db.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, is_platform_admin)
		VALUES ($1, $2, $3)
		RETURNING id, created_at`,
		username, passwordHash, isAdmin,
	).Scan(&u.ID, &u.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return domain.User{}, domain.ErrConflict
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("insert user: %w", err)
	}
	return u, nil
}

func (r *UserRepo) List(ctx context.Context) ([]domain.User, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, username, is_platform_admin, is_active, created_at
		FROM users
		ORDER BY username`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var users []domain.User
	for rows.Next() {
		var u domain.User
		if err := rows.Scan(&u.ID, &u.Username, &u.IsPlatformAdmin, &u.IsActive, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (r *UserRepo) Get(ctx context.Context, id int64) (domain.User, error) {
	u := domain.User{ID: id}
	err := r.db.QueryRow(ctx, `
		SELECT username, is_platform_admin, is_active, created_at
		FROM users
		WHERE id = $1`, id,
	).Scan(&u.Username, &u.IsPlatformAdmin, &u.IsActive, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("get user: %w", err)
	}
	return u, nil
}

func (r *UserRepo) Update(ctx context.Context, id int64, passwordHash string, isAdmin bool) (domain.User, bool, error) {
	var u domain.User
	var wasAdmin bool
	err := r.inAdminTx(ctx, func(tx pgx.Tx) error {
		curAdmin, curActive, err := lockUser(ctx, tx, id)
		if err != nil {
			return err
		}
		wasAdmin = curAdmin
		if curAdmin && curActive && !isAdmin {
			if err := requireOtherActiveAdmin(ctx, tx, id); err != nil {
				return err
			}
		}
		u = domain.User{ID: id}
		return tx.QueryRow(ctx, `
			UPDATE users
			SET password_hash     = CASE WHEN $2::text = '' THEN password_hash ELSE $2::text END,
			    is_platform_admin = $3,
			    updated_at        = now()
			WHERE id = $1
			RETURNING username, is_platform_admin, is_active, created_at`,
			id, passwordHash, isAdmin,
		).Scan(&u.Username, &u.IsPlatformAdmin, &u.IsActive, &u.CreatedAt)
	})
	if err != nil {
		return domain.User{}, false, err
	}
	return u, wasAdmin, nil
}

func (r *UserRepo) SetActive(ctx context.Context, id int64, active bool) (domain.User, error) {
	var u domain.User
	err := r.inAdminTx(ctx, func(tx pgx.Tx) error {
		curAdmin, curActive, err := lockUser(ctx, tx, id)
		if err != nil {
			return err
		}
		if !active && curAdmin && curActive {
			if err := requireOtherActiveAdmin(ctx, tx, id); err != nil {
				return err
			}
		}
		u = domain.User{ID: id}
		return tx.QueryRow(ctx, `
			UPDATE users
			SET is_active  = $2,
			    updated_at = now()
			WHERE id = $1
			RETURNING username, is_platform_admin, is_active, created_at`,
			id, active,
		).Scan(&u.Username, &u.IsPlatformAdmin, &u.IsActive, &u.CreatedAt)
	})
	if err != nil {
		return domain.User{}, err
	}
	return u, nil
}

// Perubahan admin dijalanin satu-satu lewat kunci
func (r *UserRepo) inAdminTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, adminLockKey); err != nil {
		return fmt.Errorf("lock admins: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func lockUser(ctx context.Context, tx pgx.Tx, id int64) (isAdmin, isActive bool, err error) {
	err = tx.QueryRow(ctx, `
		SELECT is_platform_admin, is_active
		FROM users
		WHERE id = $1
		FOR UPDATE`, id,
	).Scan(&isAdmin, &isActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, domain.ErrNotFound
	}
	if err != nil {
		return false, false, fmt.Errorf("lock user: %w", err)
	}
	return isAdmin, isActive, nil
}

// Pastiin masih ada admin aktif lain
func requireOtherActiveAdmin(ctx context.Context, tx pgx.Tx, id int64) error {
	var n int
	err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM users
		WHERE is_platform_admin AND is_active AND id <> $1`, id,
	).Scan(&n)
	if err != nil {
		return fmt.Errorf("count admins: %w", err)
	}
	if n == 0 {
		return ErrLastAdmin
	}
	return nil
}
