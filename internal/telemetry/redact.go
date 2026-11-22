package telemetry

import (
	"log/slog"
	"regexp"
	"strings"
)

const redacted = "[REDACTED]"

var sensitiveKeys = []string{"password", "token", "secret", "cookie", "authorization", "csrf", "session"}

var sqlSecretPattern = regexp.MustCompile(`(?i)(PASSWORD|IDENTIFIED\s+BY)\s+'(?:[^']|'')*'`)

func isSensitiveKey(key string) bool {
	key = strings.ToLower(key)
	for _, s := range sensitiveKeys {
		if strings.Contains(key, s) {
			return true
		}
	}
	return false
}

// RedactSQL menyamarkan literal password di teks SQL.
func RedactSQL(sql string) string {
	return sqlSecretPattern.ReplaceAllString(sql, "$1 '"+redacted+"'")
}

// redactAttr dipakai sebagai slog.HandlerOptions.ReplaceAttr.
func redactAttr(_ []string, a slog.Attr) slog.Attr {
	if isSensitiveKey(a.Key) {
		return slog.String(a.Key, redacted)
	}
	if a.Key == "query" && a.Value.Kind() == slog.KindString {
		return slog.String(a.Key, RedactSQL(a.Value.String()))
	}
	return a
}
