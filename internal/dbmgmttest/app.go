package dbmgmttest

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"dbplatform/internal/access"
	"dbplatform/internal/auth"
	"dbplatform/internal/clock"
	"dbplatform/internal/connmgr"
	"dbplatform/internal/dbdriver/postgres"
	"dbplatform/internal/domain"
	"dbplatform/internal/httpx"
	"dbplatform/internal/testutil"
	"dbplatform/internal/viewer"
	webassets "dbplatform/web"
)

const Password = "password-rahasia-123"

type App struct {
	Pool*pgxpool.Pool 
	Target *pgxpool.Pool 
	Server *httptest.Server
	Hasher *auth.Hasher
	Service *connmgr.Service
	Repo*connmgr.Repo
	Drivers *connmgr.Provider
	ConnID int64
}

var csrfRe = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

func New(t *testing.T) *App {
	t.Helper()
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.NewAppDB(t)
	target := testutil.NewTargetDB(t, sampleSQL(t))

	key := bytes.Repeat([]byte{42}, 32)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	flash := httpx.NewFlasher(key, false)
	render, err := httpx.NewRenderer(webassets.FS, flash)
	if err != nil {
		t.Fatal(err)
	}
	handleErr := httpx.HandleError(render, logger)

	repo := connmgr.NewRepo(pool, key)
	provider := connmgr.NewProvider(repo, postgres.New)
	t.Cleanup(provider.Close)
	svc := connmgr.NewService(repo, postgres.New)

	authz := access.New(pool)
	mod := auth.New(auth.Config{Policy: auth.DefaultPolicy(), BcryptCost: bcrypt.MinCost}, auth.Deps{
		Pool: pool, Clock: clock.System{}, Render: render, HandleErr: handleErr, Authorizer: authz, Logger: logger,
	})

	d := httpx.Deps{
		Pool: pool, Clock: clock.System{}, Logger: logger, Render: render, Flash: flash,
		HandleErr: handleErr, Auth: mod.Middleware, Authz: authz, Drivers: provider,
	}
	mux := http.NewServeMux()
	mod.Handler.Register(mux)
	connmgr.NewHandler(svc, d).Register(mux)
	viewer.NewHandler(viewer.NewService(authz, provider), d).Register(mux)

	srv := httptest.NewServer(httpx.Chain(
		httpx.WithRequestID, httpx.Recover(logger, handleErr), httpx.SecurityHeaders,
	)(mux))
	t.Cleanup(srv.Close)

	a := &App{Pool: pool, Target: target, Server: srv, Hasher: mod.Hasher, Service: svc, Repo: repo, Drivers: provider}
	conn, err := svc.Create(context.Background(), domain.Principal{IsPlatformAdmin: true}, a.TargetInput("Sampel"))
	if err != nil {
		t.Fatalf("daftarkan koneksi sampel: %v", err)
	}
	a.ConnID = conn.ID
	return a
}

func (a *App) TargetInput(name string) domain.ConnectionInput {
	cc := a.Target.Config().ConnConfig
	return domain.ConnectionInput{
		Name: name, DBMS: domain.DBMSPostgres, Host: cc.Host, Port: int(cc.Port),
		DatabaseName: cc.Database, Username: cc.User, Password: cc.Password, SSLMode: "disable",
	}
}

func sampleSQL(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "docker", "targetdb", "init.sql"))
	if err != nil {
		t.Fatalf("baca init.sql: %v", err)
	}
	return string(b)
}

// membuat user dgn password
func (a *App) SeedUser(t *testing.T, username string, admin bool) domain.User {
	t.Helper()
	hash, err := a.Hasher.Hash(Password)
	if err != nil {
		t.Fatal(err)
	}
	return testutil.SeedUserWithHash(t, a.Pool, username, hash, admin)
}

// buat group berisi user, memberi akses ke koneksi sampel
func (a *App) GrantTo(t *testing.T, group string, user domain.User, level domain.AccessLevel) {
	t.Helper()
	g := testutil.SeedGroup(t, a.Pool, group, user.ID)
	testutil.Grant(t, a.Pool, g, a.ConnID, level)
}

// http.Client w cookie jar yang tdk mengikuti redirect
func (a *App) Client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}


func (a *App) Login(t *testing.T, c *http.Client, username string) {
	t.Helper()
	_, body := a.Get(t, c, "/login")
	resp, _ := a.PostRaw(t, c, "/login", url.Values{
		"csrf_token": {FindCSRF(t, body)}, "username": {username}, "password": {Password},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login %s: status %d", username, resp.StatusCode)
	}
}

// ambil path dan mengembalikan respons & body
func (a *App) Get(t *testing.T, c *http.Client, path string) (*http.Response, string) {
	t.Helper()
	resp, err := c.Get(a.Server.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	return resp, readBody(t, resp)
}

//  ngirim forms tanpa menambah token CSRF
func (a *App) PostRaw(t *testing.T, c *http.Client, path string, form url.Values) (*http.Response, string) {
	t.Helper()
	resp, err := c.PostForm(a.Server.URL+path, form)
	if err != nil {
		t.Fatal(err)
	}
	return resp, readBody(t, resp)
}

// ngirim form dengan token CSRF session saat ini
func (a *App) Post(t *testing.T, c *http.Client, path string, form url.Values) (*http.Response, string) {
	t.Helper()
	_, body := a.Get(t, c, "/databases")
	f := url.Values{}
	for k, v := range form {
		f[k] = v
	}
	f.Set("csrf_token", FindCSRF(t, body))
	return a.PostRaw(t, c, path, f)
}

// ambil nilai csrf_token pertama dari HTML
func FindCSRF(t *testing.T, html string) string {
	t.Helper()
	m := csrfRe.FindStringSubmatch(html)
	if m == nil {
		t.Fatalf("csrf_token tidak ditemukan di halaman:\n%.400s", html)
	}
	return m[1]
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
