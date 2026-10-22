package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestTokensEqual(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"abc", "abc", true},
		{"abc", "abd", false},
		{"abc", "abcd", false},
		{"", "", false},
		{"abc", "", false},
	}
	for _, tc := range tests {
		if got := tokensEqual(tc.a, tc.b); got != tc.want {
			t.Errorf("tokensEqual(%q,%q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestSameOrigin(t *testing.T) {
	tests := []struct {
		name            string
		origin, referer string
		want            bool
	}{
		{"tanpa header", "", "", true},
		{"origin sama", "http://app.local:8080", "", true},
		{"origin asing", "http://evil.example", "", false},
		{"origin null", "null", "", false},
		{"origin asing walau referer sama", "http://evil.example", "http://app.local:8080/x", false},
		{"referer sama", "", "http://app.local:8080/databases", true},
		{"referer asing", "", "https://evil.example/app.local:8080", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "http://app.local:8080/logout", nil)
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.referer != "" {
				r.Header.Set("Referer", tc.referer)
			}
			if got := sameOrigin(r); got != tc.want {
				t.Fatalf("sameOrigin = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSubmittedCSRFToken(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(url.Values{"csrf_token": {"form"}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if got := submittedCSRFToken(r); got != "form" {
		t.Fatalf("got %q", got)
	}
	r.Header.Set(CSRFHeaderName, "header")
	if got := submittedCSRFToken(r); got != "header" {
		t.Fatalf("header harus diutamakan, got %q", got)
	}
}

func TestCookieAttributes(t *testing.T) {
	exp := time.Date(2026, 10, 21, 20, 0, 0, 0, time.UTC)
	for _, secure := range []bool{false, true} {
		cookies := []*http.Cookie{
			sessionCookie("v", exp, secure), clearSessionCookie(secure),
			loginCSRFCookie("v", secure), clearLoginCSRFCookie(secure),
		}
		for _, c := range cookies {
			if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Secure != secure {
				t.Errorf("%s secure=%v: HttpOnly=%v SameSite=%v Secure=%v", c.Name, secure, c.HttpOnly, c.SameSite, c.Secure)
			}
		}
	}
	c := sessionCookie("v", exp, false)
	if c.Name != "wbd_session" || c.Path != "/" || !c.Expires.Equal(exp) {
		t.Fatalf("session cookie: %+v", c)
	}
	if clearSessionCookie(false).MaxAge != -1 {
		t.Fatal("clear cookie harus MaxAge=-1")
	}
}
