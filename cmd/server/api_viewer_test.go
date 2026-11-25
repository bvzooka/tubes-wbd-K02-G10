package main

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"dbplatform/internal/testutil"
)

const viewerTargetSQL = `
	CREATE TABLE customers (
		id    bigint PRIMARY KEY,
		name  text NOT NULL DEFAULT 'anon',
		email text UNIQUE,
		age   int CHECK (age >= 0)
	);
	CREATE TABLE orders (
		id          bigint PRIMARY KEY,
		customer_id bigint REFERENCES customers(id) ON DELETE CASCADE,
		total       numeric
	);
	CREATE TABLE "weird/name" (id int PRIMARY KEY, note text);
	CREATE VIEW customer_names AS SELECT id, name FROM customers;
	INSERT INTO customers VALUES (1, 'Ann', 'a@x.id', 30), (2, 'Bob', NULL, NULL), (3, '', 'c@x.id', 5);
	INSERT INTO "weird/name" VALUES (1, 'ok');
	CREATE SCHEMA _dbplatform;
	CREATE TABLE _dbplatform.executions (execution_id bigint PRIMARY KEY);`

// menyiapkan satu database target berisi data, satu user View Only, dan satu user tanpa akses
type viewerFixture struct {
	app      *e2eApp
	admin    *apiClient
	dbID     int64
	reader   *apiClient
	outsider *apiClient
}

func newViewerFixture(t *testing.T) *viewerFixture {
	t.Helper()
	a := newE2EApp(t)
	testutil.MustExec(t, a.target, viewerTargetSQL)

	f := &viewerFixture{app: a, admin: a.adminAPI(t)}
	f.dbID = dataID(f.admin.must(201, "POST", "/admin/connections", a.targetBody("sampel")))

	readerID := dataID(f.admin.must(201, "POST", "/admin/users", map[string]any{"username": "pembaca", "password": "password-pembaca1"}))
	f.admin.must(201, "POST", "/admin/users", map[string]any{"username": "luar", "password": "password-luar123"})
	group := dataID(f.admin.must(201, "POST", "/admin/groups", map[string]any{"name": "Pembaca"}))
	f.admin.must(204, "POST", fmt.Sprintf("/admin/groups/%d/members", group), map[string]any{"user_id": readerID})
	f.admin.must(204, "PUT", fmt.Sprintf("/admin/groups/%d/access/%d", group, f.dbID), map[string]any{"access_level": "view_only"})

	f.reader = newAPIClient(t, a.base)
	f.reader.login("pembaca", "password-pembaca1")
	f.outsider = newAPIClient(t, a.base)
	f.outsider.login("luar", "password-luar123")
	return f
}

func (f *viewerFixture) path(suffix string) string {
	return fmt.Sprintf("/databases/%d%s", f.dbID, suffix)
}

func TestViewerAPI_Integration_RequiresLoginAndAccess(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	f := newViewerFixture(t)
	routes := []string{
		f.path("/schema"),
		f.path("/tables/public/customers"),
		f.path("/tables/public/customers/data"),
	}

	anon := newAPIClient(t, f.app.base)
	anon.must(401, "GET", "/databases", nil)
	for _, p := range routes {
		anon.must(401, "GET", p, nil)
		f.outsider.must(403, "GET", p, nil) // login tapi tanpa akses ke database ini
	}

	// user tanpa akses tidak melihat database itu di daftarnya
	if list := f.outsider.must(200, "GET", "/databases", nil)["data"].([]any); len(list) != 0 {
		t.Fatalf("user tanpa akses melihat %d database", len(list))
	}
}

func TestViewerAPI_Integration_ListsAccessibleDatabases(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	f := newViewerFixture(t)

	list := f.reader.must(200, "GET", "/databases", nil)["data"].([]any)
	if len(list) != 1 {
		t.Fatalf("daftar database = %v", list)
	}
	db := list[0].(map[string]any)
	if db["name"] != "sampel" || db["access_level"] != "view_only" || db["can_edit"] != false || db["dbms"] != "postgres" {
		t.Fatalf("database: %v", db)
	}
}

func TestViewerAPI_Integration_SchemaGroupsTablesAndHidesMeta(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	f := newViewerFixture(t)

	data := f.reader.must(200, "GET", f.path("/schema"), nil)["data"].(map[string]any)
	schemas := data["schemas"].([]any)
	if len(schemas) != 1 || schemas[0].(map[string]any)["name"] != "public" {
		t.Fatalf("hanya schema public yang boleh tampil (bukan _dbplatform): %v", schemas)
	}

	kinds := map[string]string{}
	for _, raw := range schemas[0].(map[string]any)["tables"].([]any) {
		tb := raw.(map[string]any)
		kinds[tb["name"].(string)] = tb["kind"].(string)
		if est, ok := tb["estimated_rows"].(float64); ok && est < 0 {
			t.Errorf("estimated_rows %v: tidak diketahui harus null, bukan negatif", est)
		}
	}
	want := map[string]string{"customers": "table", "orders": "table", "weird/name": "table", "customer_names": "view"}
	for name, kind := range want {
		if kinds[name] != kind {
			t.Errorf("tabel %q kind = %q, mau %q (semua: %v)", name, kinds[name], kind, kinds)
		}
	}
	if len(kinds) != len(want) {
		t.Errorf("jumlah tabel = %d, mau %d: %v", len(kinds), len(want), kinds)
	}
}

func TestViewerAPI_Integration_TableDetail(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	f := newViewerFixture(t)

	cust := f.reader.must(200, "GET", f.path("/tables/public/customers"), nil)["data"].(map[string]any)
	cols := map[string]map[string]any{}
	for _, c := range cust["columns"].([]any) {
		col := c.(map[string]any)
		cols[col["name"].(string)] = col
	}
	if cols["name"]["default"] != "'anon'::text" || cols["name"]["nullable"] != false {
		t.Errorf("kolom name: %v", cols["name"])
	}
	if cols["email"]["default"] != nil || cols["email"]["nullable"] != true {
		t.Errorf("kolom email tanpa default harus null: %v", cols["email"])
	}

	types := map[string]bool{}
	for _, c := range cust["constraints"].([]any) {
		con := c.(map[string]any)
		types[con["type"].(string)] = true
		if con["type"] == "check" && !strings.Contains(con["check_expr"].(string), "age") {
			t.Errorf("check_expr: %v", con)
		}
	}
	for _, want := range []string{"primary_key", "unique", "check"} {
		if !types[want] {
			t.Errorf("constraint %q tidak ada: %v", want, types)
		}
	}
	if len(cust["indexes"].([]any)) < 2 {
		t.Errorf("index customers (primary dan unique): %v", cust["indexes"])
	}

	orders := f.reader.must(200, "GET", f.path("/tables/public/orders"), nil)["data"].(map[string]any)
	var fk map[string]any
	for _, c := range orders["constraints"].([]any) {
		if con := c.(map[string]any); con["type"] == "foreign_key" {
			fk = con
		}
	}
	if fk == nil || fk["ref_table"] != "customers" || fk["on_delete"] != "CASCADE" {
		t.Errorf("foreign key orders: %v", fk)
	}

	f.reader.must(404, "GET", f.path("/tables/public/tidak_ada"), nil)
	// tabel meta platform tidak boleh bisa dibuka walau namanya ditebak
	f.reader.must(404, "GET", f.path("/tables/_dbplatform/executions"), nil)
}

func TestViewerAPI_Integration_DataPagingAndNulls(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	f := newViewerFixture(t)
	table := f.path("/tables/public/customers/data")

	p1 := f.reader.must(200, "GET", table+"?size=2", nil)["data"].(map[string]any)
	if p1["page"] != float64(1) || p1["page_size"] != float64(2) || p1["has_next"] != true || len(p1["rows"].([]any)) != 2 {
		t.Fatalf("halaman 1: %v", p1)
	}
	if cols := p1["columns"].([]any); len(cols) != 4 || cols[0] != "id" {
		t.Fatalf("columns: %v", cols)
	}

	p2 := f.reader.must(200, "GET", table+"?size=2&page=2", nil)["data"].(map[string]any)
	rows := p2["rows"].([]any)
	if p2["has_next"] != false || len(rows) != 1 {
		t.Fatalf("halaman 2: %v", p2)
	}

	// baris Bob: email dan age NULL harus null, bukan string kosong; baris id=3 punya name "" yang BUKAN null
	all := f.reader.must(200, "GET", table, nil)["data"].(map[string]any)["rows"].([]any)
	bob, third := all[1].([]any), all[2].([]any)
	if bob[1] != "Bob" || bob[2] != nil || bob[3] != nil {
		t.Errorf("baris Bob: %v", bob)
	}
	if third[1] != "" {
		t.Errorf("name kosong harus string kosong, bukan null: %v", third)
	}

	// ukuran halaman dibatasi, nilai rusak jatuh ke default
	if got := f.reader.must(200, "GET", table+"?size=99999", nil)["data"].(map[string]any)["page_size"]; got != float64(200) {
		t.Errorf("size berlebih harus dibatasi 200, dapat %v", got)
	}
	if got := f.reader.must(200, "GET", table+"?size=abc&page=-4", nil)["data"].(map[string]any); got["page"] != float64(1) || got["page_size"] != float64(50) {
		t.Errorf("nilai rusak harus ke default: %v", got)
	}
}

func TestViewerAPI_Integration_UnusualAndHostileNames(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	f := newViewerFixture(t)

	// nama berkarakter khusus di path harus tetap bisa dibuka
	weird := f.reader.must(200, "GET", f.path("/tables/public/"+url.PathEscape("weird/name")+"/data"), nil)["data"].(map[string]any)
	if rows := weird["rows"].([]any); len(rows) != 1 || rows[0].([]any)[1] != "ok" {
		t.Fatalf("weird/name: %v", weird)
	}

	// nama yang berisi SQL hanya dianggap nama tabel yang tidak ada, dan tabel asli tetap utuh
	hostile := url.PathEscape(`customers"; DROP TABLE customers; --`)
	f.reader.must(404, "GET", f.path("/tables/public/"+hostile+"/data"), nil)
	f.reader.must(404, "GET", f.path("/tables/public/"+hostile), nil)
	if got := f.reader.must(200, "GET", f.path("/tables/public/customers/data"), nil)["data"].(map[string]any)["rows"].([]any); len(got) != 3 {
		t.Fatalf("customers berubah setelah request berbahaya: %v", got)
	}

	f.reader.must(404, "GET", "/databases/abc/schema", nil)
	f.reader.must(404, "GET", "/databases/0/schema", nil)
}

func TestViewerAPI_Integration_ViewerCannotWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	f := newViewerFixture(t)
	// API viewer hanya membaca: method tulis ke route yang sama tidak ada
	for _, m := range []string{"POST", "PUT", "DELETE"} {
		status, _ := f.reader.do(m, f.path("/tables/public/customers/data"), map[string]any{})
		if status != 404 && status != 405 {
			t.Errorf("%s ke route data: status %d, mau 404/405", m, status)
		}
	}
}

func TestViewerAPI_Integration_InactiveConnectionIsHidden(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	f := newViewerFixture(t)
	f.admin.must(200, "POST", fmt.Sprintf("/admin/connections/%d/deactivate", f.dbID), nil)

	f.reader.must(403, "GET", f.path("/schema"), nil)
	if list := f.reader.must(200, "GET", "/databases", nil)["data"].([]any); len(list) != 0 {
		t.Fatalf("koneksi nonaktif masih tampil: %v", list)
	}

	f.admin.must(200, "POST", fmt.Sprintf("/admin/connections/%d/activate", f.dbID), nil)
	f.reader.must(200, "GET", f.path("/schema"), nil)
}
