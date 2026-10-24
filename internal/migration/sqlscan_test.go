package migration

import (
	"errors"
	"strings"
	"testing"
)

func texts(stmts []Statement) []string {
	out := make([]string, len(stmts))
	for i, s := range stmts {
		out[i] = s.Text
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSplit_Statements(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want []string
	}{
		{"kosong", "", []string{}},
		{"hanya spasi", "  \n\t ", []string{}},
		{"satu statement tanpa titik koma", "SELECT 1", []string{"SELECT 1"}},
		{"satu statement dengan titik koma", "SELECT 1;", []string{"SELECT 1"}},
		{"dua statement", "SELECT 1; SELECT 2;", []string{"SELECT 1", "SELECT 2"}},
		{"titik koma berlebih", ";; SELECT 1 ;;; SELECT 2 ;", []string{"SELECT 1", "SELECT 2"}},
		{"banyak baris", "CREATE TABLE a (\n  id int\n);\nINSERT INTO a VALUES (1);\n", []string{"CREATE TABLE a (\n  id int\n)", "INSERT INTO a VALUES (1)"}},
		{"hanya komentar", "-- tidak ada apa-apa\n/* atau ini */", []string{}},
		{"komentar di antara statement", "SELECT 1; -- selesai\nSELECT 2", []string{"SELECT 1", "-- selesai\nSELECT 2"}},

		{"titik koma di string", "INSERT INTO t VALUES ('a;b'); SELECT 1", []string{"INSERT INTO t VALUES ('a;b')", "SELECT 1"}},
		{"kutip ganda di dalam string", "SELECT 'it''s; ok'; SELECT 2", []string{"SELECT 'it''s; ok'", "SELECT 2"}},
		{"titik koma di identifier ber-kutip", `SELECT "a;b" FROM t; SELECT 2`, []string{`SELECT "a;b" FROM t`, "SELECT 2"}},
		{"kutip ganda ganda di identifier", `SELECT "a""b;c" FROM t; SELECT 2`, []string{`SELECT "a""b;c" FROM t`, "SELECT 2"}},
		{"titik koma di komentar baris", "SELECT 1 -- a; b\n; SELECT 2", []string{"SELECT 1 -- a; b", "SELECT 2"}},
		{"titik koma di komentar blok", "SELECT 1 /* a; b */; SELECT 2", []string{"SELECT 1 /* a; b */", "SELECT 2"}},
		{"komentar blok bersarang", "SELECT 1 /* a /* b; */ c; */; SELECT 2", []string{"SELECT 1 /* a /* b; */ c; */", "SELECT 2"}},
		{"komentar baris di akhir tanpa newline", "SELECT 1; -- akhir", []string{"SELECT 1"}},

		{"dollar quote kosong", "CREATE FUNCTION f() RETURNS int AS $$ SELECT 1; SELECT 2; $$ LANGUAGE sql; SELECT 3",
			[]string{"CREATE FUNCTION f() RETURNS int AS $$ SELECT 1; SELECT 2; $$ LANGUAGE sql", "SELECT 3"}},
		{"dollar quote bertag", "DO $fn$ BEGIN PERFORM 1; END $fn$; SELECT 2", []string{"DO $fn$ BEGIN PERFORM 1; END $fn$", "SELECT 2"}},
		{"dollar quote bersarang beda tag", "SELECT $a$ x; $b$ y; $b$ z; $a$; SELECT 2", []string{"SELECT $a$ x; $b$ y; $b$ z; $a$", "SELECT 2"}},
		{"parameter $1 bukan dollar quote", "SELECT $1; SELECT $2", []string{"SELECT $1", "SELECT $2"}},
		{"identifier berisi dolar", "SELECT foo$bar$ FROM t; SELECT 2", []string{"SELECT foo$bar$ FROM t", "SELECT 2"}},
		{"dolar sendirian", "SELECT 1 $ 2; SELECT 3", []string{"SELECT 1 $ 2", "SELECT 3"}},

		{"string E dengan escape backslash", `SELECT E'it\'s; x'; SELECT 2`, []string{`SELECT E'it\'s; x'`, "SELECT 2"}},
		{"string biasa memperlakukan backslash apa adanya", `SELECT 'a\'; SELECT 2`, []string{`SELECT 'a\'`, "SELECT 2"}},
		{"huruf e pada akhir identifier bukan prefix E", `SELECT name'x'; SELECT 2`, []string{`SELECT name'x'`, "SELECT 2"}},
		{"string unicode", "SELECT 'é;ü'; SELECT 2", []string{"SELECT 'é;ü'", "SELECT 2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Split(tt.sql)
			if err != nil {
				t.Fatal(err)
			}
			if !equal(texts(got), tt.want) {
				t.Errorf("Split(%q)\n  got  %q\n  want %q", tt.sql, texts(got), tt.want)
			}
		})
	}
}

func TestSplit_Keywords(t *testing.T) {
	tests := []struct {
		name       string
		sql        string
		wantFirst  string
		wantSecond string
	}{
		{"huruf kecil", "select 1", "SELECT", ""},
		{"dua kata", "start transaction", "START", "TRANSACTION"},
		{"dengan komentar di depan", "-- catatan\nBEGIN", "BEGIN", ""},
		{"dengan komentar blok di depan", "/* x */ commit", "COMMIT", ""},
		{"spasi dan baris baru di depan", "\n\n  Create table t (id int)", "CREATE", "TABLE"},
		{"diawali kurung", "(SELECT 1)", "", ""},
		{"diawali identifier ber-kutip", `"begin" x`, "", ""},
		{"diawali string", "'begin'", "", ""},
		{"kata kedua berupa simbol", "COMMIT;", "COMMIT", ""},
		{"kata kedua bukan kata", "SELECT (1)", "SELECT", ""},
		{"diawali angka", "1 + 1", "", ""},
		{"kata kedua berupa angka", "select 1", "SELECT", ""},
		{"kata kedua dengan awalan garis bawah", "select _x", "SELECT", "_X"},
		{"tanda pisah", "PREPARE\n\tTRANSACTION 'x'", "PREPARE", "TRANSACTION"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Split(tt.sql)
			if err != nil || len(got) != 1 {
				t.Fatalf("Split = %v, %v", got, err)
			}
			if got[0].Keyword != tt.wantFirst || got[0].Second != tt.wantSecond {
				t.Errorf("keyword = %q %q, mau %q %q", got[0].Keyword, got[0].Second, tt.wantFirst, tt.wantSecond)
			}
		})
	}
}

func TestSplit_Unterminated(t *testing.T) {
	tests := map[string]string{
		"string":                      "SELECT 'abc",
		"string E":                    `SELECT E'abc\'`,
		"identifier":                  `SELECT "abc`,
		"komentar blok":               "SELECT 1 /* abc",
		"komentar blok bersarang":     "SELECT 1 /* a /* b */",
		"dollar quote":                "SELECT $$ abc",
		"dollar quote bertag":         "SELECT $t$ abc $x$",
		"string di statement kedua":   "SELECT 1; SELECT 'abc",
		"string berisi titik koma":    "INSERT INTO t VALUES ('a;b",
		"kutip ganda di ujung string": "SELECT 'abc''",
	}
	for name, sql := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Split(sql); !errors.Is(err, ErrUnterminated) {
				t.Errorf("err = %v, mau ErrUnterminated", err)
			}
		})
	}
}

func TestIsTransactionControl_Detected(t *testing.T) {
	for _, sql := range []string{
		"BEGIN", "begin", "BEGIN TRANSACTION", "BEGIN ISOLATION LEVEL SERIALIZABLE",
		"COMMIT", "commit", "COMMIT PREPARED 'x'",
		"ROLLBACK", "ROLLBACK TO SAVEPOINT a", "ROLLBACK PREPARED 'x'",
		"SAVEPOINT a", "RELEASE SAVEPOINT a", "RELEASE a",
		"START TRANSACTION", "start transaction read only",
		"PREPARE TRANSACTION 'x'",
		"END", "END TRANSACTION", "ABORT",
		"-- komentar\nBEGIN", "/* x */ COMMIT", "\n  rollback  ",
	} {
		t.Run(sql, func(t *testing.T) {
			stmts, err := Split(sql)
			if err != nil || len(stmts) != 1 {
				t.Fatalf("Split = %v, %v", stmts, err)
			}
			if !IsTransactionControl(stmts[0]) {
				t.Errorf("%q harus terdeteksi sebagai kontrol transaksi", sql)
			}
		})
	}
}

func TestIsTransactionControl_NotDetected(t *testing.T) {
	for _, sql := range []string{
		"SELECT 'BEGIN'",
		"SELECT 'COMMIT; ROLLBACK'",
		"-- BEGIN\nSELECT 1",
		"/* COMMIT */ SELECT 1",
		`SELECT 1 AS "begin"`,
		`SELECT "commit" FROM t`,
		"SELECT CASE WHEN a THEN 1 ELSE 2 END FROM t",
		"UPDATE t SET x = CASE WHEN a THEN 1 END",
		"INSERT INTO t (begin_at, end_at) VALUES (now(), now())",
		"SELECT * FROM begin_table",
		"CREATE TABLE commit_log (id int)",
		"DO $$ BEGIN PERFORM 1; COMMIT; END $$",
		"CREATE FUNCTION f() RETURNS int AS $$ BEGIN RETURN 1; END; $$ LANGUAGE plpgsql",
		"CREATE FUNCTION f() RETURNS int AS 'BEGIN; ROLLBACK;' LANGUAGE plpgsql",
		"PREPARE stmt AS SELECT 1",
		"START",
		"SET TRANSACTION READ ONLY",
		"SELECT E'it\\'s; COMMIT'",
	} {
		t.Run(sql, func(t *testing.T) {
			stmts, err := Split(sql)
			if err != nil {
				t.Fatal(err)
			}
			if s, found := FirstTransactionControl(stmts); found {
				t.Errorf("tidak boleh terdeteksi, tapi statement %q terdeteksi", s.Text)
			}
		})
	}
}

func TestFirstTransactionControl_FindsLaterStatement(t *testing.T) {
	stmts, err := Split("CREATE TABLE a (id int);\nINSERT INTO a VALUES (1);\nCOMMIT;\nCREATE TABLE b (id int)")
	if err != nil {
		t.Fatal(err)
	}
	s, found := FirstTransactionControl(stmts)
	if !found || s.Text != "COMMIT" {
		t.Errorf("found = %v, statement = %q, mau COMMIT", found, s.Text)
	}
}

func TestFirstTransactionControl_NoneInPlainMigration(t *testing.T) {
	stmts, err := Split(strings.Join([]string{
		"CREATE TABLE a (id int PRIMARY KEY);",
		"ALTER TABLE a ADD COLUMN note text DEFAULT 'begin; commit';",
		"CREATE INDEX a_note_idx ON a (note);",
	}, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, found := FirstTransactionControl(stmts); found {
		t.Error("migration biasa tidak boleh terdeteksi")
	}
}

func TestContainsDDL(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want bool
	}{
		{"create", "CREATE TABLE a (id int)", true},
		{"alter", "alter table a add column b int", true},
		{"drop", "DROP TABLE a", true},
		{"truncate", "TRUNCATE a", true},
		{"rename", "RENAME TABLE a TO b", true},
		{"ddl di statement kedua", "INSERT INTO a VALUES (1); DROP TABLE a", true},
		{"hanya dml", "INSERT INTO a VALUES (1); UPDATE a SET id = 2; DELETE FROM a; SELECT 1", false},
		{"kata ddl di dalam string", "INSERT INTO a VALUES ('CREATE TABLE x')", false},
		{"kata ddl di komentar", "-- DROP TABLE a\nSELECT 1", false},
		{"kosong", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmts, err := Split(tt.sql)
			if err != nil {
				t.Fatal(err)
			}
			if got := ContainsDDL(stmts); got != tt.want {
				t.Errorf("ContainsDDL = %v, mau %v", got, tt.want)
			}
		})
	}
}
