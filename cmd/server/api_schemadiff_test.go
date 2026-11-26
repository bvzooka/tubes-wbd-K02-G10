package main

import (
	"fmt"
	"testing"

	"dbplatform/internal/testutil"
)

const (
	// struktur tujuan punya tabel obsolete dan kolom legacy yang tidak ada di sumber
	diffOldSQL = `
CREATE TABLE people (id int PRIMARY KEY, name varchar(10) NOT NULL, legacy text);
CREATE TABLE obsolete (id int PRIMARY KEY);
INSERT INTO people VALUES (1, 'ani', 'x');
`
	// struktur sumber punya tabel pets dan kolom email yang harus ditambah
	diffNewSQL = `
CREATE TABLE people (id int PRIMARY KEY, name varchar(50) NOT NULL, email text);
CREATE TABLE pets (id int PRIMARY KEY, owner_id int REFERENCES people (id), name text);
CREATE INDEX pets_owner_idx ON pets (owner_id);
`
)

// draft hasil diff dijalankan lewat alur lengkap sampai kedua database tidak berbeda lagi
func TestE2E_APISchemaDiffUntilConverged(t *testing.T) {
	f := newApprovalFixture(t)
	testutil.MustExec(t, f.a.target, diffOldSQL)
	source := testutil.NewTargetDB(t, diffNewSQL)

	cc := source.Config().ConnConfig
	body := f.a.targetBody("sumber")
	body["host"], body["port"], body["database_name"] = cc.Host, int(cc.Port), cc.Database
	sourceID := dataID(f.admin.must(201, "POST", "/admin/connections", body))
	for _, group := range []int64{f.groupA, f.groupB} {
		f.admin.must(204, "PUT", fmt.Sprintf("/admin/groups/%d/access/%d", group, sourceID), map[string]any{"access_level": "view_only"})
	}
	startWorker(t, f.a)
	editor, leadA, leadB := f.login(t, "editor"), f.login(t, "leada"), f.login(t, "leadb")
	diff := fmt.Sprintf("/databases/%d/schema-diff", f.db)

	// preview mengembalikan ID perubahan, dipisah antara yang aman dan yang menghapus data
	preview := func() (safe, destructive []string) {
		t.Helper()
		out := editor.must(200, "POST", diff+"/preview", map[string]any{"source_id": sourceID})
		for _, raw := range out["data"].(map[string]any)["changes"].([]any) {
			change := raw.(map[string]any)
			if id := change["id"].(string); change["destructive"].(bool) {
				destructive = append(destructive, id)
			} else {
				safe = append(safe, id)
			}
		}
		return safe, destructive
	}

	// draft hasil diff tidak punya SQL down, jadi ditandai irreversible dulu sebelum dibekukan
	apply := func(changes []string) {
		t.Helper()
		created := editor.must(201, "POST", diff, map[string]any{"source_id": sourceID, "changes": changes})
		base := fmt.Sprintf("/databases/%d/migrations/%d", f.db, int(created["data"].(map[string]any)["version"].(float64)))
		got := editor.must(200, "GET", base, nil)["data"].(map[string]any)["migration"].(map[string]any)
		editor.must(200, "PUT", base, map[string]any{
			"name": got["name"], "sql_up": got["sql_up"], "revision": 1,
			"irreversible": true, "irreversible_reason": "hasil schema diff",
		})
		editor.must(200, "POST", base+"/freeze", map[string]any{"revision": 2})

		exec := dataID(editor.must(201, "POST", base+"/executions", map[string]any{"kind": "apply"}))
		leadA.must(200, "POST", decidePath(exec, f.groupA, "approve"), nil)
		leadB.must(200, "POST", decidePath(exec, f.groupB, "approve"), nil)
		job := jobIDOf(editor.must(202, "POST", runPath(exec), nil))
		if status := waitJob(t, editor, job); status != "success" {
			t.Fatalf("eksekusi berakhir %s", status)
		}
	}

	safe, destructive := preview()
	if len(safe) != 6 || len(destructive) != 2 {
		t.Fatalf("aman = %d, menghapus data = %d, mau 6 dan 2", len(safe), len(destructive))
	}
	if f.a.tableExists(t, "pets") {
		t.Fatal("preview tidak boleh mengubah target")
	}

	apply(safe)
	if !f.a.tableExists(t, "pets") || !f.a.tableExists(t, "obsolete") || f.a.rows(t, "people") != "1" {
		t.Error("yang dipilih harus diterapkan tanpa menghapus yang tidak dipilih")
	}

	safe, destructive = preview()
	if len(safe) != 0 || len(destructive) != 2 {
		t.Fatalf("sisa aman = %d, menghapus data = %d, mau 0 dan 2", len(safe), len(destructive))
	}
	apply(destructive)
	if f.a.tableExists(t, "obsolete") {
		t.Error("tabel obsolete harus terhapus setelah dipilih sadar")
	}
	if safe, destructive = preview(); len(safe)+len(destructive) != 0 {
		t.Errorf("masih ada %d perbedaan", len(safe)+len(destructive))
	}
}
