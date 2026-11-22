package auth_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"dbplatform/internal/access"
	"dbplatform/internal/apiv1"
	"dbplatform/internal/auth"
	"dbplatform/internal/clock"
	"dbplatform/internal/telemetry"
	"dbplatform/internal/testutil"
)

type apiApp struct {
	*app
	url string
}

func newAPIApp(t *testing.T) *apiApp {
	t.Helper()
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.NewAppDB(t)
	clk := clock.NewFake(time.Now().UTC().Truncate(time.Second))
	mod := auth.New(auth.Config{Policy: auth.DefaultPolicy(), BcryptCost: bcrypt.MinCost}, auth.Deps{
		Pool: pool, Clock: clk, Render: pageRenderer{}, HandleErr: handleErr, Authorizer: access.New(pool),
	})

	writeErr := apiv1.WriteError(telemetry.NewLogger(io.Discard, "error"))
	mux := http.NewServeMux()
	api := apiv1.NewRouter(mux, mod.Middleware.ForAPI(writeErr), writeErr)
	api.Public("GET /auth/csrf", mod.API.LoginCSRF)
	api.Public("POST /auth/login", mod.API.Login)
	api.Authed("POST /auth/logout", mod.API.Logout)
	api.Authed("GET /auth/me", mod.API.Me)
	api.Authed("POST /ping", func(w http.ResponseWriter, r *http.Request) error { return apiv1.NoContent(w) })

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &apiApp{app: &app{pool: pool, clk: clk, mod: mod, server: srv}, url: srv.URL + "/api/v1"}
}

func callJSON(t *testing.T, c *http.Client, method, url, csrf string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, url, &buf)
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (a *apiApp) login(t *testing.T, c *http.Client, username, password string) (int, map[string]any) {
	t.Helper()
	_, csrf := callJSON(t, c, "GET", a.url+"/auth/csrf", "", nil)
	token := csrf["data"].(map[string]any)["csrf_token"].(string)
	return callJSON(t, c, "POST", a.url+"/auth/login", token, map[string]string{"username": username, "password": password})
}

func TestIntegration_APIMeWithoutSessionIs401(t *testing.T) {
	a := newAPIApp(t)
	status, body := callJSON(t, newClient(t), "GET", a.url+"/auth/me", "", nil)
	if status != http.StatusUnauthorized || body["error"].(map[string]any)["code"] != "unauthenticated" {
		t.Fatalf("status %d body %v", status, body)
	}
}

func TestIntegration_APILoginFlow(t *testing.T) {
	a := newAPIApp(t)
	a.seedUser(t, "alice", "password-alice", false)
	c := newClient(t)

	status, body := a.login(t, c, "Alice", "password-alice")
	if status != http.StatusOK {
		t.Fatalf("login: %d %v", status, body)
	}
	csrf := body["data"].(map[string]any)["csrf_token"].(string)

	status, body = callJSON(t, c, "GET", a.url+"/auth/me", "", nil)
	user := body["data"].(map[string]any)["user"].(map[string]any)
	if status != http.StatusOK || user["username"] != "alice" {
		t.Fatalf("me: %d %v", status, body)
	}

	if status, _ := callJSON(t, c, "POST", a.url+"/ping", "", nil); status != http.StatusForbidden {
		t.Fatalf("POST tanpa CSRF harus 403, dapat %d", status)
	}
	if status, _ := callJSON(t, c, "POST", a.url+"/ping", csrf, nil); status != http.StatusNoContent {
		t.Fatalf("POST dengan CSRF harus 204, dapat %d", status)
	}

	if status, _ := callJSON(t, c, "POST", a.url+"/auth/logout", csrf, nil); status != http.StatusNoContent {
		t.Fatalf("logout: %d", status)
	}
	if status, _ := callJSON(t, c, "GET", a.url+"/auth/me", "", nil); status != http.StatusUnauthorized {
		t.Fatalf("setelah logout harus 401, dapat %d", status)
	}
}

func TestIntegration_APILoginFailuresUseSameMessage(t *testing.T) {
	a := newAPIApp(t)
	a.seedUser(t, "bob", "password-bob1", false)

	_, wrongPass := a.login(t, newClient(t), "bob", "salah-salah-salah")
	_, unknown := a.login(t, newClient(t), "ghost", "salah-salah-salah")
	msg := func(b map[string]any) any { return b["error"].(map[string]any)["message"] }
	if msg(wrongPass) != msg(unknown) || msg(wrongPass) == nil {
		t.Fatalf("pesan harus seragam: %v vs %v", wrongPass, unknown)
	}
}

func TestIntegration_APILoginRequiresCSRF(t *testing.T) {
	a := newAPIApp(t)
	a.seedUser(t, "carol", "password-carol", false)
	status, _ := callJSON(t, newClient(t), "POST", a.url+"/auth/login", "", map[string]string{"username": "carol", "password": "password-carol"})
	if status != http.StatusForbidden {
		t.Fatalf("login tanpa token CSRF harus 403, dapat %d", status)
	}
}

func TestIntegration_APISessionRotationStillWorks(t *testing.T) {
	a := newAPIApp(t)
	a.seedUser(t, "dave", "password-dave", false)
	c := newClient(t)
	if status, _ := a.login(t, c, "dave", "password-dave"); status != http.StatusOK {
		t.Fatal("login gagal")
	}

	a.clk.Advance(auth.DefaultPolicy().RotationInterval + time.Second)
	status, _ := callJSON(t, c, "GET", a.url+"/auth/me", "", nil)
	if status != http.StatusOK {
		t.Fatalf("request setelah rotasi: %d", status)
	}
	a.clk.Advance(time.Minute)
	if status, _ := callJSON(t, c, "GET", a.url+"/auth/me", "", nil); status != http.StatusOK {
		t.Fatalf("cookie hasil rotasi harus tetap berlaku, dapat %d", status)
	}
}
