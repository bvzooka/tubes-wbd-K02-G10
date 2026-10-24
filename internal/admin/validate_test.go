package admin

import (
	"strings"
	"testing"
)

func TestValidateGroupName(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantName string
		wantErr  bool
	}{
		{"valid", "Tim Data", "Tim Data", false},
		{"dirapikan", "  Tim Data  ", "Tim Data", false},
		{"satu karakter", "a", "a", false},
		{"batas atas", strings.Repeat("a", 64), strings.Repeat("a", 64), false},
		{"unicode dihitung per karakter", strings.Repeat("é", 64), strings.Repeat("é", 64), false},
		{"kosong", "", "", true},
		{"hanya spasi", "   ", "", true},
		{"terlalu panjang", strings.Repeat("a", 65), strings.Repeat("a", 65), true},
		{"unicode terlalu panjang", strings.Repeat("é", 65), strings.Repeat("é", 65), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, msg := validateGroupName(tt.in)
			if name != tt.wantName {
				t.Errorf("nama = %q, mau %q", name, tt.wantName)
			}
			if (msg != "") != tt.wantErr {
				t.Errorf("pesan = %q, wantErr = %v", msg, tt.wantErr)
			}
		})
	}
}

func TestValidateDescription(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"kosong boleh", "", "", false},
		{"dirapikan", "  catatan  ", "catatan", false},
		{"batas atas", strings.Repeat("a", 500), strings.Repeat("a", 500), false},
		{"terlalu panjang", strings.Repeat("a", 501), strings.Repeat("a", 501), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, msg := validateDescription(tt.in)
			if got != tt.want || (msg != "") != tt.wantErr {
				t.Errorf("got = %q, pesan = %q, wantErr = %v", got, msg, tt.wantErr)
			}
		})
	}
}

func TestValidateUsername(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantUser string
		wantErr  bool
	}{
		{"valid", "budi", "budi", false},
		{"dinormalkan", "  Budi.Santoso  ", "budi.santoso", false},
		{"semua karakter boleh", "a_b-c.9", "a_b-c.9", false},
		{"batas bawah", "abc", "abc", false},
		{"batas atas", strings.Repeat("a", 32), strings.Repeat("a", 32), false},
		{"terlalu pendek", "ab", "ab", true},
		{"terlalu panjang", strings.Repeat("a", 33), strings.Repeat("a", 33), true},
		{"kosong", "   ", "", true},
		{"berspasi", "bu di", "bu di", true},
		{"karakter terlarang", "budi!", "budi!", true},
		{"unicode", "budí", "budí", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user, msg := validateUsername(tt.in)
			if user != tt.wantUser {
				t.Errorf("username = %q, mau %q", user, tt.wantUser)
			}
			if (msg != "") != tt.wantErr {
				t.Errorf("pesan = %q, wantErr = %v", msg, tt.wantErr)
			}
		})
	}
}

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"9 byte", strings.Repeat("a", 9), true},
		{"10 byte", strings.Repeat("a", 10), false},
		{"72 byte", strings.Repeat("a", 72), false},
		{"73 byte", strings.Repeat("a", 73), true},
		{"kosong", "", true},
		{"multibyte 10 byte", strings.Repeat("é", 5), false},
		{"multibyte 74 byte", strings.Repeat("é", 37), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if msg := validatePassword(tt.in); (msg != "") != tt.wantErr {
				t.Errorf("pesan = %q, wantErr = %v", msg, tt.wantErr)
			}
		})
	}
}
