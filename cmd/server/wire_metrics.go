package main

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/queue"
	"dbplatform/internal/telemetry"
)

// statistik queue cuma dipasang di API biar nggak kehitung dobel per worker
func wireMetrics(mux *http.ServeMux, pool *pgxpool.Pool) {
	mux.Handle("GET /metrics", telemetry.MetricsHandler(queue.NewCollector(pool)))
}
