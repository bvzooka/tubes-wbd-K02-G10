package connmgr

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// panjang kunci AES-256 in byte
const keySize = 32

var (
	// ErrInvalidKey dikembalikan klo kunci bukan tepat 32 byte.
	ErrInvalidKey = errors.New("connmgr: kunci enkripsi harus 32 byte")
	// ErrDecrypt dikembalikan kalo data ga  bisa dibuka 
	ErrDecrypt = errors.New("connmgr: gagal mendekripsi data")
)

// ParseKey ubah teks base64 jadi kunci 32 byte dgn openssl rand -base64 32
func ParseKey(b64 string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("connmgr: kunci bukan base64 yang valid: %w", err)
	}
	if len(key) != keySize {
		return nil, ErrInvalidKey
	}
	return key, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != keySize {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("connmgr: buat cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

//  mengunci plaintext, hasilnya nonce (12 byte) lalu ciphertext
func Encrypt(key, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("connmgr: buat nonce: %w", err)
	}
	// menempel ciphertext abi nonce, jadi hasilnya nonce || ciphertext
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func Decrypt(key, data []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(data) < gcm.NonceSize()+gcm.Overhead() {
		return nil, ErrDecrypt
	}
	nonce, ciphertext := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}