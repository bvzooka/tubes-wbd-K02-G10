package migration

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"dbplatform/internal/domain"
)

const (
	defaultSchema    = "public"
	defaultBatchSize = 10_000
	maxBatchSize     = 100_000

	msgModeInvalid    = "Mode harus normal atau batch."
	msgNotInTx        = "Perintah %s tidak bisa dijalankan di dalam transaction."
	msgDownRequired   = "Isi SQL down, atau tandai irreversible."
	msgReasonRequired = "Tulis alasan kenapa migration ini irreversible."
	msgBatchOne       = "Mode batch hanya boleh berisi satu statement UPDATE, INSERT, atau DELETE."
	msgBatchRange     = "SQL batch harus memakai $1 sebagai batas bawah dan $2 sebagai batas atas."
	msgBatchTable     = "Nama tabel wajib diisi."
	msgBatchKey       = "Kolom kunci wajib diisi."
	msgBatchSize      = "Ukuran batch harus 1 sampai %d."
)

var (
	concurrentlyPattern = regexp.MustCompile(`(?i)\b(index|table|schema|database|system)\s+concurrently\b`)
	lowerBoundPattern   = regexp.MustCompile(`\$1(\D|$)`)
	upperBoundPattern   = regexp.MustCompile(`\$2(\D|$)`)
)

type DraftInput struct {
	Name               string
	UpSQL              string
	DownSQL            string
	Irreversible       bool
	IrreversibleReason string
	Mode               domain.MigrationMode
	Batch              *domain.BatchConfig // nil kalau mode normal
}

// cleanDraft dipakai saat menyimpan, jadi SQL down dan pengaturan batch masih boleh belum lengkap
func cleanDraft(in DraftInput) (DraftInput, map[string]string) {
	fields := map[string]string{}
	in.Name = strings.TrimSpace(in.Name)
	in.UpSQL = strings.TrimSpace(in.UpSQL)
	in.DownSQL = strings.TrimSpace(in.DownSQL)
	in.IrreversibleReason = strings.TrimSpace(in.IrreversibleReason)

	if n := utf8.RuneCountInString(in.Name); n < 1 || n > maxNameChars {
		fields["name"] = msgNameInvalid
	}
	if msg := checkScript(in.UpSQL); msg != "" {
		fields["sql_up"] = msg
	}
	// irreversible dan SQL down saling meniadakan supaya tidak ada dua jawaban soal bisa dirollback
	if in.Irreversible {
		in.DownSQL = ""
	} else {
		in.IrreversibleReason = ""
	}
	if in.DownSQL != "" {
		if msg := checkScript(in.DownSQL); msg != "" {
			fields["sql_down"] = msg
		}
	}

	switch in.Mode {
	case "", domain.ModeNormal:
		in.Mode, in.Batch = domain.ModeNormal, nil
	case domain.ModeBatch:
		in.Batch = cleanBatch(in.Batch)
	default:
		fields["mode"] = msgModeInvalid
	}
	return in, fields
}

// cleanBatch menyalin dulu supaya nilai milik pemanggil tidak ikut berubah
func cleanBatch(in *domain.BatchConfig) *domain.BatchConfig {
	var b domain.BatchConfig
	if in != nil {
		b = *in
	}
	b.Schema = strings.TrimSpace(b.Schema)
	b.Table = strings.TrimSpace(b.Table)
	b.KeyColumn = strings.TrimSpace(b.KeyColumn)
	if b.Schema == "" {
		b.Schema = defaultSchema
	}
	if b.BatchSize == 0 {
		b.BatchSize = defaultBatchSize
	}
	return &b
}

// checkScript berisi aturan yang berlaku untuk SQL up maupun down di semua mode
func checkScript(sql string) string {
	if sql == "" {
		return msgSQLEmpty
	}
	if len(sql) > maxSQLBytes {
		return msgSQLTooLarge
	}
	if msg := CheckSQL(sql); msg != "" {
		return msg
	}
	// error dari Split sudah ditangani CheckSQL di atas
	stmts, _ := Split(sql)
	for _, s := range stmts {
		if command, found := outsideTxOnly(s); found {
			return fmt.Sprintf(msgNotInTx, command)
		}
	}
	return ""
}

// outsideTxOnly mengenali perintah yang ditolak PostgreSQL kalau ada di dalam transaction
func outsideTxOnly(s Statement) (string, bool) {
	switch {
	case s.Keyword == "VACUUM":
		return "VACUUM", true
	case s.Keyword == "ALTER" && s.Second == "SYSTEM":
		return "ALTER SYSTEM", true
	case (s.Keyword == "CREATE" || s.Keyword == "DROP") && (s.Second == "DATABASE" || s.Second == "TABLESPACE"):
		return s.Keyword + " " + s.Second, true
	case concurrentlyPattern.MatchString(s.Text):
		return "CONCURRENTLY", true
	}
	return "", false
}

// checkReady adalah syarat tambahan saat freeze, dipanggil dengan input hasil cleanDraft
func checkReady(in DraftInput) map[string]string {
	fields := map[string]string{}
	switch {
	case in.Irreversible && in.IrreversibleReason == "":
		fields["irreversible_reason"] = msgReasonRequired
	case !in.Irreversible && in.DownSQL == "":
		fields["sql_down"] = msgDownRequired
	}
	if in.Mode != domain.ModeBatch {
		return fields
	}

	if in.Batch.Table == "" {
		fields["batch.table"] = msgBatchTable
	}
	if in.Batch.KeyColumn == "" {
		fields["batch.key_column"] = msgBatchKey
	}
	if in.Batch.BatchSize < 1 || in.Batch.BatchSize > maxBatchSize {
		fields["batch.batch_size"] = fmt.Sprintf(msgBatchSize, maxBatchSize)
	}
	if msg := checkBatchScript(in.UpSQL); msg != "" {
		fields["sql_up"] = msg
	}
	if in.DownSQL != "" {
		if msg := checkBatchScript(in.DownSQL); msg != "" {
			fields["sql_down"] = msg
		}
	}
	return fields
}

// checkBatchScript memastikan platform bisa mengatur sendiri rentang baris tiap batch lewat $1 dan $2
func checkBatchScript(sql string) string {
	stmts, err := Split(sql)
	if err != nil || len(stmts) != 1 {
		return msgBatchOne
	}
	switch stmts[0].Keyword {
	case "UPDATE", "INSERT", "DELETE":
	default:
		return msgBatchOne
	}
	if !lowerBoundPattern.MatchString(sql) || !upperBoundPattern.MatchString(sql) {
		return msgBatchRange
	}
	return ""
}

// draftChecksum mencakup semua yang menentukan apa yang dijalankan, nama tidak ikut
func draftChecksum(in DraftInput) string {
	// dibungkus JSON supaya batas antar bagian jelas dan dua isi berbeda tidak menghasilkan teks yang sama
	content, _ := json.Marshal(struct {
		Up    string
		Down  string
		Mode  domain.MigrationMode
		Batch *domain.BatchConfig
	}{in.UpSQL, in.DownSQL, in.Mode, in.Batch})
	return Checksum(string(content))
}
