package apiv1

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"dbplatform/internal/domain"
	"dbplatform/internal/telemetry"
)

func TestToAPIError(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"unauthenticated", domain.ErrUnauthenticated, 401, "unauthenticated"},
		{"forbidden dibungkus", fmt.Errorf("cek akses: %w", domain.ErrForbidden), 403, "forbidden"},
		{"not found", domain.ErrNotFound, 404, "not_found"},
		{"validation", &domain.ValidationError{Fields: map[string]string{"name": "wajib"}}, 422, "validation"},
		{"no leader", &domain.ValidationError{Message: "Group A belum punya leader", Cause: domain.ErrNoLeader}, 422, "validation"},
		{"conflict", domain.ErrConflict, 409, "conflict"},
		{"out of order", domain.ErrMigrationOutOfOrder, 409, "out_of_order"},
		{"unsupported", domain.ErrUnsupported, 422, "unsupported"},
		{"koneksi gagal", domain.ErrConnectionFailed, 502, "connection_failed"},
		{"koneksi nonaktif", domain.ErrConnectionInactive, 502, "connection_inactive"},
		{"lainnya", errors.New("pgx: boom"), 500, "internal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, status := toAPIError(tt.err)
			if status != tt.status || got.Code != tt.code {
				t.Fatalf("got %d %q, want %d %q", status, got.Code, tt.status, tt.code)
			}
		})
	}
}

func TestToAPIError_ValidationCarriesFieldsAndMessage(t *testing.T) {
	got, _ := toAPIError(&domain.ValidationError{Message: "Group A belum punya leader", Fields: map[string]string{"x": "y"}})
	if got.Message != "Group A belum punya leader" || got.Fields["x"] != "y" {
		t.Fatalf("got %+v", got)
	}
}

func TestWriteError_HidesInternalDetail(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/x", nil)
	req = req.WithContext(telemetry.WithCorrelationID(req.Context(), "req-12345678"))
	WriteError(telemetry.NewLogger(&bytes.Buffer{}, "error"))(rec, req, errors.New("password=rahasia"))

	if rec.Code != 500 || strings.Contains(rec.Body.String(), "rahasia") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
	var body struct{ Error APIError }
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error.RequestID != "req-12345678" {
		t.Fatalf("request_id = %q", body.Error.RequestID)
	}
}

func TestDecode(t *testing.T) {
	type input struct {
		Name string `json:"name"`
	}
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"valid", `{"name":"a"}`, false},
		{"field tak dikenal", `{"name":"a","admin":true}`, true},
		{"json rusak", `{"name":`, true},
		{"kosong", ``, true},
		{"dua objek", `{"name":"a"}{"name":"b"}`, true},
		{"terlalu besar", `{"name":"` + strings.Repeat("a", maxBodyBytes) + `"}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var in input
			err := Decode(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader(tt.body)), &in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if err != nil && !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("error harus ValidationError: %v", err)
			}
		})
	}
}

type fakeAuth struct{ loggedIn, admin bool }

func (f fakeAuth) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !f.loggedIn {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(domain.WithPrincipal(r.Context(), domain.Principal{UserID: 1, IsPlatformAdmin: f.admin})))
	})
}
func (f fakeAuth) CSRF(next http.Handler) http.Handler { return next }
func (f fakeAuth) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !Actor(r).IsPlatformAdmin {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (f fakeAuth) RequireDBAccess(domain.AccessLevel) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

func TestRouter_GuardsAndPrefix(t *testing.T) {
	ok := func(w http.ResponseWriter, r *http.Request) error { return OK(w, "hi") }
	serve := func(auth fakeAuth, method, path string) int {
		mux := http.NewServeMux()
		api := NewRouter(mux, auth, WriteError(telemetry.NewLogger(&bytes.Buffer{}, "error")))
		api.Public("GET /ping", ok)
		api.Authed("GET /me", ok)
		api.Admin("GET /admin/users", ok)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec.Code
	}

	tests := []struct {
		name   string
		auth   fakeAuth
		method string
		path   string
		want   int
	}{
		{"public tanpa login", fakeAuth{}, "GET", "/api/v1/ping", 200},
		{"authed tanpa login", fakeAuth{}, "GET", "/api/v1/me", 401},
		{"authed dengan login", fakeAuth{loggedIn: true}, "GET", "/api/v1/me", 200},
		{"admin oleh user biasa", fakeAuth{loggedIn: true}, "GET", "/api/v1/admin/users", 403},
		{"admin oleh admin", fakeAuth{loggedIn: true, admin: true}, "GET", "/api/v1/admin/users", 200},
		{"route tak dikenal", fakeAuth{}, "GET", "/api/v1/nope", 404},
		{"tanpa prefix", fakeAuth{}, "GET", "/ping", 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := serve(tt.auth, tt.method, tt.path); got != tt.want {
				t.Fatalf("status %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRouter_HandlerErrorBecomesJSON(t *testing.T) {
	mux := http.NewServeMux()
	api := NewRouter(mux, fakeAuth{}, WriteError(telemetry.NewLogger(&bytes.Buffer{}, "error")))
	api.Public("GET /fail", func(http.ResponseWriter, *http.Request) error { return domain.ErrConflict })

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/fail", nil))
	if rec.Code != 409 || !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("status %d type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestSPA_FallsBackToIndex(t *testing.T) {
	dist := fstest.MapFS{
		"index.html":    {Data: []byte("<div id=root></div>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	tests := map[string]string{
		"/":              "<div id=root></div>",
		"/admin/users/3": "<div id=root></div>",
		"/assets/app.js": "console.log(1)",
	}
	for path, want := range tests {
		rec := httptest.NewRecorder()
		SPA(dist).ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 || rec.Body.String() != want {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body)
		}
	}
}

// POST ke alamat halaman tidak boleh dijawab seolah berhasil
func TestSPA_RejectsWriteMethods(t *testing.T) {
	dist := fstest.MapFS{"index.html": {Data: []byte("<div id=root></div>")}}
	rec := httptest.NewRecorder()
	SPA(dist).ServeHTTP(rec, httptest.NewRequest("POST", "/databases", nil))
	if rec.Code != 405 || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("status %d, Allow %q", rec.Code, rec.Header().Get("Allow"))
	}
}
