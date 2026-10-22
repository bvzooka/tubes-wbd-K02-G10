package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

const tokenBytes = 32

// NewToken membuat identifier session acak 32 byte dalam base64url tanpa padding.
func NewToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken mengembalikan SHA-256 dari nilai token mentah. Hanya hash ini yang disimpan di database.
func HashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

// NewCSRFToken membuat token CSRF acak dengan format yang sama dengan token session.
func NewCSRFToken() (string, error) {
	return NewToken()
}
