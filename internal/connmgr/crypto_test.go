package connmgr

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"
)

func testKey(fill byte) []byte {
	return bytes.Repeat([]byte{fill}, keySize)
}

func TestCrypto_RoundTrip(t *testing.T) {
	key := testKey(1)
	for _, plain := range []string{"rahasia123", "p@ss:w/rd#?%", "", "密码"} {
		enc, err := Encrypt(key, []byte(plain))
		if err != nil {
			t.Fatalf("encrypt %q: %v", plain, err)
		}
		dec, err := Decrypt(key, enc)
		if err != nil {
			t.Fatalf("decrypt %q: %v", plain, err)
		}
		if string(dec) != plain {
			t.Errorf("got %q, want %q", dec, plain)
		}
	}
}

func TestCrypto_CiphertextDoesNotContainPlaintext(t *testing.T) {
	enc, _ := Encrypt(testKey(1), []byte("rahasia123"))
	if bytes.Contains(enc, []byte("rahasia123")) {
		t.Error("ciphertext masih memuat plaintext")
	}
}

func TestCrypto_WrongKeyFails(t *testing.T) {
	enc, _ := Encrypt(testKey(1), []byte("rahasia"))
	if _, err := Decrypt(testKey(2), enc); !errors.Is(err, ErrDecrypt) {
		t.Errorf("want ErrDecrypt, got %v", err)
	}
}

func TestCrypto_SamePlaintextDifferentOutput(t *testing.T) {
	key := testKey(1)
	a, _ := Encrypt(key, []byte("sama"))
	b, _ := Encrypt(key, []byte("sama"))
	if bytes.Equal(a, b) {
		t.Error("dua enkripsi plaintext yang sama menghasilkan output identik")
	}
}

func TestCrypto_TamperedDataFails(t *testing.T) {
	key := testKey(1)
	enc, _ := Encrypt(key, []byte("rahasia"))
	enc[len(enc)-1] ^= 0xFF 
	if _, err := Decrypt(key, enc); !errors.Is(err, ErrDecrypt) {
		t.Errorf("want ErrDecrypt, got %v", err)
	}
}

func TestCrypto_ShortDataDoesNotPanic(t *testing.T) {
	for _, data := range [][]byte{nil, {}, {1, 2, 3}, make([]byte, 20)} {
		if _, err := Decrypt(testKey(1), data); err == nil {
			t.Errorf("data %v seharusnya error", data)
		}
	}
}

func TestCrypto_BadKeyLength(t *testing.T) {
	if _, err := Encrypt([]byte("pendek"), []byte("x")); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("encrypt: want ErrInvalidKey, got %v", err)
	}
	if _, err := Decrypt([]byte("pendek"), []byte("x")); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("decrypt: want ErrInvalidKey, got %v", err)
	}
}

func TestParseKey(t *testing.T) {
	good := base64.StdEncoding.EncodeToString(testKey(7))
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))

	tests := []struct {
		name string
		input string
		wantErr bool
		isLen bool 
	}{
		{"valid", good, false, false},
		{"valid dengan spasi/newline", good + "\n", false, false},
		{"terlalu pendek", short, true, true},
		{"bukan base64", "!!!bukan-base64!!!", true, false},
		{"kosong", "", true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key, err := ParseKey(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if tc.isLen && !errors.Is(err, ErrInvalidKey) {
				t.Errorf("want ErrInvalidKey, got %v", err)
			}
			if !tc.wantErr && len(key) != keySize {
				t.Errorf("panjang kunci %d", len(key))
			}
		})
	}
}