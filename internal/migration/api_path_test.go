package migration

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"dbplatform/internal/domain"
)

// nomor versi di luar jangkauan kolom INTEGER dijawab tidak ditemukan, bukan terpotong jadi angka lain
func TestVersionPath_RejectsOutOfRange(t *testing.T) {
	tests := []struct {
		version string
		want    int
		err     error
	}{
		{"7", 7, nil},
		{"2147483647", 2147483647, nil},
		{"2147483648", 0, domain.ErrNotFound},
		// kalau dipotong ke 32 bit, angka ini berubah jadi 1
		{"4294967297", 0, domain.ErrNotFound},
		{"0", 0, domain.ErrNotFound},
		{"abc", 0, domain.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/x", nil)
			r.SetPathValue("dbID", "1")
			r.SetPathValue("version", tt.version)
			_, got, err := versionPath(r)
			if got != tt.want || !errors.Is(err, tt.err) {
				t.Errorf("versionPath = %d, %v; mau %d, %v", got, err, tt.want, tt.err)
			}
		})
	}
}
