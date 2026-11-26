package migration_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/domain"
	"dbplatform/internal/migration"
	"dbplatform/internal/testutil"
)

type repoEnv struct {
	pool *pgxpool.Pool
	repo *migration.Repo
	user domain.User
	conn int64
}

func newRepoEnv(t *testing.T) repoEnv {
	t.Helper()
	pool := testutil.NewAppDB(t)
	return repoEnv{
		pool: pool,
		repo: migration.NewRepo(pool),
		user: testutil.SeedUser(t, pool, "budi", false),
		conn: testutil.SeedConnection(t, pool, "sample", true),
	}
}
