package main

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// approvalFixture: satu database dengan dua group yang punya akses.
// Tim A (view_edit): editor dan leada, leader leada. Tim B (view_only): leadb, leader leadb.
type approvalFixture struct {
	a      *e2eApp
	admin  *apiClient
	db     int64
	groupA int64
	groupB int64
	users  map[string]int64
}

func newApprovalFixture(t *testing.T) *approvalFixture {
	t.Helper()
	if testing.Short() {
		t.Skip("integration")
	}
	a := newE2EApp(t)
	f := &approvalFixture{a: a, admin: a.adminAPI(t), users: map[string]int64{}}
	f.db = dataID(f.admin.must(201, "POST", "/admin/connections", a.targetBody("sampel")))
	for _, name := range []string{"editor", "leada", "leadb"} {
		f.users[name] = dataID(f.admin.must(201, "POST", "/admin/users",
			map[string]any{"username": name, "password": "password-" + name}))
	}
	f.groupA = f.group(t, "Tim A", "view_edit", "leada", "editor", "leada")
	f.groupB = f.group(t, "Tim B", "view_only", "leadb", "leadb")
	return f
}

// group membuat group beserta anggota dan aksesnya; leader kosong berarti tanpa leader
func (f *approvalFixture) group(t *testing.T, name, level, leader string, members ...string) int64 {
	t.Helper()
	id := dataID(f.admin.must(201, "POST", "/admin/groups", map[string]any{"name": name}))
	for _, m := range members {
		f.admin.must(204, "POST", fmt.Sprintf("/admin/groups/%d/members", id), map[string]any{"user_id": f.users[m]})
	}
	if leader != "" {
		f.admin.must(204, "PUT", fmt.Sprintf("/admin/groups/%d/leader", id), map[string]any{"user_id": f.users[leader]})
	}
	f.admin.must(204, "PUT", fmt.Sprintf("/admin/groups/%d/access/%d", id, f.db), map[string]any{"access_level": level})
	return id
}

func (f *approvalFixture) login(t *testing.T, name string) *apiClient {
	t.Helper()
	c := newAPIClient(t, f.a.base)
	c.login(name, "password-"+name)
	return c
}

// frozen membuat migration yang membuat tabel lalu membekukannya, mengembalikan versinya
func (f *approvalFixture) frozen(t *testing.T, c *apiClient, table string) int {
	t.Helper()
	base := fmt.Sprintf("/databases/%d/migrations", f.db)
	mig := c.must(201, "POST", base, map[string]any{
		"name": table, "sql_up": "CREATE TABLE " + table + " (id int)", "sql_down": "DROP TABLE " + table,
	})["data"].(map[string]any)
	version := int(mig["version"].(float64))
	c.must(200, "POST", fmt.Sprintf("%s/%d/freeze", base, version), map[string]any{"revision": 1})
	return version
}

func (f *approvalFixture) requestPath(version int) string {
	return fmt.Sprintf("/databases/%d/migrations/%d/executions", f.db, version)
}

// request mengajukan apply untuk migration baru dan mengembalikan ID eksekusinya
func (f *approvalFixture) request(t *testing.T, c *apiClient, table string) int64 {
	t.Helper()
	return dataID(c.must(201, "POST", f.requestPath(f.frozen(t, c, table)), map[string]any{"kind": "apply"}))
}

func decidePath(exec, group int64, action string) string {
	return fmt.Sprintf("/executions/%d/approvals/%d/%s", exec, group, action)
}

func runPath(exec int64) string {
	return fmt.Sprintf("/executions/%d/run", exec)
}

// detail mengembalikan status eksekusi dan approval-nya per group
func detail(t *testing.T, c *apiClient, exec int64) (string, map[int64]map[string]any) {
	t.Helper()
	data := c.must(200, "GET", fmt.Sprintf("/executions/%d", exec), nil)["data"].(map[string]any)
	approvals := map[int64]map[string]any{}
	for _, item := range data["approvals"].([]any) {
		a := item.(map[string]any)
		approvals[int64(a["group_id"].(float64))] = a
	}
	return data["execution"].(map[string]any)["status"].(string), approvals
}

func inboxHas(t *testing.T, c *apiClient, exec int64) bool {
	t.Helper()
	for _, item := range c.must(200, "GET", "/approvals", nil)["data"].([]any) {
		if int64(item.(map[string]any)["execution_id"].(float64)) == exec {
			return true
		}
	}
	return false
}

func jobIDOf(out map[string]any) string {
	return out["data"].(map[string]any)["job_id"].(string)
}

func errorMessage(out map[string]any) string {
	e, _ := out["error"].(map[string]any)
	msg, _ := e["message"].(string)
	return msg
}

func TestE2E_AuthAPILoginAndLogout(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	a := newE2EApp(t)
	admin := a.adminAPI(t)
	budi := dataID(admin.must(201, "POST", "/admin/users", map[string]any{"username": "budi", "password": "password-budi"}))
	admin.must(200, "POST", fmt.Sprintf("/admin/users/%d/deactivate", budi), nil)

	noToken := newAPIClient(t, a.base)
	noToken.must(200, "GET", "/auth/csrf", nil)
	noToken.must(403, "POST", "/auth/login", map[string]string{"username": "admin", "password": e2eAdminPassword})

	// password salah, user tidak ada, dan user nonaktif tidak boleh bisa dibedakan
	failed := func(username, password string) string {
		t.Helper()
		c := newAPIClient(t, a.base)
		c.csrf = c.must(200, "GET", "/auth/csrf", nil)["data"].(map[string]any)["csrf_token"].(string)
		return errorMessage(c.must(401, "POST", "/auth/login", map[string]string{"username": username, "password": password}))
	}
	wrong := failed("admin", "password-salah")
	if unknown := failed("tidak-ada", "password-salah"); unknown != wrong {
		t.Errorf("pesan user tidak ada %q, password salah %q", unknown, wrong)
	}
	if inactive := failed("budi", "password-budi"); inactive != wrong {
		t.Errorf("pesan user nonaktif %q, password salah %q", inactive, wrong)
	}

	token := admin.csrf
	admin.csrf = ""
	admin.must(403, "POST", "/auth/logout", nil)
	admin.csrf = token
	admin.must(204, "POST", "/auth/logout", nil)
	admin.must(401, "GET", "/auth/me", nil)
}

func TestE2E_ApprovalNeedsEveryGroupLeaderThenWorkerRuns(t *testing.T) {
	f := newApprovalFixture(t)
	editor, leadA, leadB := f.login(t, "editor"), f.login(t, "leada"), f.login(t, "leadb")

	exec := f.request(t, editor, "promo")
	status, approvals := detail(t, editor, exec)
	if status != "pending_review" || len(approvals) != 2 {
		t.Fatalf("status %s, approval %d group, mau pending_review dan 2", status, len(approvals))
	}
	for group, a := range approvals {
		if a["status"] != "pending" {
			t.Errorf("approval group %d: %v, mau pending", group, a["status"])
		}
	}
	if !inboxHas(t, leadA, exec) || !inboxHas(t, leadB, exec) || inboxHas(t, editor, exec) {
		t.Error("inbox harus memuat permintaan untuk kedua leader saja")
	}

	editor.must(403, "POST", decidePath(exec, f.groupA, "approve"), nil)
	f.admin.must(403, "POST", decidePath(exec, f.groupA, "approve"), nil)
	leadB.must(403, "POST", decidePath(exec, f.groupA, "approve"), nil)

	leadA.must(200, "POST", decidePath(exec, f.groupA, "approve"), nil)
	editor.must(409, "POST", runPath(exec), nil)
	leadA.must(409, "POST", decidePath(exec, f.groupA, "approve"), nil)
	leadB.must(200, "POST", decidePath(exec, f.groupB, "approve"), nil)
	if status, _ := detail(t, editor, exec); status != "approved" {
		t.Fatalf("setelah semua leader setuju status %s", status)
	}

	// worker belum jalan, jadi run kedua harus mendapat job yang sama
	first := jobIDOf(editor.must(202, "POST", runPath(exec), nil))
	if second := jobIDOf(editor.must(202, "POST", runPath(exec), nil)); second != first {
		t.Fatalf("run kedua membuat job baru: %s, sebelumnya %s", second, first)
	}

	startWorker(t, f.a)
	if status := waitJob(t, editor, first); status != "success" {
		t.Fatalf("eksekusi berakhir %s", status)
	}
	if !f.a.tableExists(t, "promo") {
		t.Fatal("tabel promo tidak dibuat di target")
	}

	resp, err := http.Get(f.a.base + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body bytes.Buffer
	_, _ = body.ReadFrom(resp.Body)
	for _, want := range []string{
		`dbplatform_jobs_finished_total{outcome="success"}`,
		`dbplatform_lock_wait_seconds_count`,
		`dbplatform_queue_wait_seconds_count`,
		`dbplatform_migration_executions{status="success"} 1`,
	} {
		if !strings.Contains(body.String(), want) {
			t.Errorf("/metrics tidak memuat %s", want)
		}
	}
}

func TestE2E_ApprovalAutoForRequestingLeaderAndReject(t *testing.T) {
	f := newApprovalFixture(t)
	leadA, leadB := f.login(t, "leada"), f.login(t, "leadb")

	exec := f.request(t, leadA, "promo")
	_, approvals := detail(t, leadA, exec)
	if a := approvals[f.groupA]; a["status"] != "approved" || a["auto"] != true {
		t.Errorf("approval Tim A untuk pemohon leader: %v, mau approved otomatis", a)
	}
	if a := approvals[f.groupB]; a["status"] != "pending" {
		t.Errorf("approval Tim B: %v, mau pending", a)
	}

	leadB.must(200, "POST", decidePath(exec, f.groupB, "reject"), map[string]any{"comment": "belum siap"})
	if status, _ := detail(t, leadA, exec); status != "rejected" {
		t.Fatalf("setelah ditolak status %s", status)
	}
	leadA.must(409, "POST", runPath(exec), nil)
	leadB.must(409, "POST", decidePath(exec, f.groupB, "approve"), nil)
}

func TestE2E_ApprovalBlockedByGroupWithoutLeader(t *testing.T) {
	f := newApprovalFixture(t)
	f.group(t, "Tim Tanpa Leader", "view_only", "")
	editor := f.login(t, "editor")

	version := f.frozen(t, editor, "promo")
	out := editor.must(422, "POST", f.requestPath(version), map[string]any{"kind": "apply"})
	if msg := errorMessage(out); !strings.Contains(msg, "Tim Tanpa Leader") {
		t.Errorf("pesan harus menyebut group tanpa leader: %q", msg)
	}
}

func TestE2E_ViewOnlyCannotWriteMigrations(t *testing.T) {
	f := newApprovalFixture(t)
	editor, leadB := f.login(t, "editor"), f.login(t, "leadb")
	base := fmt.Sprintf("/databases/%d/migrations", f.db)

	leadB.must(403, "POST", base, map[string]any{"name": "x", "sql_up": "SELECT 1", "sql_down": "SELECT 1"})
	version := f.frozen(t, editor, "promo")
	leadB.must(403, "POST", f.requestPath(version), map[string]any{"kind": "apply"})
	exec := dataID(editor.must(201, "POST", f.requestPath(version), map[string]any{"kind": "apply"}))
	leadB.must(403, "POST", runPath(exec), nil)
	leadB.must(200, "GET", base, nil)
}
