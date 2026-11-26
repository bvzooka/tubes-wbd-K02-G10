package main

import (
	"net/http"

	"dbplatform/internal/dbdriver/obs"
	"dbplatform/internal/telemetry"
)

// melayani /metrics worker: metrik job dari paket queue dan statistik pool koneksi.
func metricsHandler(pools *obs.PoolCollector) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", telemetry.MetricsHandler(pools))
	return mux
}
