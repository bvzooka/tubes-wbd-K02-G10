package authstub

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/contract"
	"dbplatform/internal/domain"
)

type Bundle struct {
	Middleware   contract.AuthMiddleware
	Authorizer   contract.Authorizer
	Hasher       contract.PasswordHasher
	SessionAdmin contract.SessionAdmin
}

func New(ctx context.Context, pool *pgxpool.Pool) (*Bundle, error) {
	p, err := ensureAdmin(ctx, pool)
	if err != nil {
		return nil, err
	}
	return &Bundle{
		Middleware:   &Middleware{Principal: p},
		Authorizer:   &Authorizer{Pool: pool},
		Hasher:       Hasher{},
		SessionAdmin: SessionAdmin{},
	}, nil
}

func ensureAdmin(ctx context.Context, pool *pgxpool.Pool) (domain.Principal, error) {
	var p domain.Principal
	err := pool.QueryRow(ctx, `
		WITH ins AS (
			INSERT INTO users (username, password_hash, is_platform_admin)
			SELECT 'stubadmin', 'stub:disabled', TRUE
			WHERE NOT EXISTS (SELECT 1 FROM users)
			RETURNING id, username, is_platform_admin
		)
		SELECT id, username, is_platform_admin FROM ins
		UNION ALL
		(SELECT id, username, is_platform_admin FROM users ORDER BY id LIMIT 1)
		LIMIT 1`).Scan(&p.UserID, &p.Username, &p.IsPlatformAdmin)
	if err != nil {
		return p, fmt.Errorf("authstub: siapkan user: %w", err)
	}
	return p, nil
}

type Middleware struct{ Principal domain.Principal }

var _ contract.AuthMiddleware = (*Middleware)(nil)

func (m *Middleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := domain.WithPrincipal(r.Context(), m.Principal)
		ctx = domain.WithCSRFToken(ctx, "stub-csrf-token")
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *Middleware) RequireAdmin(next http.Handler) http.Handler { return next }

func (m *Middleware) RequireDBAccess(domain.AccessLevel) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

func (m *Middleware) CSRF(next http.Handler) http.Handler { return next }

type Authorizer struct{ Pool *pgxpool.Pool }

var _ contract.Authorizer = (*Authorizer)(nil)

func (a *Authorizer) EffectiveLevel(context.Context, int64, int64) (domain.AccessLevel, error) {
	return domain.AccessViewEdit, nil
}

func (a *Authorizer) Can(context.Context, int64, int64, domain.AccessLevel) (bool, error) {
	return true, nil
}

func (a *Authorizer) ListAccessible(ctx context.Context, _ int64) ([]domain.AccessibleDatabase, error) {
	if a.Pool == nil {
		return nil, nil
	}
	rows, err := a.Pool.Query(ctx, `SELECT id, name, dbms FROM connections WHERE is_active ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AccessibleDatabase
	for rows.Next() {
		d := domain.AccessibleDatabase{Level: domain.AccessViewEdit}
		if err := rows.Scan(&d.ConnectionID, &d.Name, &d.DBMS); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

type Hasher struct{}

var _ contract.PasswordHasher = Hasher{}

func (Hasher) Hash(plain string) (string, error) { return "stub:" + plain, nil }
func (Hasher) Verify(hash, plain string) bool    { return hash == "stub:"+plain }

type SessionAdmin struct{}

var _ contract.SessionAdmin = SessionAdmin{}

func (SessionAdmin) RevokeAllForUser(context.Context, int64) error      { return nil }
func (SessionAdmin) MarkRotationNeeded(context.Context, ...int64) error { return nil }
