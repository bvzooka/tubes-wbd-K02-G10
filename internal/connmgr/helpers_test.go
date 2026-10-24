package connmgr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/domain"
	"dbplatform/internal/testutil"
)

var (
	adminPrincipal = domain.Principal{UserID: 1, Username: "root", IsPlatformAdmin: true}
	plainPrincipal = domain.Principal{UserID: 2, Username: "biasa"}
)

func newRepo(t *testing.T) (*Repo, *pgxpool.Pool) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.NewAppDB(t)
	return NewRepo(pool, testKey(1)), pool
}

func inputFor(t *testing.T, target *pgxpool.Pool, name string) domain.ConnectionInput {
	t.Helper()
	cc := target.Config().ConnConfig
	return domain.ConnectionInput{
		Name:         name,
		DBMS:         domain.DBMSPostgres,
		Host:         cc.Host,
		Port:         int(cc.Port),
		DatabaseName: cc.Database,
		Username:     cc.User,
		Password:     cc.Password,
		SSLMode:      "disable",
	}
}

func newPasswordRole(t *testing.T, target *pgxpool.Pool, password string) string {
	t.Helper()
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	role := "role_" + hex.EncodeToString(b)
	ctx := context.Background()
	sql := "CREATE ROLE " + pgx.Identifier{role}.Sanitize() + " LOGIN PASSWORD " + quoteLiteral(password)
	if _, err := target.Exec(ctx, sql); err != nil {
		t.Fatalf("create role: %v", err)
	}
	t.Cleanup(func() {
		_, _ = target.Exec(context.Background(), "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize())
	})
	return role
}

func quoteLiteral(s string) string {
	out := []byte{'\''}
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, '\'')
		}
		out = append(out, s[i])
	}
	return string(append(out, '\''))
}
