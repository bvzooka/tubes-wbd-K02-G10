package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"dbplatform/internal/clock"
)

type loginFixture struct {
	clk    *clock.Fake
	store  *fakeStore
	render *fakeRenderer
	mux    *http.ServeMux
}

func newLoginFixture(t *testing.T) *loginFixture {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC))
	store := newFakeStore()
	store.addUser(1, "alice", false, true)
	store.addUser(2, "bob", false, false)
	hasher := NewHasher(bcrypt.MinCost)
	hash, err := hasher.Hash("password-alice")
	if err != nil {
		t.Fatal(err)
	}
	users := fakeUsers{
		"alice": {ID: 1, Username: "alice", PasswordHash: hash, IsActive: true},
		"bob":   {ID: 2, Username: "bob", PasswordHash: hash, IsActive: false},
	}
	mgr := NewManager(store, DefaultPolicy(), clk, nil)
	render := &fakeRenderer{}
	h := NewHandler(HandlerDeps{
		Sessions: mgr, Users: users, Verifier: hasher, Render: render, HandleErr: testHandleErr,
		MW: NewMiddleware(mgr, fakeAuthorizer{}, testHandleErr, false), Clock: clk,
	})
	mux := http.NewServeMux()
	h.Register(mux)
	return &loginFixture{clk: clk, store: store, render: render, mux: mux}
}

// getForm memanggil GET /login dan mengembalikan cookie dan token CSRF login.
func (f *loginFixture) getForm(t *testing.T) (*http.Cookie, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login", nil))
	if rec.Code != http.StatusOK || f.render.name != "auth/login.html" {
		t.Fatalf("GET /login: %d %s", rec.Code, f.render.name)
	}
	c := responseCookie(rec, LoginCSRFCookieName)
	page := f.render.data.(LoginPage)
	if c == nil || c.Value != page.CSRFToken {
		t.Fatal("cookie dan field CSRF login harus sama")
	}
	return c, page.CSRFToken
}

func (f *loginFixture) post(t *testing.T, cookie *http.Cookie, form url.Values, extra ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	for _, c := range extra {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, r)
	return rec
}

func TestLogin_Success(t *testing.T) {
	f := newLoginFixture(t)
	c, tok := f.getForm(t)
	rec := f.post(t, c, url.Values{"csrf_token": {tok}, "username": {"  Alice "}, "password": {"password-alice"}, "next": {"/databases/3"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/databases/3" {
		t.Fatalf("status=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
	sc := responseCookie(rec, SessionCookieName)
	if sc == nil || sc.Value == "" || !sc.HttpOnly {
		t.Fatalf("cookie session: %+v", sc)
	}
	if f.store.row(1).UserID != 1 {
		t.Fatal("session tidak dibuat")
	}
	if strings.Contains(rec.Header().Get("Location"), sc.Value) || strings.Contains(rec.Body.String(), sc.Value) {
		t.Fatal("session id tidak boleh muncul di URL atau body")
	}
}

func TestLogin_FailureMessagesIdentical(t *testing.T) {
	f := newLoginFixture(t)
	var msgs []string
	for _, form := range []url.Values{
		{"username": {"alice"}, "password": {"wrong-password"}},
		{"username": {"nobody"}, "password": {"wrong-password"}},
		{"username": {"bob"}, "password": {"password-alice"}}, // user nonaktif
	} {
		c, tok := f.getForm(t)
		form.Set("csrf_token", tok)
		rec := f.post(t, c, form)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%v: status = %d", form, rec.Code)
		}
		if responseCookie(rec, SessionCookieName) != nil {
			t.Fatal("login gagal tidak boleh membuat session")
		}
		page := f.render.data.(LoginPage)
		msgs = append(msgs, page.Error)
	}
	if msgs[0] != msgInvalidCredentials || msgs[0] != msgs[1] || msgs[1] != msgs[2] {
		t.Fatalf("pesan harus identik: %q", msgs)
	}
}

func TestLogin_CSRFMismatch(t *testing.T) {
	f := newLoginFixture(t)
	c, _ := f.getForm(t)
	form := url.Values{"csrf_token": {"forged"}, "username": {"alice"}, "password": {"password-alice"}}
	if rec := f.post(t, c, form); rec.Code != http.StatusForbidden {
		t.Fatalf("token salah: %d", rec.Code)
	}
	form.Set("csrf_token", c.Value)
	if rec := f.post(t, nil, form); rec.Code != http.StatusForbidden {
		t.Fatalf("tanpa cookie: %d", rec.Code)
	}
}

func TestLogin_RateLimit(t *testing.T) {
	f := newLoginFixture(t)
	for i := 0; i < loginMaxFailures; i++ {
		c, tok := f.getForm(t)
		f.post(t, c, url.Values{"csrf_token": {tok}, "username": {"alice"}, "password": {"wrong-password"}})
	}
	c, tok := f.getForm(t)
	rec := f.post(t, c, url.Values{"csrf_token": {tok}, "username": {"alice"}, "password": {"password-alice"}})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	f.clk.Advance(loginFailureWindow)
	c, tok = f.getForm(t)
	rec = f.post(t, c, url.Values{"csrf_token": {tok}, "username": {"alice"}, "password": {"password-alice"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("setelah jendela berlalu: %d", rec.Code)
	}
}

func TestLogin_RevokesPreviousSessionFromCookie(t *testing.T) {
	f := newLoginFixture(t)
	login := func(extra ...*http.Cookie) *http.Cookie {
		c, tok := f.getForm(t)
		rec := f.post(t, c, url.Values{"csrf_token": {tok}, "username": {"alice"}, "password": {"password-alice"}}, extra...)
		return responseCookie(rec, SessionCookieName)
	}
	first := login()
	second := login(first)
	if second.Value == first.Value {
		t.Fatal("login harus membuat identifier baru")
	}
	if r := f.store.row(1); r.revokedReason != ReasonRelogin {
		t.Fatalf("session lama harus dicabut, reason=%q", r.revokedReason)
	}
}

func TestLogout(t *testing.T) {
	f := newLoginFixture(t)
	c, tok := f.getForm(t)
	sc := responseCookie(f.post(t, c, url.Values{"csrf_token": {tok}, "username": {"alice"}, "password": {"password-alice"}}), SessionCookieName)

	// Ambil token CSRF session dari store.
	csrf := f.store.row(1).CSRFToken
	r := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(url.Values{"csrf_token": {csrf}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(sc)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, r)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("logout: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if ck := responseCookie(rec, SessionCookieName); ck == nil || ck.MaxAge != -1 {
		t.Fatal("cookie harus dihapus")
	}
	if f.store.row(1).revokedReason != ReasonLogout {
		t.Fatal("session harus dicabut")
	}
}

func TestLoginForm_AlreadyLoggedInRedirects(t *testing.T) {
	f := newLoginFixture(t)
	c, tok := f.getForm(t)
	sc := responseCookie(f.post(t, c, url.Values{"csrf_token": {tok}, "username": {"alice"}, "password": {"password-alice"}}), SessionCookieName)
	r := httptest.NewRequest(http.MethodGet, "/login", nil)
	r.AddCookie(sc)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, r)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/databases" {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestSafeNext(t *testing.T) {
	tests := map[string]string{
		"":                       "/databases",
		"/databases/1":           "/databases/1",
		"/x?y=1":                 "/x?y=1",
		"//evil.example":         "/databases",
		"/\\evil.example":        "/databases",
		"https://evil.example/":  "/databases",
		"databases":              "/databases",
		"/ok\r\nSet-Cookie: a=b": "/databases",
	}
	for in, want := range tests {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}
