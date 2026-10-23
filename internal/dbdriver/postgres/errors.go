package postgres

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"dbplatform/internal/domain"
)

//  ErrConnectionFailed dgn pesan utk ditampilkan
type connError struct {
	msg   string
	cause error
}

func (e *connError) Error() string { return e.msg }
func (e *connError) Cause() error { return e.cause }
func (e *connError) Is(target error) bool { return target == domain.ErrConnectionFailed }

//  mengubah error koneksi/driver jadi domain.ErrConnectionFailed
func ClassifyError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, domain.ErrConnectionFailed) {
		return err
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == "28P01" || pgErr.Code == "28000":
			return &connError{"Autentikasi database gagal", err}
		case pgErr.Code == "3D000":
			return &connError{"Database tidak ditemukan di server", err}
		case strings.HasPrefix(pgErr.Code, "08") || pgErr.Code == "57P03":
			return &connError{"Tidak dapat terhubung ke database", err}
		case pgErr.Code == "57014":
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

	var connErr *pgconn.ConnectError
	var opErr *net.OpError
	var dnsErr *net.DNSError
	if errors.As(err, &connErr) || errors.As(err, &opErr) || errors.As(err, &dnsErr) {
		return &connError{"Tidak dapat terhubung ke database (host tidak terjangkau)", err}
	}
	return err
}
