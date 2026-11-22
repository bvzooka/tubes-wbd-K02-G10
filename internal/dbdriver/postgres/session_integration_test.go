package postgres

import (
	"context"
	"errors"
	"testing"

	"dbplatform/internal/dbdriver"
	"dbplatform/internal/testutil"
)

// membuat driver di atas database target sementara dengan satu tabel kecil
func newSessionDriver(t *testing.T) *Driver {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test butuh Postgres (make up)")
	}
	pool := testutil.NewTargetDB(t, `
		CREATE TABLE items (id bigint PRIMARY KEY, qty bigint);
		INSERT INTO items VALUES (1, 10), (2, NULL);`)
	return NewFromPool(pool)
}

func openSession(t *testing.T, d *Driver) dbdriver.Session {
	t.Helper()
	s, err := d.OpenSession(context.Background())
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	return s
}

func TestSession_Integration_LockIsExclusiveBetweenSessions(t *testing.T) {
	ctx := context.Background()
	d := newSessionDriver(t)
	a, b := openSession(t, d), openSession(t, d)
	defer b.Close()

	got, err := a.TryLock(ctx, dbdriver.PlatformMigrationLockKey)
	if err != nil || !got {
		t.Fatalf("sesi a TryLock = %v, %v; mau true", got, err)
	}
	if got, err := b.TryLock(ctx, dbdriver.PlatformMigrationLockKey); err != nil || got {
		t.Fatalf("sesi b TryLock = %v, %v; mau false karena a memegang lock", got, err)
	}

	if err := a.Unlock(ctx, dbdriver.PlatformMigrationLockKey); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if got, err := b.TryLock(ctx, dbdriver.PlatformMigrationLockKey); err != nil || !got {
		t.Fatalf("sesi b TryLock setelah Unlock = %v, %v; mau true", got, err)
	}
	a.Close()
}

func TestSession_Integration_CloseReleasesLock(t *testing.T) {
	ctx := context.Background()
	d := newSessionDriver(t)
	a, b := openSession(t, d), openSession(t, d)
	defer b.Close()

	if got, err := a.TryLock(ctx, dbdriver.PlatformMigrationLockKey); err != nil || !got {
		t.Fatalf("sesi a TryLock = %v, %v", got, err)
	}
	a.Close() // tanpa Unlock: Close yang harus melepas lock

	if got, err := b.TryLock(ctx, dbdriver.PlatformMigrationLockKey); err != nil || !got {
		t.Fatalf("sesi b TryLock setelah a Close = %v, %v; mau true", got, err)
	}
}

func TestSession_Integration_UnlockWithoutLockFails(t *testing.T) {
	d := newSessionDriver(t)
	s := openSession(t, d)
	defer s.Close()

	if err := s.Unlock(context.Background(), dbdriver.PlatformMigrationLockKey); err == nil {
		t.Fatal("Unlock tanpa lock harus error")
	}
}

func TestSession_Integration_WithTxCommitAndRollback(t *testing.T) {
	ctx := context.Background()
	d := newSessionDriver(t)
	s := openSession(t, d)
	defer s.Close()
	opts := dbdriver.TxOptions{}

	// fn nil: commit
	err := s.WithTx(ctx, opts, func(tx dbdriver.ArgTx) error {
		_, err := tx.Exec(ctx, "INSERT INTO items VALUES (3, 30)")
		return err
	})
	if err != nil {
		t.Fatalf("WithTx commit: %v", err)
	}
	if n := countItems(t, s); n != 3 {
		t.Fatalf("setelah commit ada %d baris, mau 3", n)
	}

	// ErrForceRollback: rollback tanpa error
	err = s.WithTx(ctx, opts, func(tx dbdriver.ArgTx) error {
		if _, err := tx.Exec(ctx, "INSERT INTO items VALUES (4, 40)"); err != nil {
			return err
		}
		return dbdriver.ErrForceRollback
	})
	if err != nil {
		t.Fatalf("WithTx ErrForceRollback harus nil, dapat %v", err)
	}
	if n := countItems(t, s); n != 3 {
		t.Fatalf("setelah force rollback ada %d baris, mau 3", n)
	}

	// error biasa: rollback dan error diteruskan
	boom := errors.New("boom")
	err = s.WithTx(ctx, opts, func(tx dbdriver.ArgTx) error {
		if _, err := tx.Exec(ctx, "INSERT INTO items VALUES (5, 50)"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("WithTx error = %v, mau membungkus boom", err)
	}
	if n := countItems(t, s); n != 3 {
		t.Fatalf("setelah rollback ada %d baris, mau 3", n)
	}
}

func TestSession_Integration_LockSurvivesTransactions(t *testing.T) {
	ctx := context.Background()
	d := newSessionDriver(t)
	a, b := openSession(t, d), openSession(t, d)
	defer b.Close()
	defer a.Close()

	if got, err := a.TryLock(ctx, dbdriver.PlatformMigrationLockKey); err != nil || !got {
		t.Fatalf("TryLock = %v, %v", got, err)
	}
	// lock sesi harus tetap dipegang di antara transaction (dipakai antar batch)
	for i := 0; i < 2; i++ {
		if err := a.WithTx(ctx, dbdriver.TxOptions{}, func(dbdriver.ArgTx) error { return nil }); err != nil {
			t.Fatalf("WithTx: %v", err)
		}
	}
	if got, _ := b.TryLock(ctx, dbdriver.PlatformMigrationLockKey); got {
		t.Fatal("lock harus tetap dipegang a setelah transaction selesai")
	}
}

func TestArgTx_Integration_ExecArgs(t *testing.T) {
	ctx := context.Background()
	d := newSessionDriver(t)
	s := openSession(t, d)
	defer s.Close()

	var affected int64
	err := s.WithTx(ctx, dbdriver.TxOptions{}, func(tx dbdriver.ArgTx) error {
		var err error
		affected, err = tx.ExecArgs(ctx, "UPDATE items SET qty = qty + 1 WHERE id > $1 AND id <= $2", int64(0), int64(1))
		return err
	})
	if err != nil {
		t.Fatalf("ExecArgs: %v", err)
	}
	if affected != 1 {
		t.Fatalf("baris terdampak = %d, mau 1", affected)
	}
}

func TestArgTx_Integration_QueryInt(t *testing.T) {
	ctx := context.Background()
	d := newSessionDriver(t)
	s := openSession(t, d)
	defer s.Close()

	tests := []struct {
		name      string
		sql       string
		args      []any
		wantValue int64
		wantFound bool
	}{
		{"ada nilai", "SELECT qty FROM items WHERE id = $1", []any{int64(1)}, 10, true},
		{"nilai NULL", "SELECT qty FROM items WHERE id = $1", []any{int64(2)}, 0, false},
		{"tanpa baris", "SELECT qty FROM items WHERE id = $1", []any{int64(99)}, 0, false},
		{"agregat", "SELECT max(id) FROM items WHERE id <= $1", []any{int64(1)}, 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.WithTx(ctx, dbdriver.TxOptions{ReadOnly: true}, func(tx dbdriver.ArgTx) error {
				v, found, err := tx.QueryInt(ctx, tt.sql, tt.args...)
				if err != nil {
					return err
				}
				if v != tt.wantValue || found != tt.wantFound {
					t.Errorf("QueryInt = (%d, %v), mau (%d, %v)", v, found, tt.wantValue, tt.wantFound)
				}
				return nil
			})
			if err != nil {
				t.Fatalf("WithTx: %v", err)
			}
		})
	}
}

func TestArgTx_Integration_ExecStillAllowsMultipleStatements(t *testing.T) {
	ctx := context.Background()
	d := newSessionDriver(t)
	s := openSession(t, d)
	defer s.Close()

	err := s.WithTx(ctx, dbdriver.TxOptions{}, func(tx dbdriver.ArgTx) error {
		_, err := tx.Exec(ctx, "INSERT INTO items VALUES (7, 1); INSERT INTO items VALUES (8, 2)")
		return err
	})
	if err != nil {
		t.Fatalf("Exec banyak statement: %v", err)
	}
	if n := countItems(t, s); n != 4 {
		t.Fatalf("ada %d baris, mau 4", n)
	}
}

// countItems menghitung baris items lewat sesi yang sama
func countItems(t *testing.T, s dbdriver.Session) int64 {
	t.Helper()
	var n int64
	err := s.WithTx(context.Background(), dbdriver.TxOptions{ReadOnly: true}, func(tx dbdriver.ArgTx) error {
		var err error
		n, _, err = tx.QueryInt(context.Background(), "SELECT count(*) FROM items")
		return err
	})
	if err != nil {
		t.Fatalf("countItems: %v", err)
	}
	return n
}
