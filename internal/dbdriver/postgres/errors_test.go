package postgres

import (
	"context"
	"dbplatform/internal/domain"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"net"
	"strings"
	"testing"
)

type timeoutErr struct{}

func (timeoutErr) Error() string { return "i/o timeout" }
func (timeoutErr) Timeout() bool { return true }
func (timeoutErr) Temporary() bool { return true }

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name string
		err error
		wantMsg string 
	}{
		{"password salah", &pgconn.PgError{Code: "28P01", Message: "password authentication failed"}, "Autentikasi database gagal"},
		{"auth invalid", &pgconn.PgError{Code: "28000"}, "Autentikasi database gagal"},
		{"database tidak ada", &pgconn.PgError{Code: "3D000"}, "Database tidak ditemukan di server"},
		{"connection exception", &pgconn.PgError{Code: "08006"}, "Tidak dapat terhubung ke database"},
		{"cannot connect now", &pgconn.PgError{Code: "57P03"}, "Tidak dapat terhubung ke database"},
		{"deadline", context.DeadlineExceeded, "Koneksi ke database melewati batas waktu"},
		{"net timeout", timeoutErr{}, "Koneksi ke database melewati batas waktu"},
		{"deadline terbungkus", fmt.Errorf("ping: %w", context.DeadlineExceeded), "Koneksi ke database melewati batas waktu"},
		{"op error", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, "Tidak dapat terhubung ke database (host tidak terjangkau)"},
		{"dns error", &net.DNSError{Err: "no such host", Name: "x"}, "Tidak dapat terhubung ke database (host tidak terjangkau)"},
		{"error sql biasa", &pgconn.PgError{Code: "42P01", Message: "relation does not exist"}, ""},
		{"error lain", errors.New("boom"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyError(tt.err)
			if tt.wantMsg == "" {
				if got != tt.err {
					t.Fatalf("seharusnya dikembalikan apa adanya, dapat %v", got)
				}
				if errors.Is(got, domain.ErrConnectionFailed) {
					t.Fatal("tidak boleh ErrConnectionFailed")
				}
				return
			}
			if !errors.Is(got, domain.ErrConnectionFailed) {
				t.Fatalf("harus errors.Is ErrConnectionFailed: %v", got)
			}
			if got.Error() != tt.wantMsg {
				t.Errorf("pesan = %q, mau %q", got.Error(), tt.wantMsg)
			}
		})
	}
}

func TestClassifyError_Nil(t *testing.T) {
	if ClassifyError(nil) != nil {
		t.Fatal("nil harus tetap nil")
	}
}

func TestClassifyError_NoSecretLeak(t *testing.T) {
	secret := "SuperSecret123"
	raw := &pgconn.PgError{Code: "28P01", Message: "password authentication failed for user with " + secret}
	got := ClassifyError(fmt.Errorf("postgres://u:%s@h/db: %w", secret, raw))
	if strings.Contains(got.Error(), secret) || strings.Contains(fmt.Sprint(got), secret) {
		t.Fatalf("pesan membocorkan password: %q", got.Error())
	}
}

func TestClassifyError_Idempotent(t *testing.T) {
	first := ClassifyError(&pgconn.PgError{Code: "28P01"})
	if second := ClassifyError(first); second != first {
		t.Fatal("klasifikasi ulang tidak boleh mengubah error")
	}
}
