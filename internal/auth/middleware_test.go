package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"dbplatform/internal/clock"
	"dbplatform/internal/domain"
)

type mwFixture struct {
	clk   *clock.Fake
	store *fakeStore
	mgr   *Manager
	mw    *Middleware
}

func newMWFixture(t *testing.T) *mwFixture {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC))
	store := newFakeStore()
	store.addUser(1, "alice", false, true)
	store.addUser(2, "root", true, true)
	mgr := NewManager(store, DefaultPolicy(), clk, nil)
	authz := fakeAuthorizer{levels: map[[2]int64]domain.AccessLevel{
		{1, 10}: domain.AccessViewOnly,
		{1, 11}: domain.AccessViewEdit,
	}}
	return &mwFixture{clk: clk, store: store, mgr: mgr, mw: NewMiddleware(mgr, authz, testHandleErr, false)}
}

func (f *mwFixture) login(t *testing.T, userID int64) string {
	t.Helper()
	s, err := f.mgr.Start(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	return s.Token
}

// capture menyimpan principal dan token CSRF yang dilihat handler.
type capture struct {
	called    bool
	principal domain.Principal
	csrf      string
}

func (c *capture) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.called = true
		c.principal, _ = domain.PrincipalFrom(r.Context())
		c.csrf = domain.CSRFTokenFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})
}

func withCookie(r *http.Request, token string) *http.Request {
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
	return r
}

func responseCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestMiddleware_AuthenticateWithoutSessionRedirects(t *testing.T) {
	f := newMWFixture(t)
	var c capture
	rec := httptest.NewRecorder()
	f.mw.Authenticate(c.handler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/databases/3?tab=data", nil))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login?next="+url.QueryEscape("/databases/3?tab=data") {
		t.Fatalf("Location = %q", loc)
	}
	if c.called {
		t.Fatal("handler tidak boleh dipanggil")
	}

	rec = httptest.NewRecorder()
	f.mw.Authenticate(c.handler()).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/logout", nil))
	if rec.Header().Get("Location") != "/login" {
		t.Fatalf("POST harus redirect ke /login tanpa next, got %q", rec.Header().Get("Location"))
	}
}

func TestMiddleware_AuthenticateValidSession(t *testing.T) {
	f := newMWFixture(t)
	tok := f.login(t, 1)
	var c capture
	rec := httptest.NewRecorder()
	f.mw.Authenticate(c.handler()).ServeHTTP(rec, withCookie(httptest.NewRequest(http.MethodGet, "/databases", nil), tok))
	if rec.Code != http.StatusOK || !c.called {
		t.Fatalf("status = %d called=%v", rec.Code, c.called)
	}
	if c.principal.UserID != 1 || c.principal.Username != "alice" || c.principal.IsPlatformAdmin {
		t.Fatalf("principal = %+v", c.principal)
	}
	if c.csrf == "" {
		t.Fatal("token CSRF tidak ada di context")
	}
	if responseCookie(rec, SessionCookieName) != nil {
		t.Fatal("tidak boleh ada cookie baru tanpa rotasi")
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("halaman terautentikasi harus no-store")
	}
}

func TestMiddleware_AuthenticateInvalidCookieClearsIt(t *testing.T) {
	f := newMWFixture(t)
	var c capture
	rec := httptest.NewRecorder()
	f.mw.Authenticate(c.handler()).ServeHTTP(rec, withCookie(httptest.NewRequest(http.MethodGet, "/", nil), "bogus"))
	if rec.Code != http.StatusSeeOther || c.called {
		t.Fatalf("status = %d", rec.Code)
	}
	if ck := responseCookie(rec, SessionCookieName); ck == nil || ck.MaxAge != -1 {
		t.Fatalf("cookie harus dihapus: %+v", ck)
	}
}

func TestMiddleware_RotationSetsNewCookieAndGrace(t *testing.T) {
	f := newMWFixture(t)
	old := f.login(t, 1)
	f.clk.Advance(16 * time.Minute) // lewat rotation interval, masih dalam idle 30m

	var c capture
	rec := httptest.NewRecorder()
	f.mw.Authenticate(c.handler()).ServeHTTP(rec, withCookie(httptest.NewRequest(http.MethodGet, "/", nil), old))
	nc := responseCookie(rec, SessionCookieName)
	if nc == nil || nc.Value == "" || nc.Value == old {
		t.Fatalf("harus ada cookie baru, got %+v", nc)
	}
	if !nc.Expires.Equal(f.clk.Now().Add(-16 * time.Minute).Add(12 * time.Hour)) {
		t.Fatalf("Expires harus tetap absolute_expires_at, got %v", nc.Expires)
	}

	// Identifier lama masih berlaku dalam grace, tanpa rotasi lagi.
	f.clk.Advance(10 * time.Second)
	rec = httptest.NewRecorder()
	f.mw.Authenticate(c.handler()).ServeHTTP(rec, withCookie(httptest.NewRequest(http.MethodGet, "/", nil), old))
	if rec.Code != http.StatusOK || responseCookie(rec, SessionCookieName) != nil {
		t.Fatalf("token lama dalam grace: status=%d", rec.Code)
	}

	// Setelah grace, identifier lama tidak berlaku; yang baru tetap berlaku.
	f.clk.Advance(30 * time.Second)
	rec = httptest.NewRecorder()
	f.mw.Authenticate(c.handler()).ServeHTTP(rec, withCookie(httptest.NewRequest(http.MethodGet, "/", nil), old))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("token lama setelah grace harus ditolak, status=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	f.mw.Authenticate(c.handler()).ServeHTTP(rec, withCookie(httptest.NewRequest(http.MethodGet, "/", nil), nc.Value))
	if rec.Code != http.StatusOK {
		t.Fatalf("token baru harus berlaku, status=%d", rec.Code)
	}
}

func TestMiddleware_CSRF(t *testing.T) {
	f := newMWFixture(t)
	const token = "session-csrf-token"
	newReq := func(method, field, origin string) *http.Request {
		var body *strings.Reader
		if field != "" {
			body = strings.NewReader(url.Values{"csrf_token": {field}}.Encode())
		} else {
			body = strings.NewReader("")
		}
		r := httptest.NewRequest(method, "http://app.local/x", body)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r.WithContext(domain.WithCSRFToken(r.Context(), token))
	}
	tests := []struct {
		name string
		req  *http.Request
		want int
	}{
		{"GET tanpa token", newReq(http.MethodGet, "", ""), http.StatusOK},
		{"POST token cocok", newReq(http.MethodPost, token, ""), http.StatusOK},
		{"POST token cocok origin sama", newReq(http.MethodPost, token, "http://app.local"), http.StatusOK},
		{"POST tanpa token", newReq(http.MethodPost, "", ""), http.StatusForbidden},
		{"POST token salah", newReq(http.MethodPost, "other", ""), http.StatusForbidden},
		{"POST origin asing", newReq(http.MethodPost, token, "http://evil.example"), http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var c capture
			rec := httptest.NewRecorder()
			f.mw.CSRF(c.handler()).ServeHTTP(rec, tc.req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}

	// Token lewat header juga diterima.
	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r.Header.Set(CSRFHeaderName, token)
	r = r.WithContext(domain.WithCSRFToken(r.Context(), token))
	rec := httptest.NewRecorder()
	var c capture
	f.mw.CSRF(c.handler()).ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("header token: status = %d", rec.Code)
	}
}

func TestMiddleware_RequireAdmin(t *testing.T) {
	f := newMWFixture(t)
	run := func(p *domain.Principal) int {
		r := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
		if p != nil {
			r = r.WithContext(domain.WithPrincipal(r.Context(), *p))
		}
		rec := httptest.NewRecorder()
		var c capture
		f.mw.RequireAdmin(c.handler()).ServeHTTP(rec, r)
		return rec.Code
	}
	if got := run(&domain.Principal{UserID: 2, IsPlatformAdmin: true}); got != http.StatusOK {
		t.Errorf("admin: %d", got)
	}
	if got := run(&domain.Principal{UserID: 1}); got != http.StatusForbidden {
		t.Errorf("non-admin: %d", got)
	}
	if got := run(nil); got != http.StatusSeeOther {
		t.Errorf("tanpa principal: %d", got)
	}
}

func TestMiddleware_RequireDBAccess(t *testing.T) {
	f := newMWFixture(t)
	tests := []struct {
		name   string
		user   int64
		dbID   string
		min    domain.AccessLevel
		status int
	}{
		{"view_only ke route view", 1, "10", domain.AccessViewOnly, http.StatusOK},
		{"view_only ke route edit", 1, "10", domain.AccessViewEdit, http.StatusForbidden},
		{"view_edit ke route edit", 1, "11", domain.AccessViewEdit, http.StatusOK},
		{"view_edit ke route view", 1, "11", domain.AccessViewOnly, http.StatusOK},
		{"tanpa akses", 1, "12", domain.AccessViewOnly, http.StatusForbidden},
		{"admin tanpa group", 2, "10", domain.AccessViewOnly, http.StatusForbidden},
		{"dbID bukan angka", 1, "abc", domain.AccessViewOnly, http.StatusNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			var c capture
			mux.Handle("GET /databases/{dbID}", f.mw.RequireDBAccess(tc.min)(c.handler()))
			r := httptest.NewRequest(http.MethodGet, "/databases/"+tc.dbID, nil)
			r = r.WithContext(domain.WithPrincipal(r.Context(), domain.Principal{UserID: tc.user}))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, r)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
			if c.called != (tc.status == http.StatusOK) {
				t.Fatalf("handler called = %v", c.called)
			}
		})
	}
}

func TestManager_IdleAndUserInactive(t *testing.T) {
	f := newMWFixture(t)
	ctx := context.Background()
	tok := f.login(t, 1)

	f.clk.Advance(31 * time.Minute)
	if _, err := f.mgr.Resolve(ctx, tok); err != domain.ErrUnauthenticated {
		t.Fatalf("idle: err = %v", err)
	}
	if r := f.store.row(1); r.revokedReason != ReasonIdle {
		t.Fatalf("revoked_reason = %q", r.revokedReason)
	}

	tok2 := f.login(t, 1)
	f.store.addUser(1, "alice", false, false)
	if _, err := f.mgr.Resolve(ctx, tok2); err != domain.ErrUnauthenticated {
		t.Fatalf("user nonaktif: err = %v", err)
	}
	if r := f.store.row(2); r.revokedReason != ReasonUserInactive {
		t.Fatalf("revoked_reason = %q", r.revokedReason)
	}
}

func TestManager_MarkRotationNeeded(t *testing.T) {
	f := newMWFixture(t)
	ctx := context.Background()
	tok := f.login(t, 1)
	if err := f.mgr.MarkRotationNeeded(ctx, 1); err != nil {
		t.Fatal(err)
	}
	res, err := f.mgr.Resolve(ctx, tok)
	if err != nil {
		t.Fatal(err)
	}
	if res.NewToken == "" {
		t.Fatal("MarkRotationNeeded harus memicu rotasi")
	}
	res, err = f.mgr.Resolve(ctx, res.NewToken)
	if err != nil || res.NewToken != "" {
		t.Fatalf("rotasi hanya sekali: %+v %v", res, err)
	}
}
