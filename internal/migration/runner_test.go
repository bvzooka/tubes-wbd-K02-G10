package migration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"dbplatform/internal/domain"
)

func TestIsTransient_Errors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"koneksi putus", &pgconn.PgError{Code: "08006"}, true},
		{"deadlock", &pgconn.PgError{Code: "40P01"}, true},
		{"koneksi penuh", &pgconn.PgError{Code: "53300"}, true},
		{"dibungkus", fmt.Errorf("tx: %w", &pgconn.PgError{Code: "40001"}), true},
		{"eof", io.ErrUnexpectedEOF, true},
		{"gagal terhubung", domain.ErrConnectionFailed, true},
		{"salah sintaks", &pgconn.PgError{Code: "42601"}, false},
		{"constraint", &pgconn.PgError{Code: "23505"}, false},
		{"statement timeout", &pgconn.PgError{Code: "57014"}, false},
		{"batas waktu", context.DeadlineExceeded, false},
		{"error lain", errors.New("aneh"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTransient(tt.err); got != tt.want {
				t.Errorf("isTransient = %v, mau %v", got, tt.want)
			}
		})
	}
}
