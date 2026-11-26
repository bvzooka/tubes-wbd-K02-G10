package migration

import (
	"strings"
	"testing"
)

func TestCheckSQL_Messages(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want string
	}{
		{"valid", "CREATE TABLE a (id int)", ""},
		{"kosong", "", msgSQLEmpty},
		{"hanya komentar", "-- tidak ada apa-apa", msgSQLEmpty},
		{"hanya titik koma", ";;;", msgSQLEmpty},
		{"string tak ditutup", "SELECT 'abc", msgSQLUnterminated},
		{"komentar blok tak ditutup", "SELECT 1 /* abc", msgSQLUnterminated},
		{"dollar quote tak ditutup", "DO $$ BEGIN", msgSQLUnterminated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CheckSQL(tt.sql); got != tt.want {
				t.Errorf("pesan = %q, mau %q", got, tt.want)
			}
		})
	}
}

func TestCheckSQL_TransactionControlRejected(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want string
	}{
		{"commit", "CREATE TABLE a (id int); COMMIT;", "COMMIT"},
		{"begin di awal", "BEGIN; CREATE TABLE a (id int);", "BEGIN"},
		{"rollback", "INSERT INTO a VALUES (1); rollback", "ROLLBACK"},
		{"savepoint", "SAVEPOINT x; SELECT 1", "SAVEPOINT"},
		{"start transaction", "START TRANSACTION; SELECT 1", "START TRANSACTION"},
		{"prepare transaction", "PREPARE TRANSACTION 'x'", "PREPARE TRANSACTION"},
		{"end", "SELECT 1; END", "END"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CheckSQL(tt.sql)
			if !strings.Contains(got, "kontrol transaksi") || !strings.Contains(got, "("+tt.want+")") {
				t.Errorf("pesan = %q, mau memuat kontrol transaksi (%s)", got, tt.want)
			}
		})
	}
}

func TestCheckSQL_LegitimateSQLNotMistakenForTransactionControl(t *testing.T) {
	for _, sql := range []string{
		"INSERT INTO log (msg) VALUES ('BEGIN; COMMIT;')",
		"-- COMMIT\nSELECT 1",
		"CREATE FUNCTION f() RETURNS int AS $$ BEGIN RETURN 1; END; $$ LANGUAGE plpgsql",
		"SELECT CASE WHEN a THEN 1 ELSE 2 END FROM t",
		"ALTER TABLE a ADD COLUMN begin_at timestamptz",
	} {
		t.Run(sql, func(t *testing.T) {
			if got := CheckSQL(sql); got != "" {
				t.Errorf("SQL sah ditolak: %q", got)
			}
		})
	}
}

func TestChecksum(t *testing.T) {
	if got := Checksum("abc"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf("Checksum(abc) = %s", got)
	}
	if Checksum("SELECT 1") != Checksum("SELECT 1") {
		t.Error("checksum harus deterministik")
	}
	if Checksum("SELECT 1") == Checksum("SELECT 2") {
		t.Error("SQL berbeda harus menghasilkan checksum berbeda")
	}
	if len(Checksum("")) != 64 {
		t.Error("checksum harus 64 karakter hex")
	}
}
