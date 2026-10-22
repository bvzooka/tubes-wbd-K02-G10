package explain

import (
	"errors"
	"testing"

	"dbplatform/internal/domain"
)

func TestScanSQL(t *testing.T) {
	tests := []struct {
		name      string
		sql       string
		keyword   string
		stmts     int
		body      string
		innerSemi bool
	}{
		{"select sederhana", "SELECT 1", "select", 1, "SELECT 1", false},
		{"titik koma penutup", "select * from t;  ", "select", 1, "select * from t", false},
		{"komentar di depan", "-- catatan\n/* blok */ UPDATE t SET a = 1", "update", 1, "-- catatan\n/* blok */ UPDATE t SET a = 1", false},
		{"komentar penutup dibuang", "SELECT 1; -- akhir", "select", 1, "SELECT 1", false},
		{"dua statement", "SELECT 1; DROP TABLE t", "select", 2, "SELECT 1; DROP TABLE t", true},
		{"titik koma di string", "SELECT ';DROP TABLE t'", "select", 1, "SELECT ';DROP TABLE t'", false},
		{"kutip ganda escape", "SELECT 'it''s; ok'", "select", 1, "SELECT 'it''s; ok'", false},
		{"E-string backslash", `SELECT E'a\'; b'`, "select", 1, `SELECT E'a\'; b'`, false},
		{"identifier ber-quote", `SELECT "a;b" FROM t`, "select", 1, `SELECT "a;b" FROM t`, false},
		{"dollar quote", "SELECT $$a;b$$", "select", 1, "SELECT $$a;b$$", false},
		{"dollar quote bertag", "SELECT $x$ ; $$ ; $x$", "select", 1, "SELECT $x$ ; $$ ; $x$", false},
		{"parameter $1 bukan dollar quote", "SELECT $1", "select", 1, "SELECT $1", false},
		{"komentar bersarang", "/* a /* b; */ c; */ DELETE FROM t", "delete", 1, "/* a /* b; */ c; */ DELETE FROM t", false},
		{"titik koma kosong di depan", "; SELECT 1", "select", 1, "; SELECT 1", true},
		{"hanya komentar", "-- kosong", "", 0, "", false},
		{"with", "WITH x AS (SELECT 1) SELECT * FROM x", "with", 1, "WITH x AS (SELECT 1) SELECT * FROM x", false},
		{"ddl", "create table t(a int)", "create", 1, "create table t(a int)", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := scanSQL(tc.sql)
			if got.FirstKeyword != tc.keyword || got.Statements != tc.stmts || got.Body != tc.body || got.InnerSemicolon != tc.innerSemi {
				t.Fatalf("scanSQL(%q) = %+v", tc.sql, got)
			}
		})
	}
}

func TestValidateSQL(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		kind error
	}{
		{"select", "SELECT * FROM t", nil},
		{"insert", "insert into t values (1);", nil},
		{"values", "VALUES (1), (2)", nil},
		{"kosong", "   ", domain.ErrValidation},
		{"komentar saja", "/* x */", domain.ErrValidation},
		{"multi statement", "SELECT 1; SELECT 2", domain.ErrValidation},
		{"ddl ditolak", "CREATE TABLE x (a int)", domain.ErrUnsupported},
		{"drop ditolak", "DROP TABLE t", domain.ErrUnsupported},
		{"explain bersarang ditolak", "EXPLAIN SELECT 1", domain.ErrUnsupported},
		{"kontrol transaksi ditolak", "COMMIT", domain.ErrUnsupported},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateSQL(tc.sql)
			if tc.kind == nil {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if !errors.Is(err, tc.kind) || userMessage(err) == "" {
				t.Fatalf("err = %v, want %v dengan pesan", err, tc.kind)
			}
		})
	}
	big := make([]byte, maxSQLBytes+1)
	for i := range big {
		big[i] = ' '
	}
	copy(big, "SELECT 1")
	if _, err := validateSQL(string(big)); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("SQL terlalu panjang: %v", err)
	}
}
