package auth

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestNewToken_UniqueAndLength(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		tok, err := NewToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(tok) != 43 { // 32 byte base64url tanpa padding
			t.Fatalf("len = %d, want 43", len(tok))
		}
		raw, err := base64.RawURLEncoding.DecodeString(tok)
		if err != nil || len(raw) != 32 {
			t.Fatalf("token bukan base64url 32 byte: %v", err)
		}
		if seen[tok] {
			t.Fatal("token duplikat")
		}
		seen[tok] = true
	}
}

func TestHashToken_Deterministic(t *testing.T) {
	a, b := HashToken("abc"), HashToken("abc")
	if !bytes.Equal(a, b) {
		t.Fatal("hash tidak deterministik")
	}
	if len(a) != 32 {
		t.Fatalf("len = %d, want 32", len(a))
	}
	if bytes.Equal(a, HashToken("abd")) {
		t.Fatal("input berbeda menghasilkan hash sama")
	}
}

func TestNewCSRFToken(t *testing.T) {
	a, _ := NewCSRFToken()
	b, _ := NewCSRFToken()
	if a == "" || a == b {
		t.Fatal("token CSRF harus acak dan tidak kosong")
	}
}
