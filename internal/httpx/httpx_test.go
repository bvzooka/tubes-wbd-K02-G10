package httpx_test

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"dbplatform/internal/domain"
	"dbplatform/internal/httpx"
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

func TestRenderer_ParsesAllEmbeddedTemplates(t *testing.T) {
	r, _ := newRenderer(t)
	for _, name := range []string{"auth/login.html", "explain/result.html", "errors/403.html", "errors/404.html", "errors/500.html", "errors/502.html"} {
		if !r.Has(name) {
			t.Errorf("template %s tidak terdaftar", name)
		}
	}
	if r.Has("layouts/base.html") || r.Has("partials/csrf.html") {
		t.Error("layout dan partial tidak boleh terdaftar sebagai halaman")
	}
}

func TestRenderer_Page_WrapsDataAndEscapes(t *testing.T) {
	r, _ := newRenderer(t)
	req := httptest.NewRequest("GET", "/login", nil)
	ctx := domain.WithPrincipal(req.Context(), domain.Principal{UserID: 1, Username: "<b>budi</b>", IsPlatformAdmin: true})
	req = req.WithContext(domain.WithCSRFToken(ctx, "tok-123"))

	rec := httptest.NewRecorder()
	r.Page(rec, req, http.StatusUnprocessableEntity, "errors/500.html",
		httpx.ErrorPage{Heading: "Judul <script>", Message: "pesan", RequestID: "abc"})

	body := rec.Body.String()
	if rec.Code != 422 {
		t.Errorf("status = %d", rec.Code)
	}
	for _, want := range []string{`name="csrf_token" value="tok-123"`, "Judul &lt;script&gt;", "&lt;b&gt;budi&lt;/b&gt;", "/admin/users", `action="/logout"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body tidak memuat %q", want)
		}
	}
	if strings.Contains(body, "<script>") || strings.Contains(body, "<b>budi") {
		t.Error("data harus di-escape")
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("halaman harus no-store")
	}
}

func TestRenderer_Page_NoPrincipalHidesNav(t *testing.T) {
	r, _ := newRenderer(t)
	rec := httptest.NewRecorder()
	r.Page(rec, httptest.NewRequest("GET", "/login", nil), 200, "errors/404.html", httpx.ErrorPage{Heading: "x"})
	if strings.Contains(rec.Body.String(), "/logout") {
		t.Error("tanpa login tidak boleh ada tombol keluar")
	}
}

func TestRenderer_Page_UnknownTemplateIs500(t *testing.T) {
	r, _ := newRenderer(t)
	rec := httptest.NewRecorder()
	r.Page(rec, httptest.NewRequest("GET", "/", nil), 200, "tidak/ada.html", nil)
	if rec.Code != 500 {
		t.Errorf("status = %d, mau 500", rec.Code)
	}
}

func TestHandleError_Mapping(t *testing.T) {
	r, _ := newRenderer(t)
	h := httpx.HandleError(r, logger)
	tests := []struct {
		err      error
		status   int
		location string
	}{
		{domain.ErrUnauthenticated, 303, "/login"},
		{fmt.Errorf("bungkus: %w", domain.ErrForbidden), 403, ""},
		{domain.ErrNotFound, 404, ""},
		{domain.ErrConnectionFailed, 502, ""},
		{domain.ErrConnectionInactive, 502, ""},
		{errors.New("rahasia: password=hunter2"), 500, ""},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.status, "_", tt.err), func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/x", nil)
			h(rec, req, tt.err)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, mau %d", rec.Code, tt.status)
			}
			if tt.location != "" && rec.Header().Get("Location") != tt.location {
				t.Errorf("location = %q", rec.Header().Get("Location"))
			}
			if strings.Contains(rec.Body.String(), "hunter2") {
				t.Error("detail error tidak boleh bocor ke response")
			}
		})
	}
}

func TestFlasher_RoundTripAndTamper(t *testing.T) {
	fl := httpx.NewFlasher([]byte("0123456789abcdef0123456789abcdef"), false)

	rec := httptest.NewRecorder()
	fl.Set(rec, httpx.FlashSuccess, "Tersimpan")
	set := rec.Result().Cookies()[0]
	if !set.HttpOnly || set.Name != httpx.FlashCookieName {
		t.Fatalf("cookie flash: %+v", set)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: set.Name, Value: set.Value})
	rec = httptest.NewRecorder()
	got := fl.Take(rec, req)
	if got == nil || got.Message != "Tersimpan" || got.Kind != httpx.FlashSuccess {
		t.Fatalf("flash = %+v", got)
	}
	if c := rec.Result().Cookies(); len(c) != 1 || c[0].MaxAge >= 0 {
		t.Error("cookie flash harus dihapus setelah dibaca")
	}

	// Payload diubah: tanda tangan tidak cocok.
	payload, sig, _ := strings.Cut(set.Value, ".")
	forged := payload + "x." + sig
	req = httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: set.Name, Value: forged})
	if fl.Take(httptest.NewRecorder(), req) != nil {
		t.Error("flash palsu harus ditolak")
	}

	// Kunci lain: ditolak.
	other := httpx.NewFlasher([]byte("ffffffffffffffffffffffffffffffff"), false)
	req = httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: set.Name, Value: set.Value})
	if other.Take(httptest.NewRecorder(), req) != nil {
		t.Error("flash dengan kunci berbeda harus ditolak")
	}
}

func TestChain_Order(t *testing.T) {
	var got []string
	mk := func(name string) httpx.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = append(got, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	h := httpx.Chain(mk("a"), mk("b"), mk("c"))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		got = append(got, "handler")
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if strings.Join(got, ",") != "a,b,c,handler" {
		t.Errorf("urutan = %v", got)
	}
}

func TestMiddleware_SecurityHeadersAndRequestID(t *testing.T) {
	h := httpx.Chain(httpx.WithRequestID, httpx.SecurityHeaders)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if httpx.RequestID(r.Context()) == "" {
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
	h := httpx.Recover(logger, httpx.HandleError(r, logger))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
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
	h := httpx.Logging(lg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(418) }))
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

func TestPager_TotalPages(t *testing.T) {
	mk := func(url string, hasNext bool) httpx.Pager {
		req := httptest.NewRequest("GET", url, nil)
		return httpx.NewPager(req, httpx.ParsePage(req), hasNext)
	}
	if p := mk("/x?page=2", true); p.TotalPages != 0 {
		t.Errorf("masih ada halaman berikutnya, total belum diketahui: %+v", p)
	}
	if p := mk("/x?page=3", false); p.TotalPages != 3 {
		t.Errorf("halaman terakhir: total = %d, want 3", p.TotalPages)
	}
	if p := mk("/x", false); p.TotalPages != 1 {
		t.Errorf("satu halaman: total = %d, want 1", p.TotalPages)
	}
}

func TestPager(t *testing.T) {
	req := httptest.NewRequest("GET", "/databases/1/tables/public/t/data?page=2&size=10&x=a", nil)
	p := httpx.NewPager(req, httpx.ParsePage(req), true)
	if p.Page != 2 || !p.HasPrev || !p.HasNext {
		t.Fatalf("pager = %+v", p)
	}
	u, _ := url.Parse(p.NextURL)
	if u.Query().Get("page") != "3" || u.Query().Get("size") != "10" || u.Query().Get("x") != "a" {
		t.Errorf("next url = %s", p.NextURL)
	}
	if first := httpx.NewPager(httptest.NewRequest("GET", "/x", nil), httpx.ParsePage(httptest.NewRequest("GET", "/x", nil)), false); first.HasPrev || first.HasNext {
		t.Errorf("halaman pertama tanpa lanjutan: %+v", first)
	}
	if big := httpx.ParsePage(httptest.NewRequest("GET", "/x?size=9999&page=-3", nil)); big.PageSize != 200 || big.Page != 1 {
		t.Errorf("normalisasi: %+v", big)
	}
}
