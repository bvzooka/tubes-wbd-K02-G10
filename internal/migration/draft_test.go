package migration

import (
	"strings"
	"testing"

	"dbplatform/internal/domain"
)

// validDraft adalah titik awal yang lolos, tiap kasus tinggal merusak satu bagian
func validDraft() DraftInput {
	return DraftInput{
		Name:    "tambah kolom phone",
		UpSQL:   "ALTER TABLE customers ADD COLUMN phone TEXT",
		DownSQL: "ALTER TABLE customers DROP COLUMN phone",
	}
}

func validBatchDraft() DraftInput {
	return DraftInput{
		Name:    "isi kolom total",
		UpSQL:   "UPDATE orders SET total = price * qty WHERE id > $1 AND id <= $2",
		DownSQL: "UPDATE orders SET total = NULL WHERE id > $1 AND id <= $2",
		Mode:    domain.ModeBatch,
		Batch:   &domain.BatchConfig{Table: "orders", KeyColumn: "id", BatchSize: 500},
	}
}

// bigSQL membuat SELECT yang panjangnya persis sebanyak n byte
func bigSQL(n int) string {
	return "SELECT '" + strings.Repeat("a", n-len("SELECT ''")) + "'"
}

// field kosong artinya draft harus boleh disimpan
func TestCleanDraft_Rules(t *testing.T) {
	tests := []struct {
		name   string
		change func(*DraftInput)
		field  string
	}{
		{"valid", func(*DraftInput) {}, ""},
		{"nama kosong", func(in *DraftInput) { in.Name = "  " }, "name"},
		{"nama 120 karakter unicode", func(in *DraftInput) { in.Name = strings.Repeat("é", maxNameChars) }, ""},
		{"nama terlalu panjang", func(in *DraftInput) { in.Name = strings.Repeat("é", maxNameChars+1) }, "name"},
		{"sql up kosong", func(in *DraftInput) { in.UpSQL = "" }, "sql_up"},
		{"sql up tepat batas", func(in *DraftInput) { in.UpSQL = bigSQL(maxSQLBytes) }, ""},
		{"sql up terlalu besar", func(in *DraftInput) { in.UpSQL = bigSQL(maxSQLBytes + 1) }, "sql_up"},
		{"sql down boleh kosong dulu", func(in *DraftInput) { in.DownSQL = "" }, ""},
		{"kontrol transaksi", func(in *DraftInput) { in.UpSQL = "BEGIN; SELECT 1; COMMIT" }, "sql_up"},
		{"vacuum", func(in *DraftInput) { in.UpSQL = "VACUUM customers" }, "sql_up"},
		{"index concurrently", func(in *DraftInput) { in.UpSQL = "CREATE INDEX CONCURRENTLY i ON customers (id)" }, "sql_up"},
		{"unique index concurrently", func(in *DraftInput) { in.UpSQL = "CREATE UNIQUE INDEX CONCURRENTLY i ON customers (id)" }, "sql_up"},
		{"refresh view concurrently boleh", func(in *DraftInput) { in.UpSQL = "REFRESH MATERIALIZED VIEW CONCURRENTLY v" }, ""},
		{"sql down ikut dicek", func(in *DraftInput) { in.DownSQL = "DROP DATABASE toko" }, "sql_down"},
		{"mode aneh", func(in *DraftInput) { in.Mode = "kilat" }, "mode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := validDraft()
			tt.change(&in)
			_, fields := cleanDraft(in)
			assertOnlyField(t, fields, tt.field)
		})
	}
}

func TestCleanDraft_NormalizesInput(t *testing.T) {
	in := validDraft()
	in.Irreversible = true
	in.IrreversibleReason = " datanya hilang "
	got, _ := cleanDraft(in)
	if got.Mode != domain.ModeNormal || got.DownSQL != "" || got.IrreversibleReason != "datanya hilang" {
		t.Errorf("hasil = %+v", got)
	}

	batch := validBatchDraft()
	batch.Batch = &domain.BatchConfig{Table: " orders ", KeyColumn: "id"}
	got, _ = cleanDraft(batch)
	if got.Batch.Schema != "public" || got.Batch.Table != "orders" || got.Batch.BatchSize != defaultBatchSize {
		t.Errorf("batch = %+v", got.Batch)
	}
	if batch.Batch.Table != " orders " {
		t.Error("input asli ikut berubah")
	}
}

func TestCheckReady_Rules(t *testing.T) {
	tests := []struct {
		name   string
		base   func() DraftInput
		change func(*DraftInput)
		field  string
	}{
		{"normal lengkap", validDraft, func(*DraftInput) {}, ""},
		{"tanpa down", validDraft, func(in *DraftInput) { in.DownSQL = "" }, "sql_down"},
		{"irreversible tanpa alasan", validDraft, func(in *DraftInput) { in.Irreversible = true }, "irreversible_reason"},
		{"irreversible dengan alasan", validDraft, func(in *DraftInput) { in.Irreversible, in.IrreversibleReason = true, "data hilang" }, ""},
		{"batch lengkap", validBatchDraft, func(*DraftInput) {}, ""},
		{"batch tanpa tabel", validBatchDraft, func(in *DraftInput) { in.Batch.Table = "" }, "batch.table"},
		{"batch tanpa kolom kunci", validBatchDraft, func(in *DraftInput) { in.Batch.KeyColumn = "" }, "batch.key_column"},
		{"batch terlalu besar", validBatchDraft, func(in *DraftInput) { in.Batch.BatchSize = maxBatchSize + 1 }, "batch.batch_size"},
		{"batch dua statement", validBatchDraft, func(in *DraftInput) { in.UpSQL += "; DELETE FROM orders WHERE id > $1 AND id <= $2" }, "sql_up"},
		{"batch bukan dml", validBatchDraft, func(in *DraftInput) { in.UpSQL = "ALTER TABLE orders ADD COLUMN x INT" }, "sql_up"},
		{"batch tanpa batas atas", validBatchDraft, func(in *DraftInput) { in.UpSQL = "UPDATE orders SET total = 0 WHERE id > $1" }, "sql_up"},
		{"dollar sepuluh bukan dollar satu", validBatchDraft, func(in *DraftInput) { in.UpSQL = "UPDATE orders SET total = $10 WHERE id <= $2" }, "sql_up"},
		{"batch down ikut dicek", validBatchDraft, func(in *DraftInput) { in.DownSQL = "UPDATE orders SET total = NULL" }, "sql_down"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := tt.base()
			tt.change(&in)
			cleaned, fields := cleanDraft(in)
			if len(fields) > 0 {
				t.Fatalf("cleanDraft menolak: %v", fields)
			}
			assertOnlyField(t, checkReady(cleaned), tt.field)
		})
	}
}

func TestDraftChecksum_Changes(t *testing.T) {
	base := validBatchDraft()
	renamed := validBatchDraft()
	renamed.Name = "nama lain"
	if draftChecksum(base) != draftChecksum(renamed) {
		t.Error("ganti nama tidak boleh mengubah checksum")
	}
	resized := validBatchDraft()
	resized.Batch.BatchSize = 900
	if draftChecksum(base) == draftChecksum(resized) {
		t.Error("ganti ukuran batch harus mengubah checksum")
	}
}

// assertOnlyField memastikan error muncul tepat di field yang diharapkan, atau tidak ada sama sekali
func assertOnlyField(t *testing.T, fields map[string]string, want string) {
	t.Helper()
	if want == "" {
		if len(fields) != 0 {
			t.Errorf("harusnya lolos, dapat %v", fields)
		}
		return
	}
	if len(fields) != 1 || fields[want] == "" {
		t.Errorf("field error = %v, mau %s", fields, want)
	}
}
