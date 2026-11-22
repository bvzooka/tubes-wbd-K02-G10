package middleware

import (
	"net/http"

	"dbplatform/internal/telemetry"
)

// WithRequestID memakai X-Request-ID dari klien kalau formatnya aman, kalau tidak membuat yang baru, lalu menyimpannya di context sebagai correlation ID
func WithRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := telemetry.CorrelationIDFrom(r.Header.Get("X-Request-ID"))
		// ID juga dikirim ke browser lewat header supaya user bisa melaporkannya saat ada error
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(telemetry.WithCorrelationID(r.Context(), id)))
	})
}
