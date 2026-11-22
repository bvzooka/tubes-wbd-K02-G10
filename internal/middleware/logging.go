package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"dbplatform/internal/telemetry"
)

// statusRecorder membungkus ResponseWriter untuk mengingat status code yang dikirim, karena net/http tidak menyediakannya
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	// menulis body tanpa WriteHeader berarti status 200
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Logging mencatat satu baris log per request: siapa pelakunya, method, path, status, dan lama prosesnya
func Logging(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			// slot pelaku diisi oleh middleware auth saat user berhasil dikenali, kosong berarti anonim
			ctx, actor := telemetry.WithActorSlot(r.Context())
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r.WithContext(ctx))
			if rec.status == 0 {
				rec.status = http.StatusOK
			}
			if *actor == "" {
				*actor = "anonymous"
			}
			// query string dan cookie sengaja tidak dicatat karena bisa berisi token atau session
			logger.InfoContext(ctx, "http request",
				"event", "http.request",
				"actor", *actor,
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}
