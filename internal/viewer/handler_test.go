package viewer_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"dbplatform/internal/dbmgmttest"
	"dbplatform/internal/domain"
)

func path(a *dbmgmttest.App, suffix string) string {
	return fmt.Sprintf("/databases/%d%s", a.ConnID, suffix)
}

func wantStatus(t *testing.T, resp *http.Response, want int, what string) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("%s: status %d, mau %d", what, resp.StatusCode, want)
	}
}

func TestViewer_Integration_UnauthenticatedRedirectsToLogin(t *testing.T) {
	a := dbmgmttest.New(t)
	c := a.Client()
	for _, p := range []string{"/databases", path(a, ""), path(a, "/tables/public/customers"), path(a, "/tables/public/customers/data")} {
		resp, _ := a.Get(t, c, p)
		wantStatus(t, resp, http.StatusSeeOther, p)
		if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/login") {
			t.Errorf("%s: redirect ke %q, mau /login", p, loc)
		}
	}
}

func TestViewer_Integration_NoAccessIs403(t *testing.T) {
	a := dbmgmttest.New(t)
	a.SeedUser(t, "tanpagroup", false)
	c := a.Client()
	a.Login(t, c, "tanpagroup")

	resp, body := a.Get(t, c, "/databases")
	wantStatus(t, resp, http.StatusOK, "daftar database")
	if strings.Contains(body, "Sampel") {
		t.Error("database tanpa akses tidak boleh muncul di daftar")
	}
	if !strings.Contains(body, "belum memiliki akses") {
		t.Error("harus ada pesan belum punya akses")
	}
	for _, p := range []string{path(a, ""), path(a, "/tables/public/customers"), path(a, "/tables/public/customers/data"), "/databases/99999"} {
		resp, body := a.Get(t, c, p)
		wantStatus(t, resp, http.StatusForbidden, p)
		if strings.Contains(body, "Alice Wijaya") || strings.Contains(body, "customers_pkey") {
			t.Errorf("%s: data bocor pada respons 403", p)
		}
	}
}

func TestViewer_Integration_PlatformAdminIsNotDatabaseAccess(t *testing.T) {
	a := dbmgmttest.New(t)
	a.SeedUser(t, "root", true)
	c := a.Client()
	a.Login(t, c, "root")
	resp, _ := a.Get(t, c, path(a, ""))
	wantStatus(t, resp, http.StatusForbidden, "admin tanpa group")
}

func TestViewer_Integration_ViewOnlyCanBrowse(t *testing.T) {
	a := dbmgmttest.New(t)
	u := a.SeedUser(t, "pembaca", false)
	a.GrantTo(t, "readers", u, domain.AccessViewOnly)
	c := a.Client()
	a.Login(t, c, "pembaca")

	resp, body := a.Get(t, c, "/databases")
	wantStatus(t, resp, http.StatusOK, "daftar")
	for _, want := range []string{"Sampel", "View Only", fmt.Sprintf(`href="/databases/%d"`, a.ConnID), "/migrations"} {
		if !strings.Contains(body, want) {
			t.Errorf("daftar database tidak memuat %q", want)
		}
	}

	resp, body = a.Get(t, c, path(a, ""))
	wantStatus(t, resp, http.StatusOK, "ringkasan")
	for _, want := range []string{"Schema public", "Schema hr", "customers", "order_items", "active_customers", "order_totals", "employees", "View", "Mixed Case"} {
		if !strings.Contains(body, want) {
			t.Errorf("ringkasan tidak memuat %q", want)
		}
	}
	if strings.Contains(body, "pg_catalog") || strings.Contains(body, "information_schema") {
		t.Error("schema sistem tidak boleh tampil")
	}
	if !strings.Contains(body, "250") {
		t.Error("estimasi baris event_log (250) harus tampil")
	}
	if !strings.Contains(body, "tidak diketahui") {
		t.Error("view biasa harus menampilkan 'tidak diketahui'")
	}
}

func TestViewer_Integration_TableDetail(t *testing.T) {
	a := dbmgmttest.New(t)
	u := a.SeedUser(t, "pembaca", false)
	a.GrantTo(t, "readers", u, domain.AccessViewOnly)
	c := a.Client()
	a.Login(t, c, "pembaca")

	resp, body := a.Get(t, c, path(a, "/tables/public/orders"))
	wantStatus(t, resp, http.StatusOK, "detail tabel")
	for _, want := range []string{
		"public.orders", "customer_id", "bigint", "timestamp without time zone",
		"orders_pkey", "orders_customer_fk", "Foreign key", "public.customers", "ON DELETE RESTRICT",
		"orders_status_check", "CHECK", "orders_customer_idx", "btree",
		path(a, "/tables/public/orders/data"),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail tabel tidak memuat %q", want)
		}
	}

	resp, body = a.Get(t, c, path(a, "/tables/hr/employees"))
	wantStatus(t, resp, http.StatusOK, "tabel schema hr")
	if !strings.Contains(body, "hr.employees") || !strings.Contains(body, "employees_manager_id_fkey") {
		t.Error("detail hr.employees tidak lengkap")
	}

	resp, _ = a.Get(t, c, path(a, "/tables/public/tidak_ada"))
	wantStatus(t, resp, http.StatusNotFound, "tabel tidak ada")
	resp, _ = a.Get(t, c, path(a, "/tables/pg_catalog/pg_class"))
	wantStatus(t, resp, http.StatusNotFound, "tabel sistem")
}

func TestViewer_Integration_TableData(t *testing.T) {
	a := dbmgmttest.New(t)
	u := a.SeedUser(t, "pembaca", false)
	a.GrantTo(t, "readers", u, domain.AccessViewOnly)
	c := a.Client()
	a.Login(t, c, "pembaca")

	resp, body := a.Get(t, c, path(a, "/tables/public/customers/data"))
	wantStatus(t, resp, http.StatusOK, "data")
	for _, want := range []string{"Alice Wijaya", "alice@example.com", `<span class="null">NULL</span>`, "full_name"} {
		if !strings.Contains(body, want) {
			t.Errorf("data tidak memuat %q", want)
		}
	}
	// Nilai dari database harus di-escape, bukan dirender sebagai HTML.
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatal("XSS: nilai sel dirender sebagai HTML mentah")
	}
	if !strings.Contains(body, "Dedi &lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Error("nilai sel harus ter-escape")
	}
}

func TestViewer_Integration_Pagination(t *testing.T) {
	a := dbmgmttest.New(t)
	u := a.SeedUser(t, "pembaca", false)
	a.GrantTo(t, "readers", u, domain.AccessViewOnly)
	c := a.Client()
	a.Login(t, c, "pembaca")
	base := path(a, "/tables/public/event_log/data")

	resp, body := a.Get(t, c, base)
	wantStatus(t, resp, http.StatusOK, "halaman 1")
	if !strings.Contains(body, "event 1<") || strings.Contains(body, "event 51<") {
		t.Error("halaman 1 harus berisi event 1..50")
	}
	if !strings.Contains(body, "page=2") || !strings.Contains(body, "Halaman 1") {
		t.Error("harus ada tautan halaman berikutnya")
	}
	if strings.Contains(body, "page=0") {
		t.Error("tidak boleh ada tautan sebelumnya di halaman 1")
	}

	resp, body = a.Get(t, c, base+"?page=2&size=100")
	wantStatus(t, resp, http.StatusOK, "halaman 2")
	if !strings.Contains(body, "event 101<") || strings.Contains(body, "event 100<") || !strings.Contains(body, "page=3") || !strings.Contains(body, "page=1") {
		t.Error("halaman 2 (size 100) salah")
	}

	resp, body = a.Get(t, c, base+"?page=3&size=100")
	wantStatus(t, resp, http.StatusOK, "halaman terakhir")
	if !strings.Contains(body, "event 250<") || strings.Contains(body, "page=4") {
		t.Error("halaman terakhir tidak boleh punya tautan berikutnya")
	}

	for _, q := range []string{"?page=abc&size=xyz", "?page=-5&size=-1", "?size=999999", "?page=99999"} {
		resp, _ := a.Get(t, c, base+q)
		wantStatus(t, resp, http.StatusOK, "query tidak lazim "+q)
	}
	_, body = a.Get(t, c, base+"?size=999999")
	if strings.Contains(body, "event 201<") {
		t.Error("size harus dibatasi 200")
	}
}

func TestViewer_Integration_QuotedTableNames(t *testing.T) {
	a := dbmgmttest.New(t)
	u := a.SeedUser(t, "pembaca", false)
	a.GrantTo(t, "readers", u, domain.AccessViewOnly)
	c := a.Client()
	a.Login(t, c, "pembaca")

	_, body := a.Get(t, c, path(a, ""))
	if !strings.Contains(body, "/tables/public/Mixed%20Case") {
		t.Fatalf("tautan tabel berspasi harus di-escape")
	}
	resp, body := a.Get(t, c, path(a, "/tables/public/Mixed%20Case/data"))
	wantStatus(t, resp, http.StatusOK, "tabel berspasi")
	if !strings.Contains(body, "Some Value") || !strings.Contains(body, "satu") {
		t.Error("data tabel berspasi tidak tampil")
	}
	// Payload injection hanya menghasilkan 404, tabel tetap ada.
	for _, p := range []string{
		path(a, "/tables/public/customers%3B%20DROP%20TABLE%20customers/data"),
		path(a, "/tables/public/%22%3B%20DROP%20TABLE%20customers%3B--/data"),
	} {
		resp, _ := a.Get(t, c, p)
		wantStatus(t, resp, http.StatusNotFound, p)
	}
	var n int
	if err := a.Target.QueryRow(t.Context(), "SELECT count(*) FROM customers").Scan(&n); err != nil || n != 4 {
		t.Fatalf("customers berubah: n=%d err=%v", n, err)
	}
}

func TestViewer_Integration_ReadOnlyRoutes(t *testing.T) {
	a := dbmgmttest.New(t)
	u := a.SeedUser(t, "editor", false)
	a.GrantTo(t, "editors", u, domain.AccessViewEdit)
	c := a.Client()
	a.Login(t, c, "editor")
	// Viewer tidak punya aksi tulis: POST ke route viewer tidak diizinkan.
	resp, _ := a.Post(t, c, path(a, "/tables/public/customers/data"), nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST ke route viewer: status %d, mau 405", resp.StatusCode)
	}
}

func TestViewer_Integration_InactiveConnectionHidden(t *testing.T) {
	a := dbmgmttest.New(t)
	u := a.SeedUser(t, "pembaca", false)
	a.GrantTo(t, "readers", u, domain.AccessViewOnly)
	c := a.Client()
	a.Login(t, c, "pembaca")

	if err := a.Repo.SetActive(t.Context(), a.ConnID, false); err != nil {
		t.Fatal(err)
	}
	_, body := a.Get(t, c, "/databases")
	if strings.Contains(body, "Sampel") {
		t.Error("koneksi nonaktif tidak boleh muncul di daftar")
	}
	resp, _ := a.Get(t, c, path(a, ""))
	wantStatus(t, resp, http.StatusForbidden, "koneksi nonaktif")

	_ = a.Repo.SetActive(t.Context(), a.ConnID, true)
	resp, _ = a.Get(t, c, path(a, ""))
	wantStatus(t, resp, http.StatusOK, "diaktifkan lagi")
}

func TestViewer_Integration_UnreachableTargetIs502(t *testing.T) {
	a := dbmgmttest.New(t)
	u := a.SeedUser(t, "pembaca", false)
	a.GrantTo(t, "readers", u, domain.AccessViewOnly)
	c := a.Client()
	a.Login(t, c, "pembaca")

	bad := a.TargetInput("Sampel")
	bad.Port = 1
	bad.Password = "RahasiaSekali"
	if _, err := a.Repo.Update(t.Context(), a.ConnID, bad); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{path(a, ""), path(a, "/tables/public/customers"), path(a, "/tables/public/customers/data")} {
		resp, body := a.Get(t, c, p)
		wantStatus(t, resp, http.StatusBadGateway, p)
		if !strings.Contains(body, "tidak dapat dijangkau") {
			t.Errorf("%s: pesan ramah tidak ada", p)
		}
		for _, secret := range []string{"RahasiaSekali", "postgres://", "127.0.0.1"} {
			if strings.Contains(body, secret) {
				t.Errorf("%s: halaman 502 membocorkan %q", p, secret)
			}
		}
		if !strings.Contains(body, `href="/databases"`) {
			t.Errorf("%s: harus ada tautan kembali ke daftar database", p)
		}
	}
}
