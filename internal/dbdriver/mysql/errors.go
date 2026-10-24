package mysql

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql/driver"
	"errors"
	"net"
	"strings"
	gomysql "github.com/go-sql-driver/mysql"
	"dbplatform/internal/domain"
)

type connError struct {
	msg string
	cause error
}

func (e *connError) Error() string { return e.msg }
func (e *connError) Cause() error { return e.cause }
func (e *connError) Unwrap() error { return e.cause }
func (e *connError) Is(target error) bool { return target == domain.ErrConnectionFailed }

const (
	erAccessDenied = 1045 
	erDBAccessDenied = 1044 
	erBadDB = 1049 
	erTooManyConn = 1040 
	erHostNotAllowed = 1130 
	erQueryInterrupted = 1317 
	erMaxExecTime = 3024 
	erNoSuchTable = 1146 
)

func ClassifyError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, domain.ErrConnectionFailed) {
		return err
	}

	var me *gomysql.MySQLError
	if errors.As(err, &me) {
		switch me.Number {
		case erAccessDenied:
			return &connError{"Autentikasi database gagal", err}
		case erDBAccessDenied, erHostNotAllowed:
			return &connError{"Akses ke database ditolak oleh server", err}
		case erBadDB:
			return &connError{"Database tidak ditemukan di server", err}
		case erTooManyConn:
			return &connError{"Tidak dapat terhubung ke database (terlalu banyak koneksi)", err}
		case erQueryInterrupted, erMaxExecTime:
			return &connError{"Koneksi ke database melewati batas waktu", err}
		}
		return err
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return &connError{"Koneksi ke database melewati batas waktu", err}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &connError{"Koneksi ke database melewati batas waktu", err}
	}
	if errors.Is(err, gomysql.ErrNoTLS) {
		return &connError{"Tidak dapat terhubung ke database (server tidak mendukung SSL)", err}
	}

	var certVerify *tls.CertificateVerificationError
	var unknownAuth x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var invalidCert x509.CertificateInvalidError
	if errors.As(err, &certVerify) || errors.As(err, &unknownAuth) || errors.As(err, &hostErr) || errors.As(err, &invalidCert) {
		return &connError{"Sertifikat SSL server tidak dapat diverifikasi", err}
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Err != nil && strings.Contains(opErr.Err.Error(), "tls:") {
		return &connError{"Negosiasi SSL dengan server gagal (versi TLS atau cipher tidak cocok)", err}
	}

	var dnsErr *net.DNSError
	if errors.As(err, &opErr) || errors.As(err, &dnsErr) ||
		errors.Is(err, gomysql.ErrInvalidConn) || errors.Is(err, driver.ErrBadConn) {
		return &connError{"Tidak dapat terhubung ke database (host tidak terjangkau)", err}
	}
	return err
}
