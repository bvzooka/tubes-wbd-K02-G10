package middleware_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dbplatform/internal/httpx"
	"dbplatform/internal/middleware"
	webassets "dbplatform/web"
)

var logger = slog.New(slog.NewTextHandler(io.Discard, nil))

func newRenderer(t *testing.T) (*httpx.Renderer, *httpx.Flasher) {
	t.Helper()
	fl := httpx.NewFlasher(make([]byte, 32), false)
	r, err := httpx.NewRenderer(webassets.FS, fl)
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	return r, fl
}

func TestChain_Order(t *testing.T) {
	var got []string
	mk := func(name string) middleware.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = append(got, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	h := middleware.Chain(mk("a"), mk("b"), mk("c"))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		got = append(got, "handler")
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if strings.Join(got, ",") != "a,b,c,handler" {
		t.Errorf("urutan = %v", got)
	}
}

func TestMiddleware_SecurityHeadersAndRequestID(t *testing.T) {
	h := middleware.Chain(middleware.WithRequestID, middleware.SecurityHeaders)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if middleware.RequestID(r.Context()) == "" {
			t.Error("request id kosong di context")
		}
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	for k, v := range map[string]string{
		"Content-Security-Policy": "default-src 'self'",
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "same-origin",
	} {
		if rec.Header().Get(k) != v {
			t.Errorf("%s = %q", k, rec.Header().Get(k))
		}
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("X-Request-ID tidak ada")
	}
}

func TestRecover_PanicBecomes500(t *testing.T) {
	r, _ := newRenderer(t)
	h := middleware.Recover(logger, httpx.HandleError(r, logger))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("meledak")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "meledak") {
		t.Errorf("status=%d body memuat detail panic", rec.Code)
	}
}

func TestLogging_OmitsQueryAndCookies(t *testing.T) {
	var sb strings.Builder
	lg := slog.New(slog.NewTextHandler(&sb, nil))
	h := middleware.Logging(lg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(418) }))
	req := httptest.NewRequest("GET", "/a?token=rahasia", nil)
	req.AddCookie(&http.Cookie{Name: "wbd_session", Value: "sesirahasia"})
	h.ServeHTTP(httptest.NewRecorder(), req)
	out := sb.String()
	if strings.Contains(out, "rahasia") || strings.Contains(out, "wbd_session") {
		t.Errorf("log membocorkan query atau cookie: %s", out)
	}
	if !strings.Contains(out, "status=418") || !strings.Contains(out, "path=/a") {
		t.Errorf("log tidak lengkap: %s", out)
	}
}
