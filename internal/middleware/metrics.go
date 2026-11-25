package middleware

import (
	"net/http"
	"strconv"
	"time"

	"dbplatform/internal/telemetry"
)

// harus dipasang paling dalam (nempel ke mux), karena r.Pattern baru diisi sama mux
func Metrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}

		// pakai pola route, bukan path asli, biar /users/1 dan /users/2 masuk satu label
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		telemetry.HTTPRequests.WithLabelValues(r.Method, route, strconv.Itoa(rec.status)).Inc()
		telemetry.HTTPDuration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
	})
}
