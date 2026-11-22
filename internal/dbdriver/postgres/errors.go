package postgres

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"dbplatform/internal/domain"
)

// connError membawa pesan aman untuk ditampilkan, penyebab aslinya disimpan di cause dan tidak ikut tampil.
// Method Is membuat errors.Is(err, domain.ErrConnectionFailed) bernilai true.
type connError struct {
	msg   string
	cause error
}

func (e *connError) Error() string        { return e.msg }
func (e *connError) Cause() error         { return e.cause }
func (e *connError) Is(target error) bool { return target == domain.ErrConnectionFailed }

// SQLSTATE PostgreSQL yang dikenali, lihat https://www.postgresql.org/docs/current/errcodes-appendix.html
const (
	sqlstateInvalidPassword      = "28P01"
	sqlstateInvalidAuthorization = "28000"
	sqlstateInvalidCatalogName   = "3D000"
	sqlstateConnectionClassPfx   = "08"
	sqlstateCannotConnectNow     = "57P03"
	sqlstateQueryCanceled        = "57014"
)

// ClassifyError mengubah error koneksi atau driver menjadi domain.ErrConnectionFailed dengan pesan yang aman
// ditampilkan. Error lain (misalnya kesalahan SQL) dikembalikan apa adanya.
func ClassifyError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, domain.ErrConnectionFailed) {
		return err
	}

	// error dari server PostgreSQL punya kode SQLSTATE, dipetakan ke pesan yang aman ditampilkan
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == sqlstateInvalidPassword || pgErr.Code == sqlstateInvalidAuthorization:
			return &connError{"Autentikasi database gagal", err}
		case pgErr.Code == sqlstateInvalidCatalogName:
			return &connError{"Database tidak ditemukan di server", err}
		case strings.HasPrefix(pgErr.Code, sqlstateConnectionClassPfx) || pgErr.Code == sqlstateCannotConnectNow:
			return &connError{"Tidak dapat terhubung ke database", err}
		case pgErr.Code == sqlstateQueryCanceled:
			return &connError{"Koneksi ke database melewati batas waktu", err}
		}
		return err
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return &connError{"Koneksi ke database melewati batas waktu", err}
	}
	// error di luar server: timeout dan host tidak terjangkau
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
