package pgmeta_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/dbdriver"
	"dbplatform/internal/dbdriver/pgmeta"
	"dbplatform/internal/dbdriver/postgres"
	"dbplatform/internal/domain"
	"dbplatform/internal/testutil"
)

// newDriver membuat database target sementara dan driver Postgres di atasnya
func newDriver(t *testing.T) (*postgres.Driver, *pgxpool.Pool) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test butuh Postgres (make up)")
	}
	pool := testutil.NewTargetDB(t, `CREATE TABLE items (id bigint PRIMARY KEY)`)
	return postgres.NewFromPool(pool), pool
}

// withTx menjalankan fn di sesi terpin, seperti yang dilakukan runner
func withTx(t *testing.T, d *postgres.Driver, fn func(tx dbdriver.ArgTx) error) error {
	t.Helper()
	s, err := d.OpenSession(context.Background())
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer s.Close()
	return s.WithTx(context.Background(), dbdriver.TxOptions{}, fn)
}

func ensure(t *testing.T, d *postgres.Driver) {
	t.Helper()
	err := withTx(t, d, func(tx dbdriver.ArgTx) error { return pgmeta.Ensure(context.Background(), tx) })
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
}

func TestEnsure_Integration_IsIdempotent(t *testing.T) {
	d, pool := newDriver(t)
	ensure(t, d)
	ensure(t, d) // kedua kali ga boleh error

	var n int
	err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = $1 AND table_name IN ('executions', 'batch_scopes', 'batches')`,
		pgmeta.Schema).Scan(&n)
	if err != nil || n != 3 {
		t.Fatalf("tabel meta terbentuk = %d (err %v), mau 3", n, err)
	}
}

func TestEnsure_Integration_FailsClearlyWithoutCreatePrivilege(t *testing.T) {
	_, pool := newDriver(t)
	ctx := context.Background()

	// user tanpa hak CREATE di database: hak ini diberikan ke PUBLIC secara default hanya untuk CONNECT
	role := "limited_" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "_"))
	if len(role) > 50 {
		role = role[:50]
	}
	testutil.MustExec(t, pool, `CREATE ROLE `+role+` LOGIN PASSWORD 'pw'`)
	t.Cleanup(func() { testutil.MustExec(t, pool, `DROP ROLE IF EXISTS `+role) })

	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.User, cfg.ConnConfig.Password = role, "pw"
	limitedPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(limitedPool.Close)

	limited := postgres.NewFromPool(limitedPool)
	err = withTx(t, limited, func(tx dbdriver.ArgTx) error { return pgmeta.Ensure(ctx, tx) })
	if err == nil {
		t.Fatal("Ensure harus gagal tanpa hak CREATE")
	}
	if !strings.Contains(err.Error(), pgmeta.Schema) || !strings.Contains(err.Error(), "CREATE") {
		t.Fatalf("pesan error kurang jelas: %v", err)
	}
}

func TestRecordExecution_Integration_DuplicateIsRejected(t *testing.T) {
	d, _ := newDriver(t)
	ensure(t, d)
	ctx := context.Background()
	exec := pgmeta.Execution{ID: 42, Kind: "apply", Attempt: 1, Checksum: "abc"}

	err := withTx(t, d, func(tx dbdriver.ArgTx) error { return pgmeta.RecordExecution(ctx, tx, exec) })
	if err != nil {
		t.Fatalf("RecordExecution pertama: %v", err)
	}

	err = withTx(t, d, func(tx dbdriver.ArgTx) error { return pgmeta.RecordExecution(ctx, tx, exec) })
	if !errors.Is(err, pgmeta.ErrDuplicateExecution) {
		t.Fatalf("RecordExecution kedua = %v, mau ErrDuplicateExecution", err)
	}
}

func TestExecutionCommitted_Integration_FollowsCommit(t *testing.T) {
	d, _ := newDriver(t)
	ensure(t, d)
	ctx := context.Background()

	check := func(id int64) bool {
		var got bool
		err := withTx(t, d, func(tx dbdriver.ArgTx) error {
			var err error
			got, err = pgmeta.ExecutionCommitted(ctx, tx, id)
			return err
		})
		if err != nil {
			t.Fatalf("ExecutionCommitted: %v", err)
		}
		return got
	}

	if check(7) {
		t.Fatal("eksekusi 7 belum tercatat")
	}
	// dicatat lalu di-rollback: bukti tidak boleh ada
	_ = withTx(t, d, func(tx dbdriver.ArgTx) error {
		if err := pgmeta.RecordExecution(ctx, tx, pgmeta.Execution{ID: 7, Kind: "apply", Attempt: 1, Checksum: "x"}); err != nil {
			return err
		}
		return dbdriver.ErrForceRollback
	})
	if check(7) {
		t.Fatal("setelah rollback, eksekusi 7 tidak boleh tercatat")
	}
	err := withTx(t, d, func(tx dbdriver.ArgTx) error {
		return pgmeta.RecordExecution(ctx, tx, pgmeta.Execution{ID: 7, Kind: "apply", Attempt: 1, Checksum: "x"})
	})
	if err != nil || !check(7) {
		t.Fatalf("setelah commit, eksekusi 7 harus tercatat (err %v)", err)
	}
}

func TestScope_Integration_WrittenOnce(t *testing.T) {
	d, _ := newDriver(t)
	ensure(t, d)
	ctx := context.Background()

	err := withTx(t, d, func(tx dbdriver.ArgTx) error {
		if _, found, err := pgmeta.Scope(ctx, tx, 5); err != nil || found {
			t.Errorf("sebelum SetScope: found=%v err=%v, mau found=false", found, err)
		}
		if err := pgmeta.SetScope(ctx, tx, 5, 1000); err != nil {
			return err
		}
		got, found, err := pgmeta.Scope(ctx, tx, 5)
		if err != nil || !found || got != 1000 {
			t.Errorf("Scope = %d, %v, %v; mau 1000, true, nil", got, found, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// menulis lagi untuk id yang sama harus ditolak: cakupan hanya ditentukan sekali di awal
	err = withTx(t, d, func(tx dbdriver.ArgTx) error { return pgmeta.SetScope(ctx, tx, 5, 2000) })
	if err == nil {
		t.Fatal("SetScope kedua untuk id yang sama harus error")
	}
}

func TestBatches_Integration_CursorAndProgress(t *testing.T) {
	d, _ := newDriver(t)
	ensure(t, d)
	ctx := context.Background()
	chain := []int64{10, 11} // eksekusi awal (10) lalu resumenya (11)

	err := withTx(t, d, func(tx dbdriver.ArgTx) error {
		if _, found, err := pgmeta.ResumeCursor(ctx, tx, chain); err != nil || found {
			t.Errorf("tanpa batch: found=%v err=%v, mau found=false", found, err)
		}
		if p, err := pgmeta.ForwardProgress(ctx, tx, chain); err != nil || p != (pgmeta.Progress{}) {
			t.Errorf("tanpa batch: progress=%+v err=%v, mau nol", p, err)
		}
		batches := []pgmeta.Batch{
			{ExecutionID: 10, BatchNo: 1, Direction: pgmeta.Forward, LoKey: 0, HiKey: 100, RowsAffected: 100},
			{ExecutionID: 10, BatchNo: 2, Direction: pgmeta.Forward, LoKey: 100, HiKey: 200, RowsAffected: 100},
			{ExecutionID: 11, BatchNo: 1, Direction: pgmeta.Forward, LoKey: 200, HiKey: 250, RowsAffected: 50},
			// batch revert dan eksekusi di luar rantai tidak boleh ikut dihitung
			{ExecutionID: 10, BatchNo: 3, Direction: pgmeta.Revert, LoKey: 100, HiKey: 200, RowsAffected: 100},
			{ExecutionID: 99, BatchNo: 1, Direction: pgmeta.Forward, LoKey: 0, HiKey: 9999, RowsAffected: 9999},
		}
		for _, b := range batches {
			if err := pgmeta.RecordBatch(ctx, tx, b); err != nil {
				return err
			}
		}
		cursor, found, err := pgmeta.ResumeCursor(ctx, tx, chain)
		if err != nil || !found || cursor != 250 {
			t.Errorf("ResumeCursor = %d, %v, %v; mau 250, true, nil", cursor, found, err)
		}
		p, err := pgmeta.ForwardProgress(ctx, tx, chain)
		if err != nil || p.Batches != 3 || p.Rows != 250 {
			t.Errorf("ForwardProgress = %+v, %v; mau {3 250}", p, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRecordBatch_Integration_RejectsUnknownDirection(t *testing.T) {
	d, _ := newDriver(t)
	ensure(t, d)
	err := withTx(t, d, func(tx dbdriver.ArgTx) error {
		return pgmeta.RecordBatch(context.Background(), tx,
			pgmeta.Batch{ExecutionID: 1, BatchNo: 1, Direction: "sideways", HiKey: 1})
	})
	if err == nil {
		t.Fatal("arah selain forward dan revert harus ditolak oleh CHECK")
	}
}

func TestIntrospect_Integration_HidesMetaSchema(t *testing.T) {
	d, _ := newDriver(t)
	ensure(t, d)
	ctx := context.Background()

	schema, err := d.Introspect(ctx)
	if err != nil {
		t.Fatalf("Introspect: %v", err)
	}
	if len(schema.Tables) != 1 || schema.Tables[0].Name != "items" {
		t.Fatalf("Introspect harus hanya menampilkan items, dapat %+v", schema.Tables)
	}

	_, err = d.TableData(ctx, domain.TableRef{Schema: pgmeta.Schema, Table: "executions"}, domain.PageRequest{})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("TableData tabel meta = %v, mau ErrNotFound", err)
	}
}
