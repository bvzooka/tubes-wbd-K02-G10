package middleware

import (
	"fmt"
	"log/slog"
	"net/http"

	"dbplatform/internal/contract"
)

// Recover menangkap panic di handler supaya satu request yang error tidak mematikan server
func Recover(logger *slog.Logger, handleErr contract.ErrorHandler) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if p := recover(); p != nil {
					// ErrAbortHandler sengaja di-panic ulang karena itu cara resmi net/http membatalkan response
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
