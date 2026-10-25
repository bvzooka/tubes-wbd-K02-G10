package mysql

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
)

func TestNew_RejectsOtherDBMS(t *testing.T) {
	_, err := New(context.Background(), domain.Connection{DBMS: domain.DBMSPostgres}, "x")
	if !errors.Is(err, domain.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestNew_UnreachableHostIsConnectionFailedWithoutPassword(t *testing.T) {
	c := domain.Connection{DBMS: domain.DBMSMySQL, Host: "127.0.0.1", Port: 1, DatabaseName: "d", Username: "u", SSLMode: "disable"}
	drv, err := New(context.Background(), c, "super-rahasia")
	if err == nil {
		drv.Close()
		t.Fatal("seharusnya gagal")
	}
	if !errors.Is(err, domain.ErrConnectionFailed) {
		t.Fatalf("err = %v, want ErrConnectionFailed", err)
	}
	if strings.Contains(err.Error(), "super-rahasia") {
		t.Errorf("pesan membocorkan password: %v", err)
	}
}

func TestNew_InvalidSSLModeIsRejectedBeforeConnecting(t *testing.T) {
	c := domain.Connection{DBMS: domain.DBMSMariaDB, Host: "127.0.0.1", Port: 1, SSLMode: "ngawur"}
	if _, err := New(context.Background(), c, "x"); err == nil || errors.Is(err, domain.ErrConnectionFailed) {
		t.Fatalf("err = %v, want error konfigurasi", err)
	}
}

func TestDriver_CapabilitiesAndDialect(t *testing.T) {
	for _, dbms := range []domain.DBMS{domain.DBMSMySQL, domain.DBMSMariaDB} {
		d := &Driver{dbms: dbms}
		if d.Dialect() != dbms {
			t.Errorf("Dialect = %v, want %v", d.Dialect(), dbms)
		}
		if d.Capabilities().TransactionalDDL {
			t.Error("TransactionalDDL harus false untuk MySQL/MariaDB")
		}
	}
	var _ dbdriver.Driver = (*Driver)(nil)
}

func TestToCell(t *testing.T) {
	cases := []struct {
		name   string
		in     sql.RawBytes
		binary bool
		want   domain.Cell
	}{
		{"NULL", nil, false, domain.Cell{Null: true}},
		{"string kosong bukan NULL", sql.RawBytes(""), false, domain.Cell{Value: ""}},
		{"teks", sql.RawBytes("halo ✓"), false, domain.Cell{Value: "halo ✓"}},
		{"biner jadi hex", sql.RawBytes{0xde, 0xad, 0xbe, 0xef}, true, domain.Cell{Value: "0xdeadbeef"}},
		{"bukan UTF-8 jadi hex", sql.RawBytes{0xff, 0xfe}, false, domain.Cell{Value: "0xfffe"}},
		{"NULL biner tetap NULL", nil, true, domain.Cell{Null: true}},
	}
	for _, tc := range cases {
		if got := toCell(tc.in, tc.binary); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestIsBinaryType(t *testing.T) {
	for _, n := range []string{"BLOB", "BINARY", "VARBINARY", "BIT", "GEOMETRY"} {
		if !isBinaryType(n) {
			t.Errorf("%s harus biner", n)
		}
	}
	for _, n := range []string{"TEXT", "VARCHAR", "CHAR", "INT", "JSON", "DECIMAL", "DATETIME", ""} {
		if isBinaryType(n) {
			t.Errorf("%s bukan biner", n)
		}
	}
}

func TestHasMultipleStatements(t *testing.T) {
	single := []string{
		"SELECT 1",
		"SELECT 1;",
		"SELECT 1;  \n\t ",
		"SELECT ';' AS x",
		"SELECT \"a;b\", 'it''s; fine', `we;ird`",
		`SELECT 'esc\'; still string'`,
		"SELECT 1; -- komentar saja",
		"SELECT 1; # komentar saja",
		"SELECT 1; /* komentar saja */",
		"SELECT /* a; b */ 1",
		"SELECT 1 -- a; b\n",
		"SELECT 1 # a; b\n",
		"",
	}
	for _, s := range single {
		if hasMultipleStatements(s) {
			t.Errorf("satu statement dianggap banyak: %q", s)
		}
	}
	multi := []string{
		"SELECT 1; SELECT 2",
		"SELECT 1;SELECT 2",
		"SELECT 1;\nDROP TABLE x",
		"SELECT 'a'; INSERT INTO t VALUES (1)",
		"SELECT 1; /* c */ SELECT 2",
		"SELECT 1; -- c\nSELECT 2",
		"SELECT 1;;SELECT 2",
		"SELECT 1; 'x'",
	}
	for _, s := range multi {
		if !hasMultipleStatements(s) {
			t.Errorf("banyak statement lolos: %q", s)
		}
	}
}
