package migration

import (
	"strings"
	"testing"
)

func TestValidate_Name(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"valid", "tambah tabel customers", "tambah tabel customers", false},
		{"dirapikan", "  tambah kolom  ", "tambah kolom", false},
		{"satu karakter", "a", "a", false},
		{"batas atas", strings.Repeat("a", 120), strings.Repeat("a", 120), false},
		{"unicode dihitung per karakter", strings.Repeat("é", 120), strings.Repeat("é", 120), false},
		{"kosong", "", "", true},
		{"hanya spasi", "   \t", "", true},
		{"terlalu panjang", strings.Repeat("a", 121), strings.Repeat("a", 121), true},
		{"unicode terlalu panjang", strings.Repeat("é", 121), strings.Repeat("é", 121), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, fields := Validate(Input{Name: tt.in, SQL: "SELECT 1"})
			if got.Name != tt.want {
				t.Errorf("nama = %q, mau %q", got.Name, tt.want)
			}
			if (fields["name"] != "") != tt.wantErr {
				t.Errorf("pesan nama = %q, wantErr = %v", fields["name"], tt.wantErr)
			}
			if fields["sql"] != "" {
				t.Errorf("SQL valid tidak boleh memberi error: %q", fields["sql"])
			}
		})
	}
}

func TestValidate_SQLLimits(t *testing.T) {
	exactly := "SELECT '" + strings.Repeat("a", maxSQLBytes-len("SELECT ''")) + "'"
	if len(exactly) != maxSQLBytes {
		t.Fatalf("panjang fixture = %d", len(exactly))
	}
	tests := []struct {
		name string
		sql  string
		want string
	}{
		{"valid", "CREATE TABLE a (id int)", ""},
		{"dirapikan", "\n  SELECT 1;  \n", ""},
		{"tepat 256 KB", exactly, ""},
		{"lebih dari 256 KB", exactly + "x", msgSQLTooLarge},
		{"kosong", "", msgSQLEmpty},
		{"hanya spasi", " \n\t ", msgSQLEmpty},
		{"hanya komentar", "-- tidak ada apa-apa", msgSQLEmpty},
		{"hanya titik koma", ";;;", msgSQLEmpty},
		{"string tak ditutup", "SELECT 'abc", msgSQLUnterminated},
		{"komentar blok tak ditutup", "SELECT 1 /* abc", msgSQLUnterminated},
		{"dollar quote tak ditutup", "DO $$ BEGIN", msgSQLUnterminated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, fields := Validate(Input{Name: "ok", SQL: tt.sql})
			if fields["sql"] != tt.want {
				t.Errorf("pesan sql = %q, mau %q", fields["sql"], tt.want)
			}
		})
	}
}

func TestValidate_TransactionControlRejected(t *testing.T) {
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
			_, fields := Validate(Input{Name: "ok", SQL: tt.sql})
			if !strings.Contains(fields["sql"], "kontrol transaksi") || !strings.Contains(fields["sql"], "("+tt.want+")") {
				t.Errorf("pesan sql = %q, mau memuat kontrol transaksi (%s)", fields["sql"], tt.want)
			}
		})
	}
}

func TestValidate_LegitimateSQLNotMistakenForTransactionControl(t *testing.T) {
	for _, sql := range []string{
		"INSERT INTO log (msg) VALUES ('BEGIN; COMMIT;')",
		"-- COMMIT\nSELECT 1",
		"CREATE FUNCTION f() RETURNS int AS $$ BEGIN RETURN 1; END; $$ LANGUAGE plpgsql",
		"SELECT CASE WHEN a THEN 1 ELSE 2 END FROM t",
		"ALTER TABLE a ADD COLUMN begin_at timestamptz",
	} {
		t.Run(sql, func(t *testing.T) {
			if _, fields := Validate(Input{Name: "ok", SQL: sql}); fields["sql"] != "" {
				t.Errorf("SQL sah ditolak: %q", fields["sql"])
			}
		})
	}
}

func TestValidate_ReportsAllFieldsAtOnce(t *testing.T) {
	_, fields := Validate(Input{Name: "", SQL: ""})
	if fields["name"] == "" || fields["sql"] == "" {
		t.Errorf("fields = %v, mau name dan sql terisi", fields)
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
