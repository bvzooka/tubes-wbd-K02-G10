package auth

import (
	"crypto/subtle"
	"net/http"
	"net/url"
)

const (
	CSRFFieldName  = "csrf_token"
	CSRFHeaderName = "X-CSRF-Token"
)

// tokensEqual membandingkan dua token dengan waktu konstan. Token kosong tidak pernah cocok.
func tokensEqual(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// sameOrigin memeriksa header Origin, atau Referer jika Origin tidak ada.
// Request tanpa keduanya diterima (token tetap wajib).
func sameOrigin(r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		return hostMatches(origin, r.Host)
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		return hostMatches(ref, r.Host)
	}
	return true
}

func hostMatches(raw, host string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false // termasuk Origin: null
	}
	return u.Host == host
}

// submittedCSRFToken membaca token dari header atau field form.
func submittedCSRFToken(r *http.Request) string {
	if t := r.Header.Get(CSRFHeaderName); t != "" {
		return t
	}
	return r.PostFormValue(CSRFFieldName)
}

func safeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}
