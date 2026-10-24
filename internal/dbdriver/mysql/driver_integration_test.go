package mysql

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

func TestDriver_Integration_PingAndMetadata(t *testing.T) {
	d := newTestDriver(t)
	if err := d.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if d.Dialect() != testDBMS || d.Capabilities().TransactionalDDL {
		t.Errorf("dialect=%v caps=%+v", d.Dialect(), d.Capabilities())
	}

	other := domain.DBMSMariaDB
	if testDBMS == domain.DBMSMariaDB {
		other = domain.DBMSMySQL
	}
	c := testConn()
	c.DBMS = other
	m := newDriverFor(t, c, testPass)
	if m.Dialect() != other {
		t.Errorf("Dialect = %v", m.Dialect())
	}
}

func TestDriver_Integration_WrongPasswordAndUnknownDatabase(t *testing.T) {
	skipIfNoServer(t)
	_ = newTestDriver(t)

	c := testConn()
	_, err := New(context.Background(), c, "password-salah-123")
	if !isErr(err, domain.ErrConnectionFailed) || err.Error() != "Autentikasi database gagal" {
		t.Fatalf("password salah: %v", err)
	}
	if strings.Contains(err.Error(), "password-salah-123") {
		t.Error("pesan membocorkan password")
	}

	c.DatabaseName = "database_yang_tidak_ada"
	_, err = New(context.Background(), c, testPass)
	if !isErr(err, domain.ErrConnectionFailed) {
		t.Fatalf("database tidak ada: %v", err)
	}
	if got := err.Error(); got != "Database tidak ditemukan di server" && got != "Akses ke database ditolak oleh server" {
		t.Errorf("pesan = %q", got)
	}
}

func TestDriver_Integration_SpecialCharacterCredentials(t *testing.T) {
	d := newTestDriver(t)
	db := rawDB(t, d)

	user := uniqueName("u")
	const pass = `p@ss:w/rd#?&=%'"\ ;` + "`" + `é`
	if _, err := db.Exec("CREATE USER ?@'%' IDENTIFIED BY ?", user, pass); err != nil {
		t.Fatalf("buat user: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DROP USER IF EXISTS ?@'%'", user) })
	if _, err := db.Exec(fmt.Sprintf("GRANT SELECT ON %s.* TO ?@'%%'", quoteIdent(testDB)), user); err != nil {
		t.Fatalf("grant: %v", err)
	}

	c := testConn()
	c.Username = user
	drv := newDriverFor(t, c, pass)
	if err := drv.Ping(context.Background()); err != nil {
		t.Fatalf("Ping dengan password khusus: %v", err)
	}
}

func TestDriver_Integration_WithTxCommitAndRollback(t *testing.T) {
	d := newTestDriver(t)
	tbl := scratchTable(t, d, "CREATE TABLE %s (id INT PRIMARY KEY, v VARCHAR(20)) ENGINE=InnoDB")
	ctx := context.Background()
	ins := func(tx dbdriver.Tx, id int) error {
		_, err := tx.Exec(ctx, fmt.Sprintf("INSERT INTO %s VALUES (%d, 'x')", quoteIdent(tbl), id))
		return err
	}

	if err := d.WithTx(ctx, dbdriver.TxOptions{}, func(tx dbdriver.Tx) error { return ins(tx, 1) }); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if n := countRows(t, d, tbl); n != 1 {
		t.Fatalf("setelah commit baris = %d, want 1", n)
	}

	sentinel := errors.New("gagal sengaja")
	err := d.WithTx(ctx, dbdriver.TxOptions{}, func(tx dbdriver.Tx) error {
		if err := ins(tx, 2); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error fn harus terbungkus, got %v", err)
	}
	if n := countRows(t, d, tbl); n != 1 {
		t.Errorf("setelah rollback karena error baris = %d, want 1", n)
	}

	err = d.WithTx(ctx, dbdriver.TxOptions{}, func(tx dbdriver.Tx) error {
		if err := ins(tx, 3); err != nil {
			return err
		}
		return dbdriver.ErrForceRollback
	})
	if err != nil {
		t.Fatalf("ErrForceRollback harus menghasilkan nil, got %v", err)
	}
	if n := countRows(t, d, tbl); n != 1 {
		t.Errorf("setelah ErrForceRollback baris = %d, want 1", n)
	}
}

func TestDriver_Integration_WithTxPanicRollsBack(t *testing.T) {
	d := newTestDriver(t)
	tbl := scratchTable(t, d, "CREATE TABLE %s (id INT PRIMARY KEY) ENGINE=InnoDB")

	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic harus diteruskan ke pemanggil")
			}
		}()
		_ = d.WithTx(context.Background(), dbdriver.TxOptions{}, func(tx dbdriver.Tx) error {
			_, _ = tx.Exec(context.Background(), fmt.Sprintf("INSERT INTO %s VALUES (1)", quoteIdent(tbl)))
			panic("boom")
		})
	}()
	if n := countRows(t, d, tbl); n != 0 {
		t.Errorf("panic harus rollback, baris = %d", n)
	}

	if err := d.Ping(context.Background()); err != nil {
		t.Errorf("Ping setelah panic: %v", err)
	}
}

func TestDriver_Integration_ReadOnlyTxRejectsWrites(t *testing.T) {
	d := newTestDriver(t)
	tbl := scratchTable(t, d, "CREATE TABLE %s (id INT PRIMARY KEY) ENGINE=InnoDB")
	err := d.WithTx(context.Background(), dbdriver.TxOptions{ReadOnly: true}, func(tx dbdriver.Tx) error {
		_, err := tx.Exec(context.Background(), fmt.Sprintf("INSERT INTO %s VALUES (1)", quoteIdent(tbl)))
		return err
	})
	if err == nil {
		t.Fatal("INSERT di transaction READ ONLY harus ditolak")
	}
	if n := countRows(t, d, tbl); n != 0 {
		t.Errorf("baris = %d, want 0", n)
	}
}

func TestDriver_Integration_MultiStatementsOnlyOnWritePath(t *testing.T) {
	d := newTestDriver(t)
	tbl := scratchTable(t, d, "CREATE TABLE %s (id INT PRIMARY KEY) ENGINE=InnoDB")
	ctx := context.Background()
	two := fmt.Sprintf("INSERT INTO %[1]s VALUES (1); INSERT INTO %[1]s VALUES (2), (3)", quoteIdent(tbl))

	err := d.WithTx(ctx, dbdriver.TxOptions{ReadOnly: true}, func(tx dbdriver.Tx) error {
		_, err := tx.Exec(ctx, two)
		return err
	})
	if err == nil {
		t.Fatal("jalur baca tidak boleh menjalankan dua statement")
	}
	if n := countRows(t, d, tbl); n != 0 {
		t.Fatalf("baris = %d, want 0", n)
	}

	var affected int64
	err = d.WithTx(ctx, dbdriver.TxOptions{}, func(tx dbdriver.Tx) error {
		var err error
		affected, err = tx.Exec(ctx, two)
		return err
	})
	if err != nil {
		t.Fatalf("jalur tulis: %v", err)
	}
	if affected != 2 {
		t.Errorf("baris terdampak perintah terakhir = %d, want 2", affected)
	}
	if n := countRows(t, d, tbl); n != 3 {
		t.Errorf("baris = %d, want 3", n)
	}
	if d.rw == nil || d.ro == d.rw {
		t.Error("pool tulis harus terpisah dari pool baca")
	}
}

func TestDriver_Integration_TxQueryReturnsTextCells(t *testing.T) {
	d := newTestDriver(t)
	ctx := context.Background()
	err := d.WithTx(ctx, dbdriver.TxOptions{ReadOnly: true}, func(tx dbdriver.Tx) error {
		rs, err := tx.Query(ctx, "SELECT 1 AS a, NULL AS b, 'teks' AS c, '' AS d, 3.50 AS e, UNHEX('DEADBEEF') AS f")
		if err != nil {
			return err
		}
		if strings.Join(rs.Columns, ",") != "a,b,c,d,e,f" || len(rs.Rows) != 1 {
			t.Fatalf("hasil = %+v", rs)
		}
		want := []domain.Cell{{Value: "1"}, {Null: true}, {Value: "teks"}, {Value: ""}, {Value: "3.50"}, {Value: "0xdeadbeef"}}
		for i, w := range want {
			if rs.Rows[0][i] != w {
				t.Errorf("kolom %s = %+v, want %+v", rs.Columns[i], rs.Rows[0][i], w)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDriver_Integration_QueryRejectsSecondStatement(t *testing.T) {
	d := newTestDriver(t)
	tbl := scratchTable(t, d, "CREATE TABLE %s (id INT PRIMARY KEY) ENGINE=InnoDB")
	ctx := context.Background()
	err := d.WithTx(ctx, dbdriver.TxOptions{}, func(tx dbdriver.Tx) error {
		_, err := tx.Query(ctx, fmt.Sprintf("SELECT 1; INSERT INTO %s VALUES (9)", quoteIdent(tbl)))
		if !errors.Is(err, domain.ErrUnsupported) {
			t.Errorf("err = %v, want ErrUnsupported", err)
		}
		return dbdriver.ErrForceRollback
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, d, tbl); n != 0 {
		t.Errorf("statement kedua tidak boleh tersimpan, baris = %d", n)
	}
}

func TestDriver_Integration_TxTimeout(t *testing.T) {
	d := newTestDriver(t)
	ctx := context.Background()
	start := time.Now()
	err := d.WithTx(ctx, dbdriver.TxOptions{ReadOnly: true, Timeout: 300 * time.Millisecond}, func(tx dbdriver.Tx) error {
		_, err := tx.Query(ctx, "SELECT COUNT(*) FROM information_schema.COLUMNS a, information_schema.COLUMNS b, information_schema.COLUMNS c")
		return err
	})
	if err == nil {
		t.Fatal("query lambat harus gagal karena batas waktu")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("batas waktu tidak berlaku, butuh %v", elapsed)
	}
}

func TestDriver_Integration_CloseIsIdempotentAndBlocksWrites(t *testing.T) {
	d := newTestDriver(t)
	_ = d.WithTx(context.Background(), dbdriver.TxOptions{}, func(tx dbdriver.Tx) error { return nil })
	d.Close()
	d.Close()
	err := d.WithTx(context.Background(), dbdriver.TxOptions{}, func(tx dbdriver.Tx) error { return nil })
	if err == nil {
		t.Error("WithTx setelah Close harus gagal")
	}
}
