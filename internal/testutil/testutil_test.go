package testutil_test

import (
	"context"
	"errors"
	"testing"

	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
	"dbplatform/internal/testutil"
)

func TestSeed_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.NewAppDB(t)
	u := testutil.SeedUser(t, pool, "budi", false)
	conn := testutil.SeedConnection(t, pool, "sample", true)
	g := testutil.SeedGroup(t, pool, "tim", u.ID)
	testutil.Grant(t, pool, g, conn, domain.AccessViewOnly)
	testutil.Grant(t, pool, g, conn, domain.AccessViewEdit) // upsert, bukan baris kedua

	var n int
	var level string
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*), max(access_level) FROM group_database_access WHERE group_id = $1`, g).Scan(&n, &level); err != nil {
		t.Fatal(err)
	}
	if n != 1 || level != "view_edit" {
		t.Fatalf("grant upsert: n=%d level=%s", n, level)
	}
}

func TestPgDriver_Integration_WithTx(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	pool := testutil.NewTargetDB(t, `CREATE TABLE a (id int); INSERT INTO a VALUES (1);`)
	d := testutil.NewPgDriver(pool)
	count := func() string {
		var out string
		err := d.WithTx(ctx, dbdriver.TxOptions{ReadOnly: true}, func(tx dbdriver.Tx) error {
			rs, err := tx.Query(ctx, `SELECT count(*) FROM a`)
			out = rs.Rows[0][0].Value
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	// Dua statement dalam satu Exec, lalu commit.
	if err := d.WithTx(ctx, dbdriver.TxOptions{}, func(tx dbdriver.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO a VALUES (2); INSERT INTO a VALUES (3);`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := count(); got != "3" {
		t.Fatalf("setelah commit count = %s, mau 3", got)
	}

	// Statement kedua gagal: statement pertama ikut ter-rollback.
	err := d.WithTx(ctx, dbdriver.TxOptions{}, func(tx dbdriver.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO a VALUES (4); INSERT INTO tidak_ada VALUES (1);`)
		return err
	})
	if err == nil {
		t.Fatal("harus error")
	}
	if got := count(); got != "3" {
		t.Fatalf("setelah rollback count = %s, mau 3", got)
	}

	// ErrForceRollback: rollback tanpa error.
	if err := d.WithTx(ctx, dbdriver.TxOptions{}, func(tx dbdriver.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO a VALUES (5)`); err != nil {
			return err
		}
		return dbdriver.ErrForceRollback
	}); err != nil {
		t.Fatalf("ErrForceRollback harus nil, dapat %v", err)
	}
	if got := count(); got != "3" {
		t.Fatalf("setelah force rollback count = %s, mau 3", got)
	}

	// Transaction read-only menolak tulis.
	err = d.WithTx(ctx, dbdriver.TxOptions{ReadOnly: true}, func(tx dbdriver.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO a VALUES (6)`)
		return err
	})
	if err == nil {
		t.Fatal("read-only harus menolak INSERT")
	}
}

func TestFakeDriverProvider(t *testing.T) {
	p := testutil.NewFakeDriverProvider()
	if _, err := p.Get(context.Background(), 1); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, mau ErrNotFound", err)
	}
	p.Set(1, testutil.NewPgDriver(nil))
	if d, err := p.Get(context.Background(), 1); err != nil || d == nil {
		t.Fatalf("Get = %v, %v", d, err)
	}
}

func TestSpySessionAdmin(t *testing.T) {
	s := &testutil.SpySessionAdmin{}
	_ = s.RevokeAllForUser(context.Background(), 7)
	_ = s.MarkRotationNeeded(context.Background(), 1, 2)
	if r := s.Revoked(); len(r) != 1 || r[0] != 7 {
		t.Errorf("revoked = %v", r)
	}
	if r := s.RotationMarked(); len(r) != 2 {
		t.Errorf("rotation = %v", r)
	}
}
