package auth_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"dbplatform/internal/access"
	"dbplatform/internal/auth"
	"dbplatform/internal/auth/authtest"
	"dbplatform/internal/clock"
	"dbplatform/internal/domain"
)

// pageRenderer menulis token CSRF login ke body agar bisa dibaca client test.
type pageRenderer struct{}

func (pageRenderer) Page(w http.ResponseWriter, _ *http.Request, status int, _ string, data any) {
	w.WriteHeader(status)
	if p, ok := data.(auth.LoginPage); ok {
		_, _ = w.Write([]byte("csrf=" + p.CSRFToken + "\nerror=" + p.Error + "\n"))
	}
}

func handleErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrUnauthenticated):
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	case errors.Is(err, domain.ErrForbidden):
		http.Error(w, "forbidden", http.StatusForbidden)
	case errors.Is(err, domain.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	default:
		http.Error(w, "internal", http.StatusInternalServerError)
	}
}

type app struct {
	pool   *pgxpool.Pool
	clk    *clock.Fake
	mod    *auth.Module
	server *httptest.Server
}

func newApp(t *testing.T) *app {
	t.Helper()
	if testing.Short() {
		t.Skip("integration")
	}
	pool := authtest.NewAppDB(t)
	// Mulai dari waktu nyata: cookie jar memakai jam sistem untuk atribut Expires.
	clk := clock.NewFake(time.Now().UTC().Truncate(time.Second))
	mod := auth.New(auth.Config{Policy: auth.DefaultPolicy(), BcryptCost: bcrypt.MinCost}, auth.Deps{
		Pool: pool, Clock: clk, Render: pageRenderer{}, HandleErr: handleErr, Authorizer: access.New(pool),
	})
	mux := http.NewServeMux()
	mod.Handler.Register(mux)
	mw := mod.Middleware
	// Route contoh yang dibungkus seperti route modul lain.
	whoami := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := domain.PrincipalFrom(r.Context())
		_, _ = w.Write([]byte("user=" + p.Username + "\ncsrf=" + domain.CSRFTokenFrom(r.Context()) + "\n"))
	})
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.Handle("GET /databases", mw.Authenticate(mw.CSRF(whoami)))
	mux.Handle("POST /databases/{dbID}/migrations", mw.Authenticate(mw.CSRF(mw.RequireDBAccess(domain.AccessViewEdit)(ok))))
	mux.Handle("GET /databases/{dbID}", mw.Authenticate(mw.CSRF(mw.RequireDBAccess(domain.AccessViewOnly)(ok))))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &app{pool: pool, clk: clk, mod: mod, server: srv}
}

func (a *app) seedUser(t *testing.T, username, password string, admin bool) domain.User {
	t.Helper()
	hash, err := a.mod.Hasher.Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	return authtest.SeedUser(t, a.pool, username, hash, admin)
}

func newClient(t *testing.T) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func field(body, key string) string {
	for _, line := range strings.Split(body, "\n") {
		if v, ok := strings.CutPrefix(line, key+"="); ok {
			return v
		}
	}
	return ""
}

func get(t *testing.T, c *http.Client, u string) (*http.Response, string) {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b bytes.Buffer
	_, _ = b.ReadFrom(resp.Body)
	return resp, b.String()
}

func postForm(t *testing.T, c *http.Client, u string, form url.Values) (*http.Response, string) {
	t.Helper()
	resp, err := c.PostForm(u, form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b bytes.Buffer
	_, _ = b.ReadFrom(resp.Body)
	return resp, b.String()
}

func (a *app) login(t *testing.T, c *http.Client, username, password string) *http.Response {
	t.Helper()
	_, body := get(t, c, a.server.URL+"/login")
	resp, _ := postForm(t, c, a.server.URL+"/login", url.Values{
		"csrf_token": {field(body, "csrf")}, "username": {username}, "password": {password},
	})
	return resp
}

func sessionCookie(c *http.Client, base string) string {
	u, _ := url.Parse(base)
	for _, ck := range c.Jar.Cookies(u) {
		if ck.Name == auth.SessionCookieName {
			return ck.Value
		}
	}
	return ""
}

func TestAuth_Integration_LoginStoresOnlyHash(t *testing.T) {
	a := newApp(t)
	a.seedUser(t, "alice", "password-alice", false)
	c := newClient(t)
	resp := a.login(t, c, "alice", "password-alice")
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/databases" {
		t.Fatalf("login: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	raw := sessionCookie(c, a.server.URL)
	if raw == "" {
		t.Fatal("cookie session tidak ada")
	}

	ctx := context.Background()
	var n int
	var hash []byte
	if err := a.pool.QueryRow(ctx, `SELECT count(*), (array_agg(token_hash))[1] FROM sessions`).Scan(&n, &hash); err != nil {
		t.Fatal(err)
	}
	if n != 1 || !bytes.Equal(hash, auth.HashToken(raw)) {
		t.Fatalf("session tersimpan: n=%d", n)
	}
	var leaked bool
	if err := a.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM sessions WHERE position(convert_to($1, 'UTF8') IN token_hash) > 0 OR csrf_token = $1)`,
		raw).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked {
		t.Fatal("nilai mentah session tersimpan di database")
	}
	var pwHash string
	_ = a.pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE username = 'alice'`).Scan(&pwHash)
	if !strings.HasPrefix(pwHash, "$2") {
		t.Fatalf("password hash bukan bcrypt: %q", pwHash)
	}

	_, body := get(t, c, a.server.URL+"/databases")
	if field(body, "user") != "alice" {
		t.Fatalf("principal: %q", body)
	}
}

func TestAuth_Integration_WrongPasswordAndUnknownUserSameMessage(t *testing.T) {
	a := newApp(t)
	a.seedUser(t, "alice", "password-alice", false)
	var msgs []string
	for _, u := range []string{"alice", "nobody"} {
		c := newClient(t)
		_, body := get(t, c, a.server.URL+"/login")
		resp, out := postForm(t, c, a.server.URL+"/login", url.Values{
			"csrf_token": {field(body, "csrf")}, "username": {u}, "password": {"wrong-password"},
		})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: status %d", u, resp.StatusCode)
		}
		msgs = append(msgs, field(out, "error"))
	}
	if msgs[0] == "" || msgs[0] != msgs[1] {
		t.Fatalf("pesan berbeda: %q", msgs)
	}
}

func TestAuth_Integration_LogoutRejectsOldCookie(t *testing.T) {
	a := newApp(t)
	a.seedUser(t, "alice", "password-alice", false)
	c := newClient(t)
	a.login(t, c, "alice", "password-alice")
	raw := sessionCookie(c, a.server.URL)
	_, body := get(t, c, a.server.URL+"/databases")

	resp, _ := postForm(t, c, a.server.URL+"/logout", url.Values{"csrf_token": {field(body, "csrf")}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout: %d", resp.StatusCode)
	}

	// Pakai ulang cookie lama secara manual.
	req, _ := http.NewRequest(http.MethodGet, a.server.URL+"/databases", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: raw})
	resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/login") {
		t.Fatalf("cookie lama harus ditolak: %d", resp.StatusCode)
	}
}

func TestAuth_Integration_IdleTimeout(t *testing.T) {
	a := newApp(t)
	a.seedUser(t, "alice", "password-alice", false)
	c := newClient(t)
	a.login(t, c, "alice", "password-alice")
	a.clk.Advance(29 * time.Minute)
	if resp, _ := get(t, c, a.server.URL+"/databases"); resp.StatusCode != http.StatusOK {
		t.Fatalf("masih dalam idle: %d", resp.StatusCode)
	}
	a.clk.Advance(31 * time.Minute)
	if resp, _ := get(t, c, a.server.URL+"/databases"); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("idle habis: %d", resp.StatusCode)
	}
	var reason string
	_ = a.pool.QueryRow(context.Background(), `SELECT revoked_reason FROM sessions`).Scan(&reason)
	if reason != auth.ReasonIdle {
		t.Fatalf("revoked_reason = %q", reason)
	}
}

func TestAuth_Integration_AbsoluteTimeoutNotExtendedByRotation(t *testing.T) {
	a := newApp(t)
	a.seedUser(t, "alice", "password-alice", false)
	c := newClient(t)
	a.login(t, c, "alice", "password-alice")
	var expires time.Time
	_ = a.pool.QueryRow(context.Background(), `SELECT absolute_expires_at FROM sessions`).Scan(&expires)

	// Aktif terus tiap 20 menit (memicu rotasi) sampai melewati 12 jam.
	rotations := 0
	prev := sessionCookie(c, a.server.URL)
	for elapsed := time.Duration(0); elapsed < 12*time.Hour-20*time.Minute; elapsed += 20 * time.Minute {
		a.clk.Advance(20 * time.Minute)
		if resp, _ := get(t, c, a.server.URL+"/databases"); resp.StatusCode != http.StatusOK {
			t.Fatalf("setelah %v: %d", elapsed, resp.StatusCode)
		}
		if cur := sessionCookie(c, a.server.URL); cur != prev {
			rotations++
			prev = cur
		}
	}
	if rotations == 0 {
		t.Fatal("rotasi periodik tidak terjadi")
	}
	var expiresAfter time.Time
	_ = a.pool.QueryRow(context.Background(), `SELECT absolute_expires_at FROM sessions`).Scan(&expiresAfter)
	if !expiresAfter.Equal(expires) {
		t.Fatalf("absolute_expires_at berubah: %v -> %v", expires, expiresAfter)
	}
	a.clk.Advance(20 * time.Minute)
	if resp, _ := get(t, c, a.server.URL+"/databases"); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("absolute habis harus ditolak: %d", resp.StatusCode)
	}
}

func TestAuth_Integration_RotationGracePeriod(t *testing.T) {
	a := newApp(t)
	a.seedUser(t, "alice", "password-alice", false)
	c := newClient(t)
	a.login(t, c, "alice", "password-alice")
	old := sessionCookie(c, a.server.URL)

	a.clk.Advance(16 * time.Minute)
	get(t, c, a.server.URL+"/databases")
	cur := sessionCookie(c, a.server.URL)
	if cur == old {
		t.Fatal("rotasi periodik tidak terjadi")
	}

	useOld := func() int {
		req, _ := http.NewRequest(http.MethodGet, a.server.URL+"/databases", nil)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: old})
		resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		for _, ck := range resp.Cookies() {
			if ck.Name == auth.SessionCookieName && ck.Value != "" {
				t.Fatal("identifier lama dalam grace tidak boleh memicu rotasi lagi")
			}
		}
		return resp.StatusCode
	}
	a.clk.Advance(20 * time.Second)
	if got := useOld(); got != http.StatusOK {
		t.Fatalf("dalam grace: %d", got)
	}
	a.clk.Advance(15 * time.Second)
	if got := useOld(); got != http.StatusSeeOther {
		t.Fatalf("setelah grace: %d", got)
	}
	if resp, _ := get(t, c, a.server.URL+"/databases"); resp.StatusCode != http.StatusOK {
		t.Fatalf("identifier baru: %d", resp.StatusCode)
	}
}

func TestAuth_Integration_MarkRotationNeeded(t *testing.T) {
	a := newApp(t)
	u := a.seedUser(t, "alice", "password-alice", false)
	c := newClient(t)
	a.login(t, c, "alice", "password-alice")
	before := sessionCookie(c, a.server.URL)
	if err := a.mod.Sessions.MarkRotationNeeded(context.Background(), u.ID); err != nil {
		t.Fatal(err)
	}
	get(t, c, a.server.URL+"/databases")
	if sessionCookie(c, a.server.URL) == before {
		t.Fatal("MarkRotationNeeded harus memicu rotasi pada request berikutnya")
	}
	var required bool
	_ = a.pool.QueryRow(context.Background(), `SELECT rotate_required FROM sessions`).Scan(&required)
	if required {
		t.Fatal("rotate_required harus kembali false")
	}
}

func TestAuth_Integration_DeactivatedUserRejected(t *testing.T) {
	a := newApp(t)
	u := a.seedUser(t, "alice", "password-alice", false)
	c := newClient(t)
	a.login(t, c, "alice", "password-alice")
	authtest.SetUserActive(t, a.pool, u.ID, false)
	if resp, _ := get(t, c, a.server.URL+"/databases"); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("user nonaktif: %d", resp.StatusCode)
	}
	if resp := a.login(t, newClient(t), "alice", "password-alice"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("login user nonaktif: %d", resp.StatusCode)
	}
}

func TestAuth_Integration_RevokeAllForUser(t *testing.T) {
	a := newApp(t)
	u := a.seedUser(t, "alice", "password-alice", false)
	c1, c2 := newClient(t), newClient(t)
	a.login(t, c1, "alice", "password-alice")
	a.login(t, c2, "alice", "password-alice")
	if err := a.mod.Sessions.RevokeAllForUser(context.Background(), u.ID); err != nil {
		t.Fatal(err)
	}
	for _, c := range []*http.Client{c1, c2} {
		if resp, _ := get(t, c, a.server.URL+"/databases"); resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("session harus dicabut: %d", resp.StatusCode)
		}
	}
}

func TestAuth_Integration_PostWithoutCSRFForbidden(t *testing.T) {
	a := newApp(t)
	u := a.seedUser(t, "alice", "password-alice", false)
	conn := authtest.SeedConnection(t, a.pool, "sample", true)
	g := authtest.SeedGroup(t, a.pool, "editors", u.ID)
	authtest.Grant(t, a.pool, g, conn, domain.AccessViewEdit)

	c := newClient(t)
	a.login(t, c, "alice", "password-alice")
	_, body := get(t, c, a.server.URL+"/databases")
	target := a.server.URL + "/databases/" + itoa(conn) + "/migrations"

	if resp, _ := postForm(t, c, target, url.Values{}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("tanpa CSRF: %d", resp.StatusCode)
	}
	if resp, _ := postForm(t, c, a.server.URL+"/logout", url.Values{}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("logout tanpa CSRF: %d", resp.StatusCode)
	}
	if resp, _ := postForm(t, c, target, url.Values{"csrf_token": {field(body, "csrf")}}); resp.StatusCode != http.StatusOK {
		t.Fatalf("dengan CSRF: %d", resp.StatusCode)
	}
}

func TestAuth_Integration_RequireDBAccessViewOnlyOnEditRoute(t *testing.T) {
	a := newApp(t)
	u := a.seedUser(t, "viewer", "password-viewer", false)
	conn := authtest.SeedConnection(t, a.pool, "sample", true)
	g := authtest.SeedGroup(t, a.pool, "viewers", u.ID)
	authtest.Grant(t, a.pool, g, conn, domain.AccessViewOnly)

	c := newClient(t)
	a.login(t, c, "viewer", "password-viewer")
	_, body := get(t, c, a.server.URL+"/databases")
	if resp, _ := get(t, c, a.server.URL+"/databases/"+itoa(conn)); resp.StatusCode != http.StatusOK {
		t.Fatalf("view route: %d", resp.StatusCode)
	}
	resp, _ := postForm(t, c, a.server.URL+"/databases/"+itoa(conn)+"/migrations", url.Values{"csrf_token": {field(body, "csrf")}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("edit route untuk view_only: %d", resp.StatusCode)
	}
}

func TestSeedAdmin_Integration_Idempotent(t *testing.T) {
	a := newApp(t)
	ctx := context.Background()
	created, err := auth.SeedAdmin(ctx, a.pool, a.mod.Hasher, "Admin", "super-secret-pw")
	if err != nil || !created {
		t.Fatalf("seed pertama: created=%v err=%v", created, err)
	}
	created, err = auth.SeedAdmin(ctx, a.pool, a.mod.Hasher, "admin", "other-password")
	if err != nil || created {
		t.Fatalf("seed kedua harus no-op: created=%v err=%v", created, err)
	}
	var n int
	_ = a.pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE is_platform_admin`).Scan(&n)
	if n != 1 {
		t.Fatalf("jumlah admin = %d", n)
	}
	if resp := a.login(t, newClient(t), "admin", "super-secret-pw"); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login admin seed: %d", resp.StatusCode)
	}
	if _, err := auth.SeedAdmin(ctx, a.pool, a.mod.Hasher, "x", "super-secret-pw"); err == nil {
		t.Fatal("username tidak valid harus ditolak")
	}
}

func TestSessionRepo_Integration_DeleteExpired(t *testing.T) {
	a := newApp(t)
	u := a.seedUser(t, "alice", "password-alice", false)
	repo := auth.NewRepo(a.pool)
	ctx := context.Background()
	now := a.clk.Now()
	mk := func(tok string, expires time.Time) int64 {
		id, err := repo.Create(ctx, auth.NewSession{UserID: u.ID, TokenHash: auth.HashToken(tok), CSRFToken: "c", CreatedAt: now.Add(-13 * time.Hour), AbsoluteExpiresAt: expires})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	mk("expired", now.Add(-time.Hour))
	alive := mk("alive", now.Add(time.Hour))
	oldRevoked := mk("old-revoked", now.Add(time.Hour))
	_ = repo.Revoke(ctx, oldRevoked, now.Add(-8*24*time.Hour), auth.ReasonLogout)
	recent := mk("recent-revoked", now.Add(time.Hour))
	_ = repo.Revoke(ctx, recent, now.Add(-time.Hour), auth.ReasonLogout)

	n, err := repo.DeleteExpired(ctx, now)
	if err != nil || n != 2 {
		t.Fatalf("deleted=%d err=%v", n, err)
	}
	for _, id := range []int64{alive, recent} {
		var exists bool
		_ = a.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sessions WHERE id = $1)`, id).Scan(&exists)
		if !exists {
			t.Fatalf("session %d tidak boleh terhapus", id)
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
