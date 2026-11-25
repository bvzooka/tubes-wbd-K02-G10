package main

import (
	"net/http"

	"dbplatform/internal/telemetry"
)

func metricsHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", telemetry.MetricsHandler())
	return mux
}
