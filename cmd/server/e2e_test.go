package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/config"
	"dbplatform/internal/testutil"
)

const (
	e2eAdminPassword  = "admin-password-123"
	e2eEditorPassword = "penyunting-pass-123"
	e2eReaderPassword = "pembaca-pass-123"
	e2eOutsiderPass   = "orang-luar-pass-123"
	e2eConnName       = "e2e-sampel-xyz"
)

var (
	// Ambil token CSRF dari HTML pakai regex
	csrfPattern     = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)
	trailingIDMatch = regexp.MustCompile(`/(\d+)$`)
)

type e2eResponse struct {
	Code     int
	Location string
	Body     string
}

type e2eClient struct {
	t    *testing.T
	base string
	http *http.Client
	jar  *cookiejar.Jar
}

// Simpan cookie tapi ga ikut redirect biar bisa dicek
func newE2EClient(t *testing.T, base string) *e2eClient {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &e2eClient{
		t: t, base: base, jar: jar,
		http: &http.Client{
			Jar:     jar,
			Timeout: 60 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (c *e2eClient) do(method, path string, form url.Values) e2eResponse {
	c.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		c.t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return e2eResponse{Code: resp.StatusCode, Location: resp.Header.Get("Location"), Body: string(b)}
}

func (c *e2eClient) get(path string) e2eResponse {
	c.t.Helper()
	return c.do(http.MethodGet, path, nil)
}

func (c *e2eClient) post(path string, form url.Values) e2eResponse {
	c.t.Helper()
	return c.do(http.MethodPost, path, form)
}

// Token CSRF diambil dari halaman /databases
func (c *e2eClient) csrf() string {
	c.t.Helper()
	resp := c.get("/databases")
	if resp.Code != http.StatusOK {
		c.t.Fatalf("GET /databases untuk mengambil token CSRF: status %d", resp.Code)
	}
	m := csrfPattern.FindStringSubmatch(resp.Body)
	if m == nil {
		c.t.Fatal("token CSRF tidak ditemukan di halaman")
	}
	return m[1]
}

func (c *e2eClient) login(username, password string) e2eResponse {
	c.t.Helper()
	page := c.get("/login")
	m := csrfPattern.FindStringSubmatch(page.Body)
	if m == nil {
		c.t.Fatal("token CSRF login tidak ditemukan")
	}
	return c.post("/login", url.Values{"csrf_token": {m[1]}, "username": {username}, "password": {password}})
}

// Buat ngecek session lama udah ga berlaku
func (c *e2eClient) sessionCookie() string {
	u, _ := url.Parse(c.base)
	for _, ck := range c.jar.Cookies(u) {
		if ck.Name == "wbd_session" {
			return ck.Value
		}
	}
	return ""
}

func (c *e2eClient) form(extra url.Values) url.Values {
	c.t.Helper()
	v := url.Values{"csrf_token": {c.csrf()}}
	for k, vs := range extra {
		v[k] = vs
	}
	return v
}

type e2eApp struct {
	base   string
	app    *pgxpool.Pool
	target *pgxpool.Pool
}

// Jalanin aplikasi lewat buildApp di atas database sementara
func newE2EApp(t *testing.T) *e2eApp {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// Database kosong buat platform (skema dibikin sama buildApp)
	appPool := testutil.NewTargetDB(t)
	cc := appPool.Config().ConnConfig
	appURL := (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cc.User, cc.Password),
		Host:     fmt.Sprintf("%s:%d", cc.Host, cc.Port),
		Path:     "/" + cc.Database,
		RawQuery: "sslmode=disable",
	}).String()

	cfg := config.Config{
		Env:         config.EnvDevelopment,
		Addr:        ":0",
		DatabaseURL: appURL,
		SecretKey:   make([]byte, 32),
		Session: config.SessionConfig{
			IdleTimeout:      30 * time.Minute,
			AbsoluteTimeout:  12 * time.Hour,
			RotationInterval: 15 * time.Minute,
			GracePeriod:      30 * time.Second,
		},
		SeedAdminUsername: "admin",
		SeedAdminPassword: e2eAdminPassword,
	}
	handler, cleanup, err := buildApp(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("buildApp: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(cleanup)
	t.Cleanup(srv.Close)

	// Database target terpisah dari database platform
	return &e2eApp{base: srv.URL, app: appPool, target: testutil.NewTargetDB(t)}
}

func (a *e2eApp) targetForm() url.Values {
	cc := a.target.Config().ConnConfig
	return url.Values{
		"name": {e2eConnName}, "dbms": {"postgres"}, "host": {cc.Host}, "port": {strconv.Itoa(int(cc.Port))},
		"database_name": {cc.Database}, "db_username": {cc.User}, "password": {cc.Password}, "ssl_mode": {"disable"},
	}
}

func (a *e2eApp) scalar(t *testing.T, pool *pgxpool.Pool, query string, args ...any) string {
	t.Helper()
	var out string
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&out); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return out
}

func (a *e2eApp) id(t *testing.T, table, column, value string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(a.scalar(t, a.app, fmt.Sprintf(`SELECT id::text FROM %s WHERE %s = $1`, table, column), value), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (a *e2eApp) migrationStatus(t *testing.T, connID int64, version int) string {
	t.Helper()
	return a.scalar(t, a.app, `SELECT status FROM db_migrations WHERE connection_id = $1 AND version = $2`, connID, version)
}

func (a *e2eApp) migrationCount(t *testing.T) string {
	t.Helper()
	return a.scalar(t, a.app, `SELECT count(*)::text FROM db_migrations`)
}

func (a *e2eApp) tableExists(t *testing.T, name string) bool {
	t.Helper()
	return a.scalar(t, a.target, `SELECT (to_regclass($1) IS NOT NULL)::text`, name) == "true"
}

func (a *e2eApp) rows(t *testing.T, table string) string {
	t.Helper()
	return a.scalar(t, a.target, `SELECT count(*)::text FROM `+table)
}

func expect(t *testing.T, r e2eResponse, code int, what string) {
	t.Helper()
	if r.Code != code {
		t.Fatalf("%s: status %d, mau %d (location %q)", what, r.Code, code, r.Location)
	}
}

func expectRedirect(t *testing.T, r e2eResponse, prefix, what string) {
	t.Helper()
	if r.Code != http.StatusSeeOther || !strings.HasPrefix(r.Location, prefix) {
		t.Fatalf("%s: status %d location %q, mau 303 ke %q", what, r.Code, r.Location, prefix)
	}
}

// Ambil ID dari akhir URL redirect
func idFromLocation(t *testing.T, r e2eResponse) int64 {
	t.Helper()
	m := trailingIDMatch.FindStringSubmatch(r.Location)
	if m == nil {
		t.Fatalf("location %q tidak berakhir dengan id", r.Location)
	}
	n, _ := strconv.ParseInt(m[1], 10, 64)
	return n
}

// Satu langkah bernama, kalau gagal langsung berhenti biar langkah berikutnya ga jalan di keadaan rusak 
func step(t *testing.T, name string, fn func(t *testing.T)) {
	t.Helper()
	if !t.Run(name, fn) {
		t.FailNow()
	}
}

// Flow lengkapnya: admin siapin data, tiga jenis user bekerja, aturan akses dijalanin
func TestE2E_FullFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e")
	}
	a := newE2EApp(t)
	admin := newE2EClient(t, a.base)
	editor := newE2EClient(t, a.base)
	reader := newE2EClient(t, a.base)
	outsider := newE2EClient(t, a.base)
	var connID, editorGroup int64

	step(t, "admin pertama masuk dan halaman admin bisa dibuka", func(t *testing.T) {
		expectRedirect(t, admin.login("admin", e2eAdminPassword), "/databases", "login admin")
		for _, p := range []string{"/admin/users", "/admin/groups", "/admin/connections"} {
			expect(t, admin.get(p), http.StatusOK, "GET "+p)
		}
	})

	step(t, "admin menyiapkan koneksi, user, group, dan akses lewat halaman admin", func(t *testing.T) {
		expectRedirect(t, admin.post("/admin/connections", admin.form(a.targetForm())), "/admin/connections", "buat koneksi")
		connID = a.id(t, "connections", "name", e2eConnName)

		for user, pass := range map[string]string{"penyunting": e2eEditorPassword, "pembaca": e2eReaderPassword, "orangluar": e2eOutsiderPass} {
			expectRedirect(t, admin.post("/admin/users", admin.form(url.Values{"username": {user}, "password": {pass}})), "/admin/users", "buat user "+user)
		}
		if r := admin.post("/admin/users", admin.form(url.Values{"username": {"penyunting"}, "password": {e2eEditorPassword}})); r.Code != http.StatusUnprocessableEntity {
			t.Errorf("username ganda: status %d, mau 422", r.Code)
		}

		for _, g := range []struct{ name, user, level string }{
			{"Penyunting", "penyunting", "view_edit"},
			{"Pembaca", "pembaca", "view_only"},
		} {
			r := admin.post("/admin/groups", admin.form(url.Values{"name": {g.name}}))
			expectRedirect(t, r, "/admin/groups/", "buat group "+g.name)
			gid := idFromLocation(t, r)
			base := fmt.Sprintf("/admin/groups/%d", gid)
			userID := a.id(t, "users", "username", g.user)
			expectRedirect(t, admin.post(base+"/members", admin.form(url.Values{"user_id": {strconv.FormatInt(userID, 10)}})), base, "tambah anggota")
			expectRedirect(t, admin.post(base+"/access", admin.form(url.Values{"connection_id": {strconv.FormatInt(connID, 10)}, "access_level": {g.level}})), base, "beri akses")
			if g.user == "penyunting" {
				editorGroup = gid
			}
		}
	})

	db := func(suffix string) string { return fmt.Sprintf("/databases/%d%s", connID, suffix) }

	step(t, "user view_edit masuk dan melihat database yang berhak", func(t *testing.T) {
		expectRedirect(t, editor.login("penyunting", e2eEditorPassword), "/databases", "login penyunting")
		list := editor.get("/databases")
		expect(t, list, http.StatusOK, "daftar database")
		if !strings.Contains(list.Body, e2eConnName) {
			t.Error("daftar database harus memuat koneksi yang berhak diakses")
		}
		expect(t, editor.get(db("")), http.StatusOK, "detail database")
		expect(t, editor.get(db("/migrations")), http.StatusOK, "riwayat migration")
		expect(t, editor.get(db("/migrations/new")), http.StatusOK, "editor migration")
		expect(t, editor.get("/admin/users"), http.StatusForbidden, "halaman admin untuk non-admin")
	})

	step(t, "migration dua statement dibuat dan dijalankan dengan sukses", func(t *testing.T) {
		sql := "CREATE TABLE e2e_pelanggan (id int PRIMARY KEY, nama text);\nINSERT INTO e2e_pelanggan VALUES (1, 'ani');"
		r := editor.post(db("/migrations"), editor.form(url.Values{"name": {"buat tabel pelanggan"}, "sql": {sql}}))
		expectRedirect(t, r, db("/migrations/1"), "simpan draft")
		if got := a.migrationStatus(t, connID, 1); got != "draft" {
			t.Fatalf("status = %s, mau draft", got)
		}
		if a.tableExists(t, "e2e_pelanggan") {
			t.Fatal("draft tidak boleh mengubah target")
		}
		detail := editor.get(db("/migrations/1"))
		expect(t, detail, http.StatusOK, "detail draft")
		if !strings.Contains(detail.Body, "e2e_pelanggan") {
			t.Error("detail harus menampilkan SQL")
		}

		expectRedirect(t, editor.post(db("/migrations/1/execute"), editor.form(nil)), db("/migrations/1"), "jalankan")
		if got := a.migrationStatus(t, connID, 1); got != "success" {
			t.Fatalf("status = %s, mau success", got)
		}
		if !a.tableExists(t, "e2e_pelanggan") || a.rows(t, "e2e_pelanggan") != "1" {
			t.Error("target harus berisi tabel e2e_pelanggan dengan 1 baris")
		}
		if got := a.scalar(t, a.app, `SELECT u.username FROM db_migrations m JOIN users u ON u.id = m.executed_by WHERE m.version = 1`); got != "penyunting" {
			t.Errorf("pelaksana = %q", got)
		}
	})

	// Bukti rollback: tabel dari statement pertama harus ikut batal
	step(t, "statement kedua yang gagal membatalkan seluruh perubahan di target", func(t *testing.T) {
		sql := "CREATE TABLE e2e_gagal (id int);\nINSERT INTO e2e_gagal VALUES (1);\nINSERT INTO tidak_ada VALUES (1);"
		r := editor.post(db("/migrations"), editor.form(url.Values{"name": {"migration rusak"}, "sql": {sql}, "action": {"execute"}}))
		expectRedirect(t, r, db("/migrations/2"), "simpan lalu jalankan")
		if got := a.migrationStatus(t, connID, 2); got != "failed" {
			t.Fatalf("status = %s, mau failed", got)
		}
		if msg := a.scalar(t, a.app, `SELECT error_message FROM db_migrations WHERE version = 2`); !strings.Contains(msg, "tidak_ada") {
			t.Errorf("pesan error = %q", msg)
		}
		if a.tableExists(t, "e2e_gagal") {
			t.Fatal("ROLLBACK GAGAL: tabel dari statement pertama masih ada di target")
		}
		if !a.tableExists(t, "e2e_pelanggan") {
			t.Error("tabel hasil migration sebelumnya tidak boleh ikut hilang")
		}
		expectRedirect(t, editor.post(db("/migrations/2/execute"), editor.form(nil)), db("/migrations/2"), "jalankan ulang yang gagal")
		if got := a.migrationStatus(t, connID, 2); got != "failed" {
			t.Errorf("status = %s, harus tetap failed", got)
		}
	})

	step(t, "versi harus berurutan dan draft bisa dibatalkan", func(t *testing.T) {
		for _, m := range []struct{ name, sql string }{{"v3", "CREATE TABLE e2e_t3 (id int)"}, {"v4", "CREATE TABLE e2e_t4 (id int)"}} {
			expectRedirect(t, editor.post(db("/migrations"), editor.form(url.Values{"name": {m.name}, "sql": {m.sql}})), db("/migrations/"), "buat "+m.name)
		}
		expectRedirect(t, editor.post(db("/migrations/4/execute"), editor.form(nil)), db("/migrations/4"), "jalankan v4 lebih dulu")
		if a.migrationStatus(t, connID, 4) != "draft" || a.tableExists(t, "e2e_t4") {
			t.Fatal("v4 tidak boleh jalan selama v3 masih draft")
		}
		expectRedirect(t, editor.post(db("/migrations/3/cancel"), editor.form(nil)), db("/migrations/3"), "batalkan v3")
		if got := a.migrationStatus(t, connID, 3); got != "cancelled" {
			t.Fatalf("v3 = %s, mau cancelled", got)
		}
		expectRedirect(t, editor.post(db("/migrations/4/execute"), editor.form(nil)), db("/migrations/4"), "jalankan v4")
		if a.migrationStatus(t, connID, 4) != "success" || !a.tableExists(t, "e2e_t4") {
			t.Error("v4 harus berhasil setelah v3 dibatalkan")
		}
	})

	step(t, "riwayat memuat semua migration, EXPLAIN dan validasi bekerja", func(t *testing.T) {
		history := editor.get(db("/migrations"))
		expect(t, history, http.StatusOK, "riwayat")
		for _, name := range []string{"buat tabel pelanggan", "migration rusak", "v3", "v4"} {
			if !strings.Contains(history.Body, name) {
				t.Errorf("riwayat tidak memuat %q", name)
			}
		}
		explain := editor.post(db("/explain"), editor.form(url.Values{"sql": {"SELECT * FROM e2e_pelanggan"}}))
		expect(t, explain, http.StatusOK, "EXPLAIN")

		before := a.migrationCount(t)
		bad := editor.post(db("/migrations"), editor.form(url.Values{"name": {"x"}, "sql": {"CREATE TABLE a (id int); COMMIT;"}}))
		expect(t, bad, http.StatusUnprocessableEntity, "SQL berisi COMMIT")
		if a.migrationCount(t) != before {
			t.Error("SQL tidak valid tidak boleh tersimpan")
		}
	})

	step(t, "draft disiapkan untuk uji penolakan view_only", func(t *testing.T) {
		expectRedirect(t, editor.post(db("/migrations"), editor.form(url.Values{"name": {"draft-uji"}, "sql": {"DROP TABLE e2e_pelanggan"}})), db("/migrations/5"), "buat draft uji")
	})

	step(t, "user view_only bisa melihat tetapi semua aksi tulis ditolak", func(t *testing.T) {
		expectRedirect(t, reader.login("pembaca", e2eReaderPassword), "/databases", "login pembaca")
		expect(t, reader.get(db("/migrations")), http.StatusOK, "riwayat")
		expect(t, reader.get(db("/migrations/1")), http.StatusOK, "detail")

		before := a.migrationCount(t)
		token := reader.csrf()
		writes := map[string]e2eResponse{
			"editor baru":        reader.get(db("/migrations/new")),
			"ubah draft":         reader.get(db("/migrations/5/edit")),
			"buat":               reader.post(db("/migrations"), url.Values{"csrf_token": {token}, "name": {"retas"}, "sql": {"DROP TABLE e2e_pelanggan"}}),
			"buat dan jalankan":  reader.post(db("/migrations"), url.Values{"csrf_token": {token}, "name": {"retas"}, "sql": {"DROP TABLE e2e_pelanggan"}, "action": {"execute"}}),
			"simpan perubahan":   reader.post(db("/migrations/5"), url.Values{"csrf_token": {token}, "name": {"x"}, "sql": {"DROP TABLE e2e_pelanggan"}}),
			"jalankan":           reader.post(db("/migrations/5/execute"), url.Values{"csrf_token": {token}}),
			"batalkan":           reader.post(db("/migrations/5/cancel"), url.Values{"csrf_token": {token}}),
			"EXPLAIN":            reader.post(db("/explain"), url.Values{"csrf_token": {token}, "sql": {"SELECT 1"}}),
			"tanpa token CSRF":   reader.post(db("/migrations/5/execute"), url.Values{}),
			"halaman admin":      reader.get("/admin/groups"),
			"POST halaman admin": reader.post("/admin/users", url.Values{"csrf_token": {token}, "username": {"retas"}, "password": {"retas-password-123"}}),
		}
		for name, r := range writes {
			if r.Code != http.StatusForbidden {
				t.Errorf("%s: status %d, mau 403", name, r.Code)
			}
		}
		if a.migrationCount(t) != before || a.migrationStatus(t, connID, 5) != "draft" {
			t.Error("data migration tidak boleh berubah oleh view_only")
		}
		if !a.tableExists(t, "e2e_pelanggan") || a.rows(t, "e2e_pelanggan") != "1" {
			t.Error("database target tidak boleh berubah oleh view_only")
		}
	})

	step(t, "user tanpa group dan pengunjung tanpa login", func(t *testing.T) {
		expectRedirect(t, outsider.login("orangluar", e2eOutsiderPass), "/databases", "login orangluar")
		list := outsider.get("/databases")
		expect(t, list, http.StatusOK, "daftar database")
		if strings.Contains(list.Body, e2eConnName) {
			t.Error("user tanpa akses tidak boleh melihat nama database")
		}
		for _, p := range []string{db(""), db("/migrations"), db("/migrations/1"), db("/migrations/999"), db("/migrations/new")} {
			expect(t, outsider.get(p), http.StatusForbidden, "GET "+p)
		}

		anon := newE2EClient(t, a.base)
		expectRedirect(t, anon.get("/databases"), "/login", "GET tanpa login")
		expectRedirect(t, anon.get(db("/migrations")), "/login", "riwayat tanpa login")
		expectRedirect(t, anon.post(db("/migrations/5/execute"), url.Values{}), "/login", "POST tanpa login")
		if a.migrationStatus(t, connID, 5) != "draft" {
			t.Error("pengunjung tanpa login tidak boleh mengubah migration")
		}
	})

	step(t, "perubahan keanggotaan berlaku seketika tanpa login ulang", func(t *testing.T) {
		userID := a.id(t, "users", "username", "penyunting")
		base := fmt.Sprintf("/admin/groups/%d", editorGroup)
		expectRedirect(t, admin.post(fmt.Sprintf("%s/members/%d/delete", base, userID), admin.form(nil)), base, "keluarkan penyunting")
		expect(t, editor.get(db("/migrations")), http.StatusForbidden, "riwayat setelah dikeluarkan")
		expect(t, editor.get(db("")), http.StatusForbidden, "detail setelah dikeluarkan")

		expectRedirect(t, admin.post(base+"/members", admin.form(url.Values{"user_id": {strconv.FormatInt(userID, 10)}})), base, "masukkan lagi")
		expect(t, editor.get(db("/migrations")), http.StatusOK, "riwayat setelah dimasukkan lagi")
	})

	step(t, "login salah, logout, sesi lama tidak berlaku, lalu login ulang", func(t *testing.T) {
		wrong := newE2EClient(t, a.base)
		if r := wrong.login("penyunting", "password-salah-123"); r.Code != http.StatusUnauthorized {
			t.Errorf("password salah: status %d, mau 401", r.Code)
		}
		if r := wrong.login("tidak-ada", "password-salah-123"); r.Code != http.StatusUnauthorized {
			t.Errorf("user tidak ada: status %d, mau 401", r.Code)
		}
		expectRedirect(t, wrong.get("/databases"), "/login", "tanpa sesi setelah login gagal")

		old := editor.sessionCookie()
		if old == "" {
			t.Fatal("cookie sesi harus ada setelah login")
		}
		expectRedirect(t, editor.post("/logout", editor.form(nil)), "/login", "logout")
		expectRedirect(t, editor.get("/databases"), "/login", "setelah logout")

		replay := newE2EClient(t, a.base)
		req, _ := http.NewRequest(http.MethodGet, a.base+"/databases", nil)
		req.AddCookie(&http.Cookie{Name: "wbd_session", Value: old})
		resp, err := replay.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/login") {
			t.Errorf("sesi lama dipakai ulang: status %d location %q, mau 303 ke /login", resp.StatusCode, resp.Header.Get("Location"))
		}

		expectRedirect(t, editor.login("penyunting", e2eEditorPassword), "/databases", "login ulang")
		expect(t, editor.get(db("/migrations")), http.StatusOK, "riwayat setelah login ulang")
	})
}
