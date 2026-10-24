package admin

import (
	"regexp"
	"strings"
)

const (
	minPasswordBytes = 10
	maxPasswordBytes = 72

	msgUsernameInvalid = "Username harus 3 sampai 32 karakter: huruf kecil, angka, titik, garis bawah, atau tanda minus."
	msgUsernameTaken   = "Username sudah dipakai."
	msgPasswordLength  = "Password harus 10 sampai 72 karakter."
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9_.-]{3,32}$`)

func NormalizeUsername(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func validateUsername(raw string) (string, string) {
	u := NormalizeUsername(raw)
	if !usernamePattern.MatchString(u) {
		return u, msgUsernameInvalid
	}
	return u, ""
}

func validatePassword(p string) string {
	if len(p) < minPasswordBytes || len(p) > maxPasswordBytes {
		return msgPasswordLength
	}
	return ""
}
