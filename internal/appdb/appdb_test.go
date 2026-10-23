package appdb_test

import (
	"context"
	"testing"

	"dbplatform/internal/appdb"
	"dbplatform/internal/testutil"
)

func TestMigrate_Integration_Idempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	pool := testutil.NewTargetDB(t) // database kosong tanpa skema internal

	for i := 0; i < 2; i++ {
		if err := appdb.Migrate(ctx, pool); err != nil {
			t.Fatalf("migrate #%d: %v", i+1, err)
		}
	}

	var versions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM app_schema_versions WHERE version = '202610200001_init'`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 1 {
		t.Fatalf("versi init tercatat %d kali, harus 1", versions)
	}
	for _, table := range []string{"users", "user_groups", "group_members", "connections", "group_database_access", "sessions", "db_migrations"} {
		var ok bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&ok); err != nil || !ok {
			t.Errorf("tabel %s tidak ada (err=%v)", table, err)
		}
	}
}

func TestMigrate_Integration_Concurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	pool := testutil.NewTargetDB(t)

	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() { errs <- appdb.Migrate(ctx, pool) }()
	}
	for i := 0; i < 4; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("migrate bersamaan: %v", err)
		}
	}
}
