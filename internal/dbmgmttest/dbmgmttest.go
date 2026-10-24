package dbmgmttest

import (
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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/access"
	"dbplatform/internal/auth"
	"dbplatform/internal/clock"
	"dbplatform/internal/connmgr"
	"dbplatform/internal/dbdriver/postgres"
	"dbplatform/internal/domain"
	"dbplatform/internal/explain"
	"dbplatform/internal/httpx"
	"dbplatform/internal/testutil"
	"dbplatform/internal/viewer"
	webassets "dbplatform/web"
)

const Password = "Password-Uji-123"

var csrfField = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

type App struct {
	Pool   *pgxpool.Pool
	Target *pgxpool.Pool
	Repo   *connmgr.Repo
	ConnID int64
	Server *httptest.Server

	hasher *auth.Hasher
	target domain.ConnectionInput

	mu     sync.Mutex
	groups map[string]int64
}

func New(t *testing.T) *App {
	t.Helper()
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()

	pool := testutil.NewAppDB(t)
	initSQL, err := os.ReadFile(filepath.Join(repoRoot(t), "docker", "targetdb", "init.sql"))
	if err != nil {
		t.Fatalf("baca init.sql target: %v", err)
	}
	target := testutil.NewTargetDB(t, string(initSQL), "ANALYZE")

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var clk clock.Clock = clock.System{}

	flash := httpx.NewFlasher(key, false)
	render, err := httpx.NewRenderer(webassets.FS, flash)
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	handleErr := httpx.HandleError(render, logger)

	authz := access.New(pool)
	mod := auth.New(auth.Config{
		Policy:     auth.DefaultPolicy(),
		BcryptCost: 4,
	}, auth.Deps{
		Pool: pool, Clock: clk, Render: render, HandleErr: handleErr, Authorizer: authz, Logger: logger,
	})

	repo := connmgr.NewRepo(pool, key)
	drivers := connmgr.NewProvider(repo, postgres.New)
	t.Cleanup(drivers.Close)

	d := httpx.Deps{
		Pool: pool, Clock: clk, Logger: logger, Render: render, Flash: flash, HandleErr: handleErr,
		Auth: mod.Middleware, Authz: authz, Drivers: drivers,
	}
	mux := http.NewServeMux()
	mod.Handler.Register(mux)
	connmgr.NewHandler(connmgr.NewService(repo, postgres.New), d).Register(mux)
	viewer.NewHandler(viewer.NewService(authz, drivers), d).Register(mux)
	explain.NewHandler(explain.NewService(authz, drivers), render, handleErr, mod.Middleware).Register(mux)

	handler := httpx.Chain(
		httpx.WithRequestID,
		httpx.Recover(logger, handleErr),
		httpx.SecurityHeaders,
	)(mux)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	cc := target.Config().ConnConfig
	in := domain.ConnectionInput{
		Name:         "Sampel",
		DBMS:         domain.DBMSPostgres,
		Host:         cc.Host,
		Port:         int(cc.Port),
		DatabaseName: cc.Database,
		Username:     cc.User,
		Password:     cc.Password,
		SSLMode:      "disable",
	}
	conn, err := repo.Insert(ctx, in, 0)
	if err != nil {
		t.Fatalf("insert koneksi sampel: %v", err)
	}

	return &App{
		Pool: pool, Target: target, Repo: repo, ConnID: conn.ID, Server: srv,
		hasher: mod.Hasher, target: in, groups: map[string]int64{},
	}
}

func (a *App) TargetInput(name string) domain.ConnectionInput {
	in := a.target
	in.Name = name
	return in
}

func (a *App) SeedUser(t *testing.T, username string, admin bool) domain.User {
	t.Helper()
	hash, err := a.hasher.Hash(Password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	return testutil.SeedUserWithHash(t, a.Pool, username, hash, admin)
}

func (a *App) GrantTo(t *testing.T, group string, u domain.User, level domain.AccessLevel) {
	t.Helper()
	a.mu.Lock()
	id, ok := a.groups[group]
	a.mu.Unlock()
	if ok {
		if _, err := a.Pool.Exec(context.Background(),
			`INSERT INTO group_members (group_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, id, u.ID); err != nil {
			t.Fatalf("tambah anggota: %v", err)
		}
	} else {
		id = testutil.SeedGroup(t, a.Pool, group, u.ID)
		a.mu.Lock()
		a.groups[group] = id
		a.mu.Unlock()
	}
	testutil.Grant(t, a.Pool, id, a.ConnID, level)
}

func (a *App) Client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar:     jar,
		Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (a *App) Login(t *testing.T, c *http.Client, username string) {
	t.Helper()
	_, body := a.Get(t, c, "/login")
	m := csrfField.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("form login tidak memuat csrf_token")
	}
	form := url.Values{"username": {username}, "password": {Password}, "csrf_token": {m[1]}}
	resp, _ := a.do(t, c, http.MethodPost, "/login", form)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login %s: status %d", username, resp.StatusCode)
	}
}

func (a *App) Get(t *testing.T, c *http.Client, path string) (*http.Response, string) {
	t.Helper()
	return a.do(t, c, http.MethodGet, path, nil)
}

func (a *App) Post(t *testing.T, c *http.Client, path string, form url.Values) (*http.Response, string) {
	t.Helper()
	if form == nil {
		form = url.Values{}
	} else {
		form = cloneValues(form)
	}
	form.Set("csrf_token", a.csrfToken(t, c))
	return a.do(t, c, http.MethodPost, path, form)
}

func (a *App) PostRaw(t *testing.T, c *http.Client, path string, form url.Values) (*http.Response, string) {
	t.Helper()
	if form == nil {
		form = url.Values{}
	}
	return a.do(t, c, http.MethodPost, path, form)
}

func (a *App) csrfToken(t *testing.T, c *http.Client) string {
	t.Helper()
	u, _ := url.Parse(a.Server.URL)
	for _, ck := range c.Jar.Cookies(u) {
		if ck.Name != auth.SessionCookieName {
			continue
		}
		var tok string
		err := a.Pool.QueryRow(context.Background(),
			`SELECT csrf_token FROM sessions WHERE token_hash = $1 OR prev_token_hash = $1`,
			auth.HashToken(ck.Value)).Scan(&tok)
		if err != nil {
			t.Fatalf("ambil csrf token session: %v", err)
		}
		return tok
	}
	return ""
}

func (a *App) do(t *testing.T, c *http.Client, method, path string, form url.Values) (*http.Response, string) {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, a.Server.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(b)
}

func cloneValues(v url.Values) url.Values {
	out := make(url.Values, len(v))
	for k, vs := range v {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller gagal")
	}
	return filepath.Join(filepath.Dir(file), "..", "..")
}
