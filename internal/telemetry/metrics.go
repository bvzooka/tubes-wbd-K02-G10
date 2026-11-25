package telemetry

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	HTTPRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "dbplatform_http_requests_total",
		Help: "Jumlah request HTTP per route dan status.",
	}, []string{"method", "route", "status"})

	HTTPDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "dbplatform_http_request_duration_seconds",
		Help:    "Durasi request HTTP per route.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})
)

// kalau satu collector error (misal database putus), metrik lain tetap keluar
func MetricsHandler(extra ...prometheus.Collector) http.Handler {
	reg := prometheus.NewRegistry()
	reg.MustRegister(extra...)
	gatherers := prometheus.Gatherers{prometheus.DefaultGatherer, reg}
	return promhttp.HandlerFor(gatherers, promhttp.HandlerOpts{ErrorHandling: promhttp.ContinueOnError})
}
