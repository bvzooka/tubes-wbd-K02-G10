package connmgr

import (
	"dbplatform/internal/domain"
	"strings"
	"unicode/utf8"
)

var validSSLModes = map[string]bool{
	"disable":     true,
	"allow":       true,
	"prefer":      true,
	"require":     true,
	"verify-ca":   true,
	"verify-full": true,
}

// buang space di awal dan akhir di field text (except passw)
func NormalizeInput(in domain.ConnectionInput) domain.ConnectionInput {
	in.Name = strings.TrimSpace(in.Name)
	in.Host = strings.TrimSpace(in.Host)
	in.DatabaseName = strings.TrimSpace(in.DatabaseName)
	in.Username = strings.TrimSpace(in.Username)
	in.SSLMode = strings.TrimSpace(in.SSLMode)
	return in
}

// periksa input koneksi, return nil if valid
// klo isUpdate true, password kosong boleh
func ValidateInput(in domain.ConnectionInput, isUpdate bool) error {
	in = NormalizeInput(in)
	fields := map[string]string{}
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > 64 {
		fields["name"] = "Nama harus 1 sampai 64 karakter."
	}

	switch in.DBMS {
	case domain.DBMSPostgres, domain.DBMSMySQL, domain.DBMSMariaDB:
	default:
		fields["dbms"] = "DBMS harus salah satu dari postgres, mysql, atau mariadb."
	}

	if in.Host == "" {
		fields["host"] = "Host tidak boleh kosong."
	}

	if in.Port < 1 || in.Port > 65535 {
		fields["port"] = "Port harus antara 1 sampai 65535."
	}

	if in.DatabaseName == "" {
		fields["database_name"] = "Nama database tidak boleh kosong."
	}
	if in.Username == "" {
		fields["db_username"] = "Username database tidak boleh kosong."
	}
	if !isUpdate && in.Password == "" {
		fields["password"] = "Password tidak boleh kosong."
	}
	if !validSSLModes[in.SSLMode] {
		fields["ssl_mode"] = "SSL mode tidak valid."
	}
	if len(fields) > 0 {
		return &domain.ValidationError{Fields: fields}
	}
	return nil
}
