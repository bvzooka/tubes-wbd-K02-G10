package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCorrelationIDFrom(t *testing.T) {
	tests := []struct {
		name     string
		incoming string
		keep     bool
	}{
		{"valid", "req-12345678", true},
		{"terlalu pendek", "abc", false},
		{"karakter berbahaya", "abc\ndef12345", false},
		{"kosong", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CorrelationIDFrom(tt.incoming)
			if (got == tt.incoming) != tt.keep {
				t.Fatalf("CorrelationIDFrom(%q) = %q", tt.incoming, got)
			}
			if !correlationPattern.MatchString(got) {
				t.Fatalf("hasil %q tidak valid", got)
			}
		})
	}
}

func TestRedactSQL(t *testing.T) {
	tests := map[string]string{
		"ALTER ROLE app PASSWORD 'rahasia123'":          "ALTER ROLE app PASSWORD '[REDACTED]'",
		"CREATE USER x IDENTIFIED BY 'p''w'":            "CREATE USER x IDENTIFIED BY '[REDACTED]'",
		"UPDATE t SET a = 1 WHERE id > $1 AND id <= $2": "UPDATE t SET a = 1 WHERE id > $1 AND id <= $2",
	}
	for in, want := range tests {
		if got := RedactSQL(in); got != want {
			t.Errorf("RedactSQL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLogger_RedactsAndAddsCorrelation(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, "info")
	ctx := WithCorrelationID(context.Background(), "req-12345678")

	logger.InfoContext(ctx, "auth.login",
		"password", "Sup3rSecret!", "db_password", "Sup3rSecret!", "csrf_token", "Sup3rSecret!",
		"query", "ALTER ROLE a PASSWORD 'Sup3rSecret!'")

	out := buf.String()
	if strings.Contains(out, "Sup3rSecret!") {
		t.Fatalf("password bocor ke log: %s", out)
	}
	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log bukan JSON: %v", err)
	}
	if entry["correlation_id"] != "req-12345678" {
		t.Errorf("correlation_id = %v", entry["correlation_id"])
	}
}

func TestLogger_LevelFilter(t *testing.T) {
	var buf bytes.Buffer
	NewLogger(&buf, "warn").Info("tidak tampil")
	if buf.Len() != 0 {
		t.Fatalf("log info harus tersaring pada level warn: %s", buf.String())
	}
}

func TestTracesURL(t *testing.T) {
	for in, want := range map[string]string{
		"http://localhost:4318":           "http://localhost:4318/v1/traces",
		"http://localhost:4318/":          "http://localhost:4318/v1/traces",
		"http://collector:4318/v1/traces": "http://collector:4318/v1/traces",
	} {
		if got := tracesURL(in); got != want {
			t.Errorf("tracesURL(%q) = %q, want %q", in, got, want)
		}
	}
}
