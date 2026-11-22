package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// kunci context untuk menyimpan ID request, tipe sendiri supaya tidak bentrok dengan paket lain
type requestIDKey struct{}

// RequestID mengambil ID request dari context, kosong kalau WithRequestID belum jalan
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// Nomor unik per req buat ngelacak di log
func WithRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		id := hex.EncodeToString(b)
		// ID juga dikirim ke browser lewat header supaya user bisa melaporkannya saat ada error
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}
