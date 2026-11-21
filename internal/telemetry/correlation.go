// Package telemetry menyiapkan log JSON, tracing OpenTelemetry, korelasi, dan redaksi.
package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"regexp"
)

type correlationKey struct{}

var correlationPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationKey{}, id)
}

func CorrelationID(ctx context.Context) string {
	id, _ := ctx.Value(correlationKey{}).(string)
	return id
}

// CorrelationIDFrom memakai ID dari klien bila formatnya aman, selain itu membuat yang baru.
func CorrelationIDFrom(incoming string) string {
	if correlationPattern.MatchString(incoming) {
		return incoming
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b) // crypto/rand tidak pernah gagal di platform yang didukung
	return hex.EncodeToString(b)
}
