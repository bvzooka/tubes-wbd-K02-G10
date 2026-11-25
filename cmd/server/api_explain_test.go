package main

import (
	"context"
	"fmt"
	"testing"
)

// fixture viewer ditambah satu user view_edit
func newExplainFixture(t *testing.T) (*viewerFixture, *apiClient) {
	t.Helper()
	f := newViewerFixture(t)
	editorID := dataID(f.admin.must(201, "POST", "/admin/users", map[string]any{"username": "penyunting", "password": "password-sunting1"}))
	group := dataID(f.admin.must(201, "POST", "/admin/groups", map[string]any{"name": "Penyunting"}))
	f.admin.must(204, "POST", fmt.Sprintf("/admin/groups/%d/members", group), map[string]any{"user_id": editorID})
	f.admin.must(204, "PUT", fmt.Sprintf("/admin/groups/%d/access/%d", group, f.dbID), map[string]any{"access_level": "view_edit"})

	editor := newAPIClient(t, f.app.base)
	editor.login("penyunting", "password-sunting1")
	return f, editor
}

func TestExplainAPI_Integration_RequiresViewEdit(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	f, _ := newExplainFixture(t)
	body := map[string]any{"sql": "SELECT * FROM customers"}

	newAPIClient(t, f.app.base).must(401, "POST", f.path("/explain"), body)
	f.reader.must(403, "POST", f.path("/explain"), body)
	f.outsider.must(403, "POST", f.path("/explain"), body)
}

func TestExplainAPI_Integration_ReturnsPlanTree(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	f, editor := newExplainFixture(t)

	out := editor.must(200, "POST", f.path("/explain"), map[string]any{
		"sql": "SELECT c.name, o.total FROM customers c JOIN orders o ON o.customer_id = c.id",
	})["data"].(map[string]any)
	plan := out["plan"].(map[string]any)
	nodes := plan["nodes"].([]any)
	if out["dialect"] != "postgres" || len(nodes) < 2 || plan["execution_time_ms"] != nil {
		t.Fatalf("hasil explain: %v", out)
	}
	if root := nodes[0].(map[string]any); root["depth"] != float64(0) || root["node_type"] == "" {
		t.Fatalf("node akar: %v", root)
	}
}

func TestExplainAPI_Integration_AnalyzeRollsBackChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	f, editor := newExplainFixture(t)

	out := editor.must(200, "POST", f.path("/explain"), map[string]any{
		"sql": "DELETE FROM customers", "analyze": true,
	})["data"].(map[string]any)
	if out["plan"].(map[string]any)["execution_time_ms"] == nil {
		t.Fatalf("ANALYZE harus punya waktu eksekusi: %v", out)
	}

	var n int
	if err := f.app.target.QueryRow(context.Background(), `SELECT count(*) FROM customers`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("EXPLAIN ANALYZE tidak boleh meninggalkan perubahan: %d baris, err %v", n, err)
	}
}

func TestExplainAPI_Integration_RejectsInvalidSQLWithMessage(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	f, editor := newExplainFixture(t)

	tests := []struct {
		name   string
		sql    string
		status int
	}{
		{"kosong", "  ", 422},
		{"DDL", "DROP TABLE customers", 422},
		{"dua statement", "SELECT 1; SELECT 2", 422},
		{"ditolak database", "SELECT * FROM tabel_tidak_ada", 422},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, out := editor.do("POST", f.path("/explain"), map[string]any{"sql": tt.sql})
			errBody, _ := out["error"].(map[string]any)
			msg, _ := errBody["message"].(string)
			if status != tt.status || msg == "" || msg == "Data tidak valid." {
				t.Fatalf("pesan harus spesifik, dapat %v", out)
			}
		})
	}
}
