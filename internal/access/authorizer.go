// Package access menghitung akses efektif user ke database dari keanggotaan group.
package access

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"dbplatform/internal/contract"
	"dbplatform/internal/domain"
)

// Querier adalah bagian *pgxpool.Pool yang dipakai Authorizer.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Authorizer memenuhi contract.Authorizer. Akses selalu dihitung dari database (tanpa cache),
// sehingga perubahan keanggotaan atau grant langsung berlaku pada request berikutnya.
// Platform Admin tidak mendapat akses database otomatis.
type Authorizer struct {
	db Querier
}

func New(db Querier) *Authorizer { return &Authorizer{db: db} }

var _ contract.Authorizer = (*Authorizer)(nil)

func (a *Authorizer) EffectiveLevel(ctx context.Context, userID, connectionID int64) (domain.AccessLevel, error) {
	rows, err := a.db.Query(ctx, `
		SELECT gda.access_level
		FROM group_members gm
		JOIN group_database_access gda ON gda.group_id = gm.group_id
		JOIN connections c ON c.id = gda.connection_id AND c.is_active
		JOIN users u ON u.id = gm.user_id AND u.is_active
		WHERE gm.user_id = $1 AND gda.connection_id = $2`,
		userID, connectionID)
	if err != nil {
		return domain.AccessNone, fmt.Errorf("effective level: %w", err)
	}
	levels, err := pgx.CollectRows(rows, pgx.RowTo[domain.AccessLevel])
	if err != nil {
		return domain.AccessNone, fmt.Errorf("effective level: %w", err)
	}
	return MaxLevel(levels...), nil
}

func (a *Authorizer) Can(ctx context.Context, userID, connectionID int64, min domain.AccessLevel) (bool, error) {
	level, err := a.EffectiveLevel(ctx, userID, connectionID)
	if err != nil {
		return false, err
	}
	return level.AtLeast(min), nil
}

func (a *Authorizer) ListAccessible(ctx context.Context, userID int64) ([]domain.AccessibleDatabase, error) {
	rows, err := a.db.Query(ctx, `
		SELECT c.id, c.name, c.dbms, gda.access_level
		FROM group_members gm
		JOIN group_database_access gda ON gda.group_id = gm.group_id
		JOIN connections c ON c.id = gda.connection_id AND c.is_active
		JOIN users u ON u.id = gm.user_id AND u.is_active
		WHERE gm.user_id = $1
		ORDER BY lower(c.name), c.id`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("list accessible: %w", err)
	}
	defer rows.Close()

	var out []domain.AccessibleDatabase
	index := map[int64]int{}
	for rows.Next() {
		var db domain.AccessibleDatabase
		if err := rows.Scan(&db.ConnectionID, &db.Name, &db.DBMS, &db.Level); err != nil {
			return nil, fmt.Errorf("list accessible: %w", err)
		}
		if i, ok := index[db.ConnectionID]; ok {
			out[i].Level = MaxLevel(out[i].Level, db.Level)
			continue
		}
		index[db.ConnectionID] = len(out)
		out = append(out, db)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list accessible: %w", err)
	}
	return out, nil
}
