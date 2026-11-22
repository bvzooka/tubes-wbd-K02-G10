package mysql

import (
	"crypto/tls"
	"crypto/x509"
	"dbplatform/internal/domain"
	"errors"
	gomysql "github.com/go-sql-driver/mysql"
	"net"
	"strconv"
	"strings"
)

// BuildConfig menyusun konfigurasi koneksi MySQL dari data koneksi, ssl_mode ala PostgreSQL diterjemahkan ke pengaturan TLS-nya
func BuildConfig(c domain.Connection, password string) (*gomysql.Config, error) {
	cfg := gomysql.NewConfig()
	cfg.User = c.Username
	cfg.Passwd = password
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	cfg.DBName = c.DatabaseName
	cfg.Timeout = connectTimeout
	cfg.Collation = "utf8mb4_general_ci"
	// parameter disisipkan di sisi client, jadi satu query dikirim sekali jalan
	cfg.InterpolateParams = true
	cfg.ParseTime = false
	if err := applySSLMode(cfg, c.SSLMode); err != nil {
		return nil, err
	}
	return cfg, nil
}

// applySSLMode menerjemahkan mode SSL ke pengaturan TLS go-sql-driver
func applySSLMode(cfg *gomysql.Config, mode string) error {
	switch strings.TrimSpace(mode) {
	case "disable":
		cfg.TLSConfig = "false"
	case "allow", "prefer", "":
		cfg.TLSConfig = "preferred"
	case "require":
		cfg.TLSConfig = "skip-verify"
	case "verify-ca":
		cfg.TLS = verifyCAConfig(nil)
	case "verify-full":
		cfg.TLSConfig = "true"
	default:
		return errors.New("ssl mode tidak valid")
	}
	return nil
}

// verifyCAConfig memeriksa rantai sertifikat server tapi tidak nama hostnya, itulah beda verify-ca dengan verify-full
func verifyCAConfig(roots *x509.CertPool) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("server tidak mengirim sertifikat")
			}
			inter := x509.NewCertPool()
			for _, c := range cs.PeerCertificates[1:] {
				inter.AddCert(c)
			}
			_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter})
			return err
		},
	}
}

// quoteIdent memberi backtick pada nama dan menggandakan backtick di dalamnya, supaya aman dipakai di SQL
func quoteIdent(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

// memberi quote pada tiap nama lalu menggabungkannya dengan koma
func quoteIdents(ss []string) string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = quoteIdent(s)
	}
	return strings.Join(out, ", ")
}
