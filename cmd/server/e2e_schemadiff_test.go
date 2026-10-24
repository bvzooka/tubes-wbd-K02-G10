package main

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/testutil"
)

const (
	diffTargetName = "e2e-diff-tujuan"
	diffSourceName = "e2e-diff-sumber"

	diffOldSQL = `
CREATE TABLE people (id int PRIMARY KEY, name varchar(10) NOT NULL, legacy text);
CREATE TABLE obsolete (id int PRIMARY KEY);
INSERT INTO people VALUES (1, 'ani', 'x');
`
	diffNewSQL = `
CREATE TABLE people (id int PRIMARY KEY, name varchar(50) NOT NULL, email text);
CREATE TABLE pets (id int PRIMARY KEY, owner_id int REFERENCES people (id), name text);
CREATE INDEX pets_owner_idx ON pets (owner_id);
`
)

var changeBoxPattern = regexp.MustCompile(`name="change" id="c-[0-9a-f]{12}" value="([0-9a-f]{12})"( checked)?>`)

func poolConnForm(name string, pool *pgxpool.Pool) url.Values {
	cc := pool.Config().ConnConfig
	return url.Values{
		"name": {name}, "dbms": {"postgres"}, "host": {cc.Host}, "port": {strconv.Itoa(int(cc.Port))},
		"database_name": {cc.Database}, "db_username": {cc.User}, "password": {cc.Password}, "ssl_mode": {"disable"},
	}
}

func listChanges(body string) (checked, unchecked []string) {
	for _, m := range changeBoxPattern.FindAllStringSubmatch(body, -1) {
		if m[2] != "" {
			checked = append(checked, m[1])
		} else {
			unchecked = append(unchecked, m[1])
		}
	}
	return checked, unchecked
}

func TestE2E_SchemaDiffFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e")
	}
	a := newE2EApp(t)
	testutil.MustExec(t, a.target, diffOldSQL)
	source := testutil.NewTargetDB(t, diffNewSQL)

	admin := newE2EClient(t, a.base)
	editor := newE2EClient(t, a.base)
	reader := newE2EClient(t, a.base)
	noSource := newE2EClient(t, a.base)
	var targetID, sourceID int64

	step(t, "admin menyiapkan dua koneksi dan hak akses", func(t *testing.T) {
		expectRedirect(t, admin.login("admin", e2eAdminPassword), "/databases", "login admin")
		expectRedirect(t, admin.post("/admin/connections", admin.form(poolConnForm(diffTargetName, a.target))), "/admin/connections", "koneksi tujuan")
		expectRedirect(t, admin.post("/admin/connections", admin.form(poolConnForm(diffSourceName, source))), "/admin/connections", "koneksi sumber")
		targetID = a.id(t, "connections", "name", diffTargetName)
		sourceID = a.id(t, "connections", "name", diffSourceName)

		users := map[string]string{"penyunting": e2eEditorPassword, "pembaca": e2eReaderPassword, "tanpasumber": e2eOutsiderPass}
		for name, pass := range users {
			expectRedirect(t, admin.post("/admin/users", admin.form(url.Values{"username": {name}, "password": {pass}})), "/admin/users", "user "+name)
		}

		grant := func(group, user string, grants map[int64]string) {
			r := admin.post("/admin/groups", admin.form(url.Values{"name": {group}}))
			expectRedirect(t, r, "/admin/groups/", "group "+group)
			base := fmt.Sprintf("/admin/groups/%d", idFromLocation(t, r))
			expectRedirect(t, admin.post(base+"/members", admin.form(url.Values{"user_id": {strconv.FormatInt(a.id(t, "users", "username", user), 10)}})), base, "anggota "+user)
			for conn, level := range grants {
				expectRedirect(t, admin.post(base+"/access", admin.form(url.Values{"connection_id": {strconv.FormatInt(conn, 10)}, "access_level": {level}})), base, "akses "+group)
			}
		}
		grant("Penyunting diff", "penyunting", map[int64]string{targetID: "view_edit", sourceID: "view_only"})
		grant("Pembaca diff", "pembaca", map[int64]string{targetID: "view_only", sourceID: "view_only"})
		grant("Tanpa sumber", "tanpasumber", map[int64]string{targetID: "view_edit"})
	})

	diff := func(suffix string) string { return fmt.Sprintf("/databases/%d%s", targetID, suffix) }

	step(t, "tautan schema diff muncul untuk yang berhak dan halaman pilih sumber bisa dibuka", func(t *testing.T) {
		expectRedirect(t, editor.login("penyunting", e2eEditorPassword), "/databases", "login")
		list := editor.get(diff("/migrations"))
		expect(t, list, http.StatusOK, "riwayat")
		if !strings.Contains(list.Body, diff("/schema-diff")) {
			t.Error("tautan 'Buat dari schema diff' harus ada untuk user view_edit")
		}
		pick := editor.get(diff("/schema-diff"))
		expect(t, pick, http.StatusOK, "pilih sumber")
		if !strings.Contains(pick.Body, diffSourceName) {
			t.Error("database sumber harus ditawarkan")
		}
		if strings.Contains(pick.Body, fmt.Sprintf(`<option value="%d"`, targetID)) {
			t.Error("database tujuan tidak boleh ditawarkan sebagai sumber")
		}
	})

	step(t, "pratinjau: perubahan non-destruktif tercentang, yang destruktif tidak", func(t *testing.T) {
		r := editor.post(diff("/schema-diff"), editor.form(url.Values{"source_id": {strconv.FormatInt(sourceID, 10)}, "action": {"preview"}}))
		expect(t, r, http.StatusOK, "pratinjau")
		checked, unchecked := listChanges(r.Body)
		if len(checked) != 6 || len(unchecked) != 2 {
			t.Fatalf("tercentang = %d, tidak = %d; mau 6 dan 2 (create pets, add email, ubah tipe name, PK dan FK pets, index; lalu drop kolom legacy dan drop tabel obsolete)", len(checked), len(unchecked))
		}
		if !strings.Contains(r.Body, "dapat menghapus data") {
			t.Error("peringatan perubahan destruktif harus tampil")
		}
		if a.tableExists(t, "pets") {
			t.Fatal("pratinjau tidak boleh mengubah target")
		}
	})

	step(t, "draft dibuat dari pilihan, diarahkan ke editor, dan target belum berubah", func(t *testing.T) {
		page := editor.post(diff("/schema-diff"), editor.form(url.Values{"source_id": {strconv.FormatInt(sourceID, 10)}, "action": {"preview"}}))
		checked, _ := listChanges(page.Body)

		form := editor.form(url.Values{
			"source_id": {strconv.FormatInt(sourceID, 10)}, "action": {"create"}, "change": checked,
			"sql": {"DROP TABLE people; --"},
		})
		r := editor.post(diff("/schema-diff"), form)
		expectRedirect(t, r, diff("/migrations/1/edit"), "buat draft")

		if got := a.migrationStatus(t, connIDOf(t, a, diffTargetName), 1); got != "draft" {
			t.Fatalf("status = %s, mau draft", got)
		}
		sql := a.scalar(t, a.app, `SELECT sql_text FROM db_migrations WHERE version = 1`)
		if strings.Contains(sql, "DROP TABLE people") || strings.Contains(sql, "DROP COLUMN") || strings.Contains(sql, "obsolete") {
			t.Errorf("SQL kiriman browser tidak boleh dipakai, dan yang destruktif tidak dipilih:\n%s", sql)
		}
		if !strings.Contains(sql, `CREATE TABLE "public"."pets"`) || !strings.Contains(sql, `ALTER COLUMN "name" TYPE character varying(50)`) {
			t.Errorf("SQL draft tidak lengkap:\n%s", sql)
		}
		if a.tableExists(t, "pets") {
			t.Error("draft tidak boleh langsung dijalankan")
		}
		expect(t, editor.get(diff("/migrations/1/edit")), http.StatusOK, "editor draft")
	})

	step(t, "draft dijalankan lewat migration biasa dan target berubah tanpa kehilangan data", func(t *testing.T) {
		expectRedirect(t, editor.post(diff("/migrations/1/execute"), editor.form(nil)), diff("/migrations/1"), "jalankan")
		if got := a.migrationStatus(t, targetID, 1); got != "success" {
			t.Fatalf("status = %s, mau success", got)
		}
		if !a.tableExists(t, "pets") || a.rows(t, "people") != "1" {
			t.Error("tabel pets harus ada dan data people (1 baris) tetap utuh")
		}
		if !a.tableExists(t, "obsolete") {
			t.Error("tabel obsolete tidak dipilih, harus tetap ada")
		}
	})

	step(t, "sisa perubahan destruktif dipilih sadar, dijalankan, lalu tidak ada perbedaan lagi", func(t *testing.T) {
		page := editor.post(diff("/schema-diff"), editor.form(url.Values{"source_id": {strconv.FormatInt(sourceID, 10)}, "action": {"preview"}}))
		checked, unchecked := listChanges(page.Body)
		if len(checked) != 0 || len(unchecked) != 2 {
			t.Fatalf("sisa = %d tercentang dan %d tidak; mau 0 dan 2 (hanya yang destruktif)", len(checked), len(unchecked))
		}
		r := editor.post(diff("/schema-diff"), editor.form(url.Values{"source_id": {strconv.FormatInt(sourceID, 10)}, "action": {"create"}, "change": unchecked}))
		expectRedirect(t, r, diff("/migrations/2/edit"), "draft destruktif")
		expectRedirect(t, editor.post(diff("/migrations/2/execute"), editor.form(nil)), diff("/migrations/2"), "jalankan")
		if a.tableExists(t, "obsolete") {
			t.Error("tabel obsolete harus terhapus")
		}

		done := editor.post(diff("/schema-diff"), editor.form(url.Values{"source_id": {strconv.FormatInt(sourceID, 10)}, "action": {"preview"}}))
		expect(t, done, http.StatusOK, "pratinjau akhir")
		if !strings.Contains(done.Body, "Tidak ada perbedaan") {
			t.Error("setelah semuanya diterapkan, tidak boleh ada perbedaan")
		}
	})

	step(t, "pilihan kosong, ID palsu, dan sumber sama dengan tujuan ditolak", func(t *testing.T) {
		before := a.migrationCount(t)
		src := strconv.FormatInt(sourceID, 10)
		expect(t, editor.post(diff("/schema-diff"), editor.form(url.Values{"source_id": {src}, "action": {"create"}})), http.StatusUnprocessableEntity, "tanpa pilihan")
		expect(t, editor.post(diff("/schema-diff"), editor.form(url.Values{"source_id": {src}, "action": {"create"}, "change": {"000000000000"}})), http.StatusUnprocessableEntity, "ID palsu")
		expect(t, editor.post(diff("/schema-diff"), editor.form(url.Values{"source_id": {strconv.FormatInt(targetID, 10)}, "action": {"preview"}})), http.StatusUnprocessableEntity, "sumber sama dengan tujuan")
		expect(t, editor.post(diff("/schema-diff"), editor.form(url.Values{"source_id": {"abc"}, "action": {"preview"}})), http.StatusUnprocessableEntity, "source_id tidak valid")
		if a.migrationCount(t) != before {
			t.Error("permintaan yang ditolak tidak boleh membuat draft")
		}
	})

	step(t, "user view_only di tujuan, user tanpa akses ke sumber, dan pengunjung ditolak", func(t *testing.T) {
		src := strconv.FormatInt(sourceID, 10)
		before := a.migrationCount(t)

		expectRedirect(t, reader.login("pembaca", e2eReaderPassword), "/databases", "login pembaca")
		token := reader.csrf()
		expect(t, reader.get(diff("/schema-diff")), http.StatusForbidden, "view_only membuka halaman")
		expect(t, reader.post(diff("/schema-diff"), url.Values{"csrf_token": {token}, "source_id": {src}, "action": {"preview"}}), http.StatusForbidden, "view_only pratinjau")
		expect(t, reader.post(diff("/schema-diff"), url.Values{"csrf_token": {token}, "source_id": {src}, "action": {"create"}, "change": {"aaaaaaaaaaaa"}}), http.StatusForbidden, "view_only buat draft")
		if strings.Contains(reader.get(diff("/migrations")).Body, "/schema-diff") {
			t.Error("view_only tidak boleh melihat tautan schema diff")
		}

		expectRedirect(t, noSource.login("tanpasumber", e2eOutsiderPass), "/databases", "login tanpasumber")
		pick := noSource.get(diff("/schema-diff"))
		expect(t, pick, http.StatusOK, "tanpa sumber membuka halaman")
		if strings.Contains(pick.Body, diffSourceName) {
			t.Error("database yang tidak boleh dibaca tidak boleh ditawarkan")
		}
		expect(t, noSource.post(diff("/schema-diff"), noSource.form(url.Values{"source_id": {src}, "action": {"preview"}})), http.StatusForbidden, "pratinjau dengan sumber terlarang")

		anon := newE2EClient(t, a.base)
		expectRedirect(t, anon.get(diff("/schema-diff")), "/login", "pengunjung")
		expectRedirect(t, anon.post(diff("/schema-diff"), url.Values{"source_id": {src}}), "/login", "pengunjung POST")

		if a.migrationCount(t) != before {
			t.Error("tidak boleh ada draft baru dari yang tidak berhak")
		}
	})
}

func connIDOf(t *testing.T, a *e2eApp, name string) int64 {
	t.Helper()
	return a.id(t, "connections", "name", name)
}
