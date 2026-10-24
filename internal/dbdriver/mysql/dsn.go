package mysql

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strconv"
	"strings"
	gomysql "github.com/go-sql-driver/mysql"
	"dbplatform/internal/domain"
)

func BuildConfig(c domain.Connection, password string) (*gomysql.Config, error) {
	cfg := gomysql.NewConfig()
	cfg.User = c.Username
	cfg.Passwd = password
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	cfg.DBName = c.DatabaseName
	cfg.Timeout = connectTimeout
	cfg.Collation = "utf8mb4_general_ci"
	cfg.InterpolateParams = true
	cfg.ParseTime = false
	if err := applySSLMode(cfg, c.SSLMode); err != nil {
		return nil, err
	}
	return cfg, nil
}

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

func quoteIdent(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

func quoteIdents(ss []string) string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = quoteIdent(s)
	}
	return strings.Join(out, ", ")
}
