package migration

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// batas isian dan pesan error migration
const (
	maxNameChars = 120
	maxSQLBytes  = 256 * 1024

	msgNameInvalid     = "Nama migration harus 1 sampai 120 karakter."
	msgSQLEmpty        = "SQL tidak boleh kosong."
	msgSQLTooLarge     = "SQL maksimal 256 KB."
	msgSQLUnterminated = "SQL tidak valid: tanda kutip, komentar, atau dollar-quote belum ditutup."
	msgSQLTransaction  = "SQL tidak boleh berisi perintah kontrol transaksi (%s). Seluruh migration sudah dijalankan dalam satu transaction."
)

// BEGIN/COMMIT dilarang, semua migration udah satu transaction
func CheckSQL(sql string) string {
	stmts, err := Split(sql)
	if err != nil {
		return msgSQLUnterminated
	}
	if len(stmts) == 0 {
		return msgSQLEmpty
	}
	if tc, found := FirstTransactionControl(stmts); found {
		return fmt.Sprintf(msgSQLTransaction, describe(tc))
	}
	return ""
}

// describe menamai perintah transaksi untuk pesan error, START dan PREPARE selalu diikuti kata TRANSACTION
func describe(s Statement) string {
	if s.Second == "TRANSACTION" && (s.Keyword == "START" || s.Keyword == "PREPARE") {
		return s.Keyword + " TRANSACTION"
	}
	return s.Keyword
}

// Checksum adalah SHA-256 dari SQL, dipakai untuk mengetahui apakah isi migration berubah
func Checksum(sql string) string {
	sum := sha256.Sum256([]byte(sql))
	return hex.EncodeToString(sum[:])
}
