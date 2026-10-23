package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
)

func count(t *testing.T, d *Driver, table string) int {
	t.Helper()
	var n int
	if err := d.pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDriver_Metadata(t *testing.T) {
	d := NewFromPool(nil)
	if d.Dialect() != domain.DBMSPostgres {
		t.Error("Dialect harus postgres")
	}
	if !d.Capabilities().TransactionalDDL {
		t.Error("PostgreSQL harus TransactionalDDL")
	}
}

func TestNew_RejectsOtherDBMS(t *testing.T) {
	_, err := New(context.Background(), domain.Connection{DBMS: domain.DBMSMySQL}, "x")
	if !errors.Is(err, domain.ErrUnsupported) {
		t.Fatalf("mau ErrUnsupported, dapat %v", err)
	}
}

func TestNew_Integration_Unreachable(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	// Port 1 hampir pasti tertutup.
	c := domain.Connection{DBMS: domain.DBMSPostgres, Host: "127.0.0.1", Port: 1,
		DatabaseName: "x", Username: "u", SSLMode: "disable"}
	_, err := New(context.Background(), c, "TopSecretPw")
	if !errors.Is(err, domain.ErrConnectionFailed) {
		t.Fatalf("mau ErrConnectionFailed, dapat %v", err)
	}
	if strings.Contains(err.Error(), "TopSecretPw") {
		t.Errorf("pesan membocorkan password: %v", err)
	}
}

func TestDriver_Integration_PingAndClose(t *testing.T) {
	d := newBlankDriver(t)
	if err := d.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	d.Close()
	if err := d.Ping(context.Background()); err == nil {
		t.Error("Ping setelah Close harus gagal")
	}
}

func TestWithTx_Integration_Commit(t *testing.T) {
	d := newBlankDriver(t, "CREATE TABLE t (id int)")
	err := d.WithTx(context.Background(), dbdriver.TxOptions{}, func(tx dbdriver.Tx) error {
		_, err := tx.Exec(context.Background(), "INSERT INTO t VALUES (1)")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := count(t, d, "t"); n != 1 {
		t.Fatalf("baris = %d, mau 1 (commit)", n)
	}
}

func TestWithTx_Integration_RollbackOnError(t *testing.T) {
	d := newBlankDriver(t, "CREATE TABLE t (id int)")
	boom := errors.New("boom")
	err := d.WithTx(context.Background(), dbdriver.TxOptions{}, func(tx dbdriver.Tx) error {
		if _, err := tx.Exec(context.Background(), "INSERT INTO t VALUES (1)"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("error harus diteruskan (terbungkus): %v", err)
	}
	if n := count(t, d, "t"); n != 0 {
		t.Fatalf("baris = %d, mau 0 (rollback)", n)
	}
}

func TestWithTx_Integration_ForceRollback(t *testing.T) {
	d := newBlankDriver(t, "CREATE TABLE t (id int)")
	err := d.WithTx(context.Background(), dbdriver.TxOptions{}, func(tx dbdriver.Tx) error {
		if _, err := tx.Exec(context.Background(), "INSERT INTO t VALUES (1)"); err != nil {
			return err
		}
		return dbdriver.ErrForceRollback
	})
	if err != nil {
		t.Fatalf("ErrForceRollback harus menghasilkan nil, dapat %v", err)
	}
	if n := count(t, d, "t"); n != 0 {
		t.Fatalf("baris = %d, mau 0 (rollback paksa)", n)
	}
}

func TestWithTx_Integration_PanicRollsBack(t *testing.T) {
	d := newBlankDriver(t, "CREATE TABLE t (id int)")
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic harus diteruskan")
			}
		}()
		_ = d.WithTx(context.Background(), dbdriver.TxOptions{}, func(tx dbdriver.Tx) error {
			_, _ = tx.Exec(context.Background(), "INSERT INTO t VALUES (1)")
			panic("kaboom")
		})
	}()
	if n := count(t, d, "t"); n != 0 {
		t.Fatalf("baris = %d, mau 0 (rollback saat panic)", n)
	}
	// Koneksi dikembalikan ke pool dengan benar: pool masih bisa dipakai.
	if err := d.Ping(context.Background()); err != nil {
		t.Fatalf("pool rusak setelah panic: %v", err)
	}
}

func TestWithTx_Integration_ReadOnlyRejectsWrite(t *testing.T) {
	d := newBlankDriver(t, "CREATE TABLE t (id int)")
	err := d.WithTx(context.Background(), dbdriver.TxOptions{ReadOnly: true}, func(tx dbdriver.Tx) error {
		_, err := tx.Exec(context.Background(), "INSERT INTO t VALUES (1)")
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("INSERT di tx read-only harus ditolak, dapat %v", err)
	}
	if n := count(t, d, "t"); n != 0 {
		t.Fatalf("baris = %d, mau 0", n)
	}
}

func TestWithTx_Integration_DDLRollsBack(t *testing.T) {
	d := newBlankDriver(t)
	_ = d.WithTx(context.Background(), dbdriver.TxOptions{}, func(tx dbdriver.Tx) error {
		if _, err := tx.Exec(context.Background(), "CREATE TABLE baru (id int)"); err != nil {
			return err
		}
		return dbdriver.ErrForceRollback
	})
	var exists bool
	if err := d.pool.QueryRow(context.Background(), "SELECT to_regclass('baru') IS NOT NULL").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("DDL harus ikut di-rollback")
	}
}

func TestWithTx_Integration_Timeout(t *testing.T) {
	d := newBlankDriver(t)
	start := time.Now()
	err := d.WithTx(context.Background(), dbdriver.TxOptions{Timeout: 200 * time.Millisecond}, func(tx dbdriver.Tx) error {
		_, err := tx.Exec(context.Background(), "SELECT pg_sleep(5)")
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "statement timeout") {
		t.Fatalf("harus kena statement_timeout, dapat %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Error("timeout tidak diterapkan")
	}
}

func TestTx_Integration_ExecMultiStatement(t *testing.T) {
	d := newBlankDriver(t)
	var affected int64
	err := d.WithTx(context.Background(), dbdriver.TxOptions{}, func(tx dbdriver.Tx) error {
		var err error
		affected, err = tx.Exec(context.Background(),
			"CREATE TABLE m (id int); INSERT INTO m VALUES (1), (2), (3); UPDATE m SET id = id + 10 WHERE id > 1")
		return err
	})
	if err != nil {
		t.Fatalf("multi-statement harus jalan: %v", err)
	}
	if affected != 2 {
		t.Errorf("RowsAffected perintah terakhir = %d, mau 2", affected)
	}
	if n := count(t, d, "m"); n != 3 {
		t.Errorf("baris = %d, mau 3", n)
	}
}

func TestTx_Integration_MultiStatementFailureRollsBackAll(t *testing.T) {
	d := newBlankDriver(t)
	err := d.WithTx(context.Background(), dbdriver.TxOptions{}, func(tx dbdriver.Tx) error {
		_, err := tx.Exec(context.Background(), "CREATE TABLE a (id int); INSERT INTO tidak_ada VALUES (1)")
		return err
	})
	if err == nil {
		t.Fatal("statement kedua yang salah harus menggagalkan eksekusi")
	}
	var exists bool
	_ = d.pool.QueryRow(context.Background(), "SELECT to_regclass('a') IS NOT NULL").Scan(&exists)
	if exists {
		t.Error("statement pertama harus ikut di-rollback")
	}
}

func TestTx_Integration_Query(t *testing.T) {
	d := newBlankDriver(t)
	err := d.WithTx(context.Background(), dbdriver.TxOptions{ReadOnly: true}, func(tx dbdriver.Tx) error {
		rs, err := tx.Query(context.Background(),
			`SELECT 1 AS satu, NULL::text AS kosong, 'a''b'::text AS teks, true AS ok, '{"k":1}'::json AS js, '\xdead'::bytea AS b`)
		if err != nil {
			return err
		}
		if fmt.Sprint(rs.Columns) != "[satu kosong teks ok js b]" {
			t.Errorf("kolom = %v", rs.Columns)
		}
		row := rs.Rows[0]
		if row[0].Value != "1" || row[0].Null {
			t.Errorf("satu = %+v", row[0])
		}
		if !row[1].Null {
			t.Errorf("NULL harus Null=true: %+v", row[1])
		}
		if row[2].Value != "a'b" {
			t.Errorf("teks = %+v", row[2])
		}
		if row[4].Value != `{"k":1}` {
			t.Errorf("json harus teks JSON: %+v", row[4])
		}
		if row[5].Value != `\xdead` {
			t.Errorf("bytea harus hex: %+v", row[5])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTx_Integration_QueryRejectsMultiStatement(t *testing.T) {
	d := newBlankDriver(t)
	err := d.WithTx(context.Background(), dbdriver.TxOptions{ReadOnly: true}, func(tx dbdriver.Tx) error {
		_, err := tx.Query(context.Background(), "SELECT 1; SELECT 2")
		return err
	})
	if err == nil {
		t.Fatal("Query harus menolak lebih dari satu statement")
	}
}
