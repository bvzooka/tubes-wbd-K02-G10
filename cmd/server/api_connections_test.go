package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// membuat isian koneksi yang mengarah ke database target milik test
func (a *e2eApp) targetBody(name string) map[string]any {
	cc := a.target.Config().ConnConfig
	return map[string]any{
		"name": name, "dbms": "postgres", "host": cc.Host, "port": int(cc.Port),
		"database_name": cc.Database, "db_username": cc.User, "password": cc.Password, "ssl_mode": "disable",
	}
}

// membuat klien API yang sudah login sebagai admin
func (a *e2eApp) adminAPI(t *testing.T) *apiClient {
	t.Helper()
	c := newAPIClient(t, a.base)
	c.login("admin", e2eAdminPassword)
	return c
}

func TestConnectionsAPI_Integration_RequiresAdmin(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	a := newE2EApp(t)

	anon := newAPIClient(t, a.base)
	anon.must(401, "GET", "/admin/connections", nil)

	admin := a.adminAPI(t)
	admin.must(201, "POST", "/admin/users", map[string]any{"username": "biasa", "password": "password-biasa1"})
	user := newAPIClient(t, a.base)
	user.login("biasa", "password-biasa1")

	// user biasa ditolak di semua route, termasuk yang menulis
	user.must(403, "GET", "/admin/connections", nil)
	user.must(403, "POST", "/admin/connections", a.targetBody("x"))
	user.must(403, "POST", "/admin/connections/test", a.targetBody("x"))
	user.must(403, "GET", "/admin/connections/1", nil)
	user.must(403, "PUT", "/admin/connections/1", a.targetBody("x"))
	user.must(403, "POST", "/admin/connections/1/deactivate", nil)
}

func TestConnectionsAPI_Integration_CreateListGetNeverExposePassword(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	a := newE2EApp(t)
	admin := a.adminAPI(t)

	// password unik supaya pencarian di respons tidak salah positif (password asli test sama dengan username)
	const secret = "pw-unik-rahasia-987"
	body := a.targetBody("target-satu")
	body["password"] = secret
	created := admin.must(201, "POST", "/admin/connections", body)
	id := dataID(created)
	data := created["data"].(map[string]any)
	if data["name"] != "target-satu" || data["is_active"] != true || data["db_username"] == "" {
		t.Fatalf("respons create: %v", data)
	}

	list := admin.must(200, "GET", "/admin/connections", nil)
	got := admin.must(200, "GET", fmt.Sprintf("/admin/connections/%d", id), nil)
	for name, body := range map[string]map[string]any{"create": created, "list": list, "get": got} {
		raw, _ := json.Marshal(body)
		if strings.Contains(strings.ToLower(string(raw)), "password") || strings.Contains(string(raw), secret) {
			t.Fatalf("respons %s memuat password: %s", name, raw)
		}
	}
	if items := list["data"].([]any); len(items) != 1 {
		t.Fatalf("list berisi %d koneksi, mau 1", len(items))
	}

	admin.must(404, "GET", "/admin/connections/999999", nil)
	admin.must(404, "GET", "/admin/connections/abc", nil)
}

func TestConnectionsAPI_Integration_ValidationReportsFieldErrors(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	a := newE2EApp(t)
	admin := a.adminAPI(t)

	out := admin.must(422, "POST", "/admin/connections", map[string]any{"name": "", "dbms": "oracle", "port": 0})
	fields, _ := out["error"].(map[string]any)["fields"].(map[string]any)
	for _, want := range []string{"name", "dbms", "host", "port", "database_name", "db_username", "password", "ssl_mode"} {
		if _, ok := fields[want]; !ok {
			t.Errorf("fields tidak memuat %q: %v", want, fields)
		}
	}

	// field yang tidak dikenal ditolak, bukan diam-diam diabaikan
	body := a.targetBody("x")
	body["nama_salah"] = 1
	admin.must(422, "POST", "/admin/connections", body)

	admin.must(201, "POST", "/admin/connections", a.targetBody("kembar"))
	dup := admin.must(422, "POST", "/admin/connections", a.targetBody("kembar"))
	if _, ok := dup["error"].(map[string]any)["fields"].(map[string]any)["name"]; !ok {
		t.Fatalf("nama kembar harus melaporkan field name: %v", dup)
	}
}

func TestConnectionsAPI_Integration_UpdateKeepsPasswordWhenEmpty(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	a := newE2EApp(t)
	admin := a.adminAPI(t)
	id := dataID(admin.must(201, "POST", "/admin/connections", a.targetBody("lama")))
	path := fmt.Sprintf("/admin/connections/%d", id)

	// password dikosongkan: nama berubah, password tersimpan tetap dipakai
	body := a.targetBody("baru")
	body["password"] = ""
	upd := admin.must(200, "PUT", path, body)
	if upd["data"].(map[string]any)["name"] != "baru" {
		t.Fatalf("nama belum berubah: %v", upd)
	}
	res := admin.must(200, "POST", "/admin/connections/test", withID(body, id))["data"].(map[string]any)
	if res["ok"] != true {
		t.Fatalf("uji dengan password tersimpan harus berhasil: %v", res)
	}

	// password diganti salah: uji dengan password tersimpan sekarang harus gagal
	body["password"] = "password-salah-123"
	admin.must(200, "PUT", path, body)
	body["password"] = ""
	res = admin.must(200, "POST", "/admin/connections/test", withID(body, id))["data"].(map[string]any)
	if res["ok"] != false {
		t.Fatalf("password sudah diganti salah, uji harus gagal: %v", res)
	}

	admin.must(404, "PUT", "/admin/connections/999999", a.targetBody("x"))
}

func TestConnectionsAPI_Integration_TestEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	a := newE2EApp(t)
	admin := a.adminAPI(t)

	res := admin.must(200, "POST", "/admin/connections/test", a.targetBody("uji"))["data"].(map[string]any)
	if res["ok"] != true || res["message"] != "Koneksi berhasil." {
		t.Fatalf("uji koneksi benar: %v", res)
	}

	// gagal terhubung adalah hasil uji (200), pesannya tidak boleh membocorkan password
	bad := a.targetBody("uji")
	bad["password"] = "password-rahasia-salah"
	res = admin.must(200, "POST", "/admin/connections/test", bad)["data"].(map[string]any)
	if res["ok"] != false || strings.Contains(fmt.Sprint(res["message"]), "password-rahasia-salah") {
		t.Fatalf("uji koneksi password salah: %v", res)
	}

	// isian tidak valid tetap 422, id negatif atau tidak ada tetap 404
	admin.must(422, "POST", "/admin/connections/test", map[string]any{"name": ""})
	admin.must(404, "POST", "/admin/connections/test", withID(a.targetBody("x"), -1))
	noPassword := a.targetBody("x")
	noPassword["password"] = ""
	admin.must(404, "POST", "/admin/connections/test", withID(noPassword, 999999))
}

func TestConnectionsAPI_Integration_ActivateAndDeactivate(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	a := newE2EApp(t)
	admin := a.adminAPI(t)
	id := dataID(admin.must(201, "POST", "/admin/connections", a.targetBody("aktif")))

	off := admin.must(200, "POST", fmt.Sprintf("/admin/connections/%d/deactivate", id), nil)
	if off["data"].(map[string]any)["is_active"] != false {
		t.Fatalf("setelah deactivate: %v", off)
	}
	on := admin.must(200, "POST", fmt.Sprintf("/admin/connections/%d/activate", id), nil)
	if on["data"].(map[string]any)["is_active"] != true {
		t.Fatalf("setelah activate: %v", on)
	}
	admin.must(404, "POST", "/admin/connections/999999/deactivate", nil)
}

// withID menambahkan id koneksi ke isian uji, tanpa mengubah map aslinya
func withID(body map[string]any, id int64) map[string]any {
	out := make(map[string]any, len(body)+1)
	for k, v := range body {
		out[k] = v
	}
	out["id"] = id
	return out
}
