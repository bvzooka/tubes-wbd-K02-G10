// Package middleware berisi middleware HTTP lintas modul.
package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"dbplatform/internal/contract"
	"dbplatform/internal/telemetry"
)

type Middleware = func(http.Handler) http.Handler

// Chain menyusun middleware sesuai urutan tulis: Chain(a, b)(h) menjalankan a, lalu b, lalu h.
func Chain(mws ...Middleware) Middleware {
	return func(next http.Handler) http.Handler {
		for i := len(mws) - 1; i >= 0; i-- {
			next = mws[i](next)
		}
		return next
	}
}

// RequestID memakai X-Request-ID dari klien bila valid. ID yang sama menjadi correlation_id.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := telemetry.CorrelationIDFrom(r.Header.Get("X-Request-ID"))
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(telemetry.WithCorrelationID(r.Context(), id)))
	})
}

func Recover(logger *slog.Logger, handleErr contract.ErrorHandler) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if p := recover(); p != nil {
					if p == http.ErrAbortHandler {
						panic(p)
					}
					logger.ErrorContext(r.Context(), "panic", "event", "http.panic", "panic", fmt.Sprint(p))
					handleErr(w, r, fmt.Errorf("panic: %v", p))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func Logging(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ctx, actor := telemetry.WithActorSlot(r.Context())
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r.WithContext(ctx))

			if *actor == "" {
				*actor = "anonymous"
			}
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

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status, s.wroteHeader = code, true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }
