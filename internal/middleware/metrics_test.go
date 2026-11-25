package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"dbplatform/internal/telemetry"
)

func TestMetrics_LabelsByRoutePatternNotRawPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	h := Metrics(mux)

	counter := telemetry.HTTPRequests.WithLabelValues("GET", "GET /users/{id}", "404")
	before := testutil.ToFloat64(counter)
	for _, path := range []string{"/users/1", "/users/2"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
	}

	if got := testutil.ToFloat64(counter) - before; got != 2 {
		t.Fatalf("dua path berbeda dengan pola sama harus masuk satu label, tambah = %v", got)
	}
}

func TestMetrics_UnmatchedRoute(t *testing.T) {
	h := Metrics(http.NewServeMux())
	counter := telemetry.HTTPRequests.WithLabelValues("GET", "unmatched", "404")
	before := testutil.ToFloat64(counter)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/nope", nil))
	if testutil.ToFloat64(counter)-before != 1 {
		t.Fatal("path tanpa pola harus dicatat sebagai unmatched")
	}
}
