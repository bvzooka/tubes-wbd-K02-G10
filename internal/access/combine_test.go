package access

import (
	"testing"

	"dbplatform/internal/domain"
)

func TestMaxLevel(t *testing.T) {
	tests := []struct {
		name string
		in   []domain.AccessLevel
		want domain.AccessLevel
	}{
		{"kosong", nil, domain.AccessNone},
		{"satu view only", []domain.AccessLevel{domain.AccessViewOnly}, domain.AccessViewOnly},
		{"edit menang", []domain.AccessLevel{domain.AccessViewOnly, domain.AccessViewEdit, domain.AccessViewOnly}, domain.AccessViewEdit},
		{"urutan tidak berpengaruh", []domain.AccessLevel{domain.AccessViewEdit, domain.AccessViewOnly}, domain.AccessViewEdit},
		{"nilai tidak dikenal diabaikan", []domain.AccessLevel{"owner", domain.AccessViewOnly}, domain.AccessViewOnly},
		{"none saja", []domain.AccessLevel{domain.AccessNone}, domain.AccessNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := MaxLevel(tc.in...); got != tc.want {
				t.Fatalf("MaxLevel = %q, want %q", got, tc.want)
			}
		})
	}
}
