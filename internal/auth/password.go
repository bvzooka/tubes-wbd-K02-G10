package auth

import (
	"errors"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

const (
	// DefaultBcryptCost adalah cost bcrypt untuk password baru.
	DefaultBcryptCost = 12
	// MinPasswordBytes dan MaxPasswordBytes adalah batas panjang password (bcrypt membatasi 72 byte).
	MinPasswordBytes = 10
	MaxPasswordBytes = 72
)

// ErrPasswordTooLong dikembalikan Hash jika password lebih dari 72 byte.
var ErrPasswordTooLong = errors.New("auth: password exceeds 72 bytes")

// Hasher memenuhi contract.PasswordHasher memakai bcrypt.
type Hasher struct {
	cost int

	dummyOnce sync.Once
	dummyHash []byte
}

// NewHasher membuat Hasher dengan cost tertentu. Cost di luar rentang bcrypt memakai DefaultBcryptCost.
func NewHasher(cost int) *Hasher {
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		cost = DefaultBcryptCost
	}
	return &Hasher{cost: cost}
}

func (h *Hasher) Hash(plain string) (string, error) {
	if len(plain) > MaxPasswordBytes {
		return "", ErrPasswordTooLong
	}
	b, err := bcrypt.GenerateFromPassword([]byte(plain), h.cost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (h *Hasher) Verify(hash, plain string) bool {
	if len(plain) > MaxPasswordBytes {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// VerifyDummy menjalankan perbandingan bcrypt terhadap hash palsu agar waktu respons
// untuk user yang tidak ada atau tidak aktif mirip dengan user yang ada. Selalu false.
func (h *Hasher) VerifyDummy(plain string) bool {
	h.dummyOnce.Do(func() {
		h.dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-password-for-timing"), h.cost)
	})
	if len(plain) > MaxPasswordBytes {
		plain = plain[:MaxPasswordBytes]
	}
	_ = bcrypt.CompareHashAndPassword(h.dummyHash, []byte(plain))
	return false
}
