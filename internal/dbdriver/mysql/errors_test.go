package mysql

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	gomysql "github.com/go-sql-driver/mysql"
	"dbplatform/internal/domain"
)

func TestClassifyError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"password salah", &gomysql.MySQLError{Number: 1045, Message: "Access denied"}, "Autentikasi database gagal"},
		{"database tidak ada", &gomysql.MySQLError{Number: 1049, Message: "Unknown database"}, "Database tidak ditemukan di server"},
		{"akses database ditolak", &gomysql.MySQLError{Number: 1044}, "Akses ke database ditolak oleh server"},
		{"host tidak diizinkan", &gomysql.MySQLError{Number: 1130}, "Akses ke database ditolak oleh server"},
		{"terlalu banyak koneksi", &gomysql.MySQLError{Number: 1040}, "Tidak dapat terhubung ke database (terlalu banyak koneksi)"},
		{"query dihentikan", &gomysql.MySQLError{Number: 1317}, "Koneksi ke database melewati batas waktu"},
		{"max_execution_time", &gomysql.MySQLError{Number: 3024}, "Koneksi ke database melewati batas waktu"},
		{"deadline", context.DeadlineExceeded, "Koneksi ke database melewati batas waktu"},
		{"server tanpa SSL", gomysql.ErrNoTLS, "Tidak dapat terhubung ke database (server tidak mendukung SSL)"},
		{"koneksi ditolak", &net.OpError{Op: "dial", Err: errors.New("refused")}, "Tidak dapat terhubung ke database (host tidak terjangkau)"},
		{"dns", &net.DNSError{Name: "nohost", Err: "no such host"}, "Tidak dapat terhubung ke database (host tidak terjangkau)"},
		{"koneksi putus", gomysql.ErrInvalidConn, "Tidak dapat terhubung ke database (host tidak terjangkau)"},
		{"bad conn", driver.ErrBadConn, "Tidak dapat terhubung ke database (host tidak terjangkau)"},
		{"error sintaks dibiarkan", &gomysql.MySQLError{Number: 1064, Message: "syntax"}, ""},
		{"error biasa dibiarkan", errors.New("boom"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyError(tc.err)
			if tc.want == "" {
				if got != tc.err {
					t.Fatalf("got %v, want error asli", got)
				}
				if errors.Is(got, domain.ErrConnectionFailed) {
					t.Fatal("tidak boleh dianggap error koneksi")
				}
				return
			}
			if !errors.Is(got, domain.ErrConnectionFailed) {
				t.Fatalf("%v bukan ErrConnectionFailed", got)
			}
			if got.Error() != tc.want {
				t.Errorf("pesan = %q, want %q", got.Error(), tc.want)
			}
		})
	}
	if ClassifyError(nil) != nil {
		t.Error("nil harus tetap nil")
	}
}

func TestClassifyError_MessageNeverLeaksCause(t *testing.T) {
	cause := fmt.Errorf("dial tcp: user=root password=s3cr3t host=h: %w", &net.OpError{Op: "dial", Err: errors.New("refused")})
	got := ClassifyError(cause)
	if !errors.Is(got, domain.ErrConnectionFailed) {
		t.Fatal("harus ErrConnectionFailed")
	}
	if strings.Contains(got.Error(), "s3cr3t") {
		t.Errorf("pesan membocorkan password: %q", got.Error())
	}
	if again := ClassifyError(got); again != got {
		t.Error("klasifikasi ulang harus idempoten")
	}
}
