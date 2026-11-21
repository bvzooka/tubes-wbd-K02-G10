package middleware

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dbplatform/internal/telemetry"
)

func TestChain_RunsInWrittenOrder(t *testing.T) {
	var order []string
	mk := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	Chain(mk("a"), mk("b"), mk("c"))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if strings.Join(order, ",") != "a,b,c" {
		t.Fatalf("urutan = %v", order)
	}
}

func TestRequestID_HonorsValidHeader(t *testing.T) {
	var seen string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = telemetry.CorrelationID(r.Context())
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-ID", "client-req-001")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if seen != "client-req-001" || rec.Header().Get("X-Request-ID") != "client-req-001" {
		t.Fatalf("header valid harus dipakai, dapat %q", seen)
	}

	req.Header.Set("X-Request-ID", "bad id!")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen == "bad id!" || seen == "" {
		t.Fatalf("header invalid harus diganti, dapat %q", seen)
	}
}

func TestLogging_RecordsActorAndStatus(t *testing.T) {
	var buf bytes.Buffer
	h := Logging(telemetry.NewLogger(&buf, "info"))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		telemetry.SetActor(r.Context(), "alice")
		w.WriteHeader(http.StatusTeapot)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["actor"] != "alice" || entry["status"] != float64(http.StatusTeapot) || entry["event"] != "http.request" {
		t.Fatalf("log = %v", entry)
	}
}

func TestRecover_UsesErrorHandler(t *testing.T) {
	var got error
	h := Recover(telemetry.NewLogger(&bytes.Buffer{}, "info"), func(w http.ResponseWriter, r *http.Request, err error) {
		got = err
		w.WriteHeader(http.StatusInternalServerError)
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusInternalServerError || got == nil || !strings.Contains(got.Error(), "boom") {
		t.Fatalf("code=%d err=%v", rec.Code, got)
	}
}

func TestSecurityHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	SecurityHeaders(http.NotFoundHandler()).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Header().Get("Content-Security-Policy") != "default-src 'self'" || rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("header = %v", rec.Header())
	}
}
