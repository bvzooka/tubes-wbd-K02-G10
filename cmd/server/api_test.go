package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

// apiClient memakai /api/v1 seperti SPA: cookie jar, token CSRF di header.
type apiClient struct {
	t    *testing.T
	base string
	http *http.Client
	csrf string
}

func newAPIClient(t *testing.T, base string) *apiClient {
	jar, _ := cookiejar.New(nil)
	return &apiClient{t: t, base: base + "/api/v1", http: &http.Client{Jar: jar}}
}

func (c *apiClient) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, c.base+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (c *apiClient) must(want int, method, path string, body any) map[string]any {
	c.t.Helper()
	status, out := c.do(method, path, body)
	if status != want {
		c.t.Fatalf("%s %s: status %d, want %d, body %v", method, path, status, want, out)
	}
	return out
}

func (c *apiClient) login(username, password string) {
	c.t.Helper()
	c.csrf = c.must(200, "GET", "/auth/csrf", nil)["data"].(map[string]any)["csrf_token"].(string)
	out := c.must(200, "POST", "/auth/login", map[string]string{"username": username, "password": password})
	c.csrf = out["data"].(map[string]any)["csrf_token"].(string)
}

func dataID(out map[string]any) int64 {
	return int64(out["data"].(map[string]any)["id"].(float64))
}

func TestE2E_APIAdminLeaderAndApprovalFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	a := newE2EApp(t)
	var connID int64
	cc := a.target.Config().ConnConfig
	err := a.app.QueryRow(context.Background(), `
		INSERT INTO connections (name, dbms, host, port, database_name, db_username, db_password_enc, ssl_mode)
		VALUES ('sampledb', 'postgres', $1, $2, $3, $4, '\x00', 'disable') RETURNING id`,
		cc.Host, cc.Port, cc.Database, cc.User).Scan(&connID)
	if err != nil {
		t.Fatal(err)
	}

	admin := newAPIClient(t, a.base)
	if status, _ := admin.do("GET", "/auth/me", nil); status != 401 {
		t.Fatalf("belum login harus 401, dapat %d", status)
	}
	admin.login("admin", e2eAdminPassword)

	editor := dataID(admin.must(201, "POST", "/admin/users", map[string]any{"username": "editor", "password": "password-editor"}))
	lead := dataID(admin.must(201, "POST", "/admin/users", map[string]any{"username": "lead", "password": "password-lead1"}))
	group := dataID(admin.must(201, "POST", "/admin/groups", map[string]any{"name": "Tim Data"}))

	admin.must(422, "PUT", fmt.Sprintf("/admin/groups/%d/leader", group), map[string]any{"user_id": lead})
	admin.must(204, "POST", fmt.Sprintf("/admin/groups/%d/members", group), map[string]any{"user_id": editor})
	admin.must(204, "POST", fmt.Sprintf("/admin/groups/%d/members", group), map[string]any{"user_id": lead})
	admin.must(204, "PUT", fmt.Sprintf("/admin/groups/%d/leader", group), map[string]any{"user_id": lead})
	admin.must(204, "PUT", fmt.Sprintf("/admin/groups/%d/access/%d", group, connID), map[string]any{"access_level": "view_edit"})

	detail := admin.must(200, "GET", fmt.Sprintf("/admin/groups/%d", group), nil)["data"].(map[string]any)
	if detail["leader_name"] != "lead" || len(detail["members"].([]any)) != 2 {
		t.Fatalf("detail group: %v", detail)
	}
	admin.must(422, "DELETE", fmt.Sprintf("/admin/groups/%d/members/%d", group, lead), nil)
	admin.must(422, "POST", fmt.Sprintf("/admin/users/%d/deactivate", lead), nil)

	user := newAPIClient(t, a.base)
	user.login("editor", "password-editor")
	user.must(403, "GET", "/admin/users", nil)
	inbox := user.must(200, "GET", "/approvals", nil)["data"].([]any)
	if len(inbox) != 0 {
		t.Fatalf("editor bukan leader, inbox harus kosong: %v", inbox)
	}
	user.must(404, "POST", "/executions/999/approvals/1/approve", nil)
	user.must(404, "GET", "/does-not-exist", nil)
}

func TestE2E_MetricsEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	a := newE2EApp(t)
	newAPIClient(t, a.base).do("GET", "/auth/me", nil)

	resp, err := http.Get(a.base + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body bytes.Buffer
	_, _ = body.ReadFrom(resp.Body)
	for _, want := range []string{
		`dbplatform_http_requests_total{method="GET",route="GET /api/v1/auth/me",status="401"}`,
		`dbplatform_connection_locks_active 0`,
		`dbplatform_jobs_running 0`,
	} {
		if !strings.Contains(body.String(), want) {
			t.Errorf("/metrics tidak memuat %s", want)
		}
	}
}
