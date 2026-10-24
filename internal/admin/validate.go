package admin

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	minPasswordBytes = 10
	maxPasswordBytes = 72

	maxGroupNameChars   = 64
	maxDescriptionChars = 500

	msgUsernameInvalid    = "Username harus 3 sampai 32 karakter: huruf kecil, angka, titik, garis bawah, atau tanda minus."
	msgUsernameTaken      = "Username sudah dipakai."
	msgPasswordLength     = "Password harus 10 sampai 72 karakter."
	msgGroupNameInvalid   = "Nama group harus 1 sampai 64 karakter."
	msgGroupNameTaken     = "Nama group sudah dipakai."
	msgDescriptionTooLong = "Deskripsi maksimal 500 karakter."
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

func validateGroupName(raw string) (string, string) {
	name := strings.TrimSpace(raw)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxGroupNameChars {
		return name, msgGroupNameInvalid
	}
	return name, ""
}

func validateDescription(raw string) (string, string) {
	desc := strings.TrimSpace(raw)
	if utf8.RuneCountInString(desc) > maxDescriptionChars {
		return desc, msgDescriptionTooLong
	}
	return desc, ""
}
