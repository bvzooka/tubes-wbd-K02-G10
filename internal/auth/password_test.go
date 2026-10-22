package auth

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHasher_HashAndVerify(t *testing.T) {
	h := NewHasher(bcrypt.MinCost)
	hash, err := h.Hash("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$2") {
		t.Fatalf("hash bukan bcrypt: %q", hash)
	}
	if hash == "correct horse battery" {
		t.Fatal("hash sama dengan plaintext")
	}
	if !h.Verify(hash, "correct horse battery") {
		t.Fatal("password benar ditolak")
	}
	if h.Verify(hash, "wrong password!") {
		t.Fatal("password salah diterima")
	}
}

func TestHasher_RejectsOver72Bytes(t *testing.T) {
	h := NewHasher(bcrypt.MinCost)
	long := strings.Repeat("a", 73)
	if _, err := h.Hash(long); !errors.Is(err, ErrPasswordTooLong) {
		t.Fatalf("err = %v, want ErrPasswordTooLong", err)
	}
	hash, err := h.Hash(strings.Repeat("a", 72))
	if err != nil {
		t.Fatalf("72 byte harus diterima: %v", err)
	}
	// Tanpa pengecekan panjang, bcrypt lama akan memotong ke 72 byte dan menerima ini.
	if h.Verify(hash, long) {
		t.Fatal("password 73 byte tidak boleh cocok dengan hash 72 byte")
	}
}

func TestHasher_VerifyDummyAlwaysFalse(t *testing.T) {
	h := NewHasher(bcrypt.MinCost)
	if h.VerifyDummy("dummy-password-for-timing") {
		t.Fatal("VerifyDummy harus selalu false")
	}
}

func TestNewHasher_InvalidCostUsesDefault(t *testing.T) {
	if got := NewHasher(0).cost; got != DefaultBcryptCost {
		t.Fatalf("cost = %d, want %d", got, DefaultBcryptCost)
	}
}
