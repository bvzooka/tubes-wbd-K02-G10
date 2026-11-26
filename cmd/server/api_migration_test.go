package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dbplatform/internal/access"
	"dbplatform/internal/approval"
	"dbplatform/internal/clock"
	"dbplatform/internal/config"
	"dbplatform/internal/connmgr"
	"dbplatform/internal/migration"
	"dbplatform/internal/queue"
)

// startWorker menjalankan worker asli dengan rakitan yang sama seperti cmd/worker
func startWorker(t *testing.T, a *e2eApp) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	clk := clock.System{}
	drivers := connmgr.NewProvider(connmgr.NewRepo(a.app, make([]byte, 32)), connmgr.DefaultFactory)
	repo := migration.NewRepo(a.app)
	runner := migration.NewRunner(migration.RunnerDeps{
		Store: repo, Drivers: drivers, Clock: clk, Logger: logger,
		Revalidator: migration.NewRevalidator(a.app, repo, access.New(a.app), approval.NewService(a.app, clk, logger), drivers),
	})
	worker := queue.NewWorker(queue.NewRepo(a.app, clk), runner, queue.Config{
		ID: "e2e", Concurrency: 1, PollInterval: 50 * time.Millisecond,
		Lease: 5 * time.Second, Heartbeat: time.Second, RetryBase: 100 * time.Millisecond,
	}, clk, logger)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { worker.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done; drivers.Close() })
}

// waitJob bertanya berulang seperti halaman monitor sampai job selesai
func waitJob(t *testing.T, c *apiClient, jobID string) string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		job := c.must(200, "GET", "/jobs/"+jobID, nil)["data"].(map[string]any)
		if job["state"] == "done" {
			return job["execution_status"].(string)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("job tidak selesai")
	return ""
}

func TestE2E_APIMigrationApplyAndRollback(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	a := newE2EApp(t)
	admin := a.adminAPI(t)
	db := dataID(admin.must(201, "POST", "/admin/connections", a.targetBody("sampel")))
	editorID := dataID(admin.must(201, "POST", "/admin/users", map[string]any{"username": "editor", "password": "password-editor"}))
	leadID := dataID(admin.must(201, "POST", "/admin/users", map[string]any{"username": "lead", "password": "password-lead1"}))
	group := dataID(admin.must(201, "POST", "/admin/groups", map[string]any{"name": "Tim Data"}))
	for _, id := range []int64{editorID, leadID} {
		admin.must(204, "POST", fmt.Sprintf("/admin/groups/%d/members", group), map[string]any{"user_id": id})
	}
	admin.must(204, "PUT", fmt.Sprintf("/admin/groups/%d/leader", group), map[string]any{"user_id": leadID})
	admin.must(204, "PUT", fmt.Sprintf("/admin/groups/%d/access/%d", group, db), map[string]any{"access_level": "view_edit"})
	startWorker(t, a)

	editor := newAPIClient(t, a.base)
	editor.login("editor", "password-editor")
	lead := newAPIClient(t, a.base)
	lead.login("lead", "password-lead1")

	base := fmt.Sprintf("/databases/%d/migrations", db)
	editor.must(201, "POST", base, map[string]any{
		"name": "tabel promo", "sql_up": "CREATE TABLE promo (id int)", "sql_down": "DROP TABLE promo",
	})
	editor.must(200, "POST", base+"/1/freeze", map[string]any{"revision": 1})

	// request, disetujui leader, dijalankan, ditunggu sampai selesai
	execute := func(kind string) {
		t.Helper()
		id := dataID(editor.must(201, "POST", base+"/1/executions", map[string]any{"kind": kind}))
		editor.must(409, "POST", fmt.Sprintf("/executions/%d/run", id), nil)
		lead.must(200, "POST", fmt.Sprintf("/executions/%d/approvals/%d/approve", id, group), nil)
		out := editor.must(202, "POST", fmt.Sprintf("/executions/%d/run", id), nil)
		jobID := out["data"].(map[string]any)["job_id"].(string)
		if status := waitJob(t, editor, jobID); status != "success" {
			detail := editor.must(200, "GET", fmt.Sprintf("/executions/%d", id), nil)
			t.Fatalf("%s berakhir %s: %v", kind, status, detail)
		}
	}

	execute("apply")
	if !a.tableExists(t, "promo") {
		t.Fatal("tabel tidak dibuat")
	}
	execute("rollback")
	if a.tableExists(t, "promo") {
		t.Fatal("tabel masih ada setelah rollback")
	}

	history := fmt.Sprintf("/databases/%d/executions", db)
	all := editor.must(200, "GET", history, nil)["data"].(map[string]any)["items"].([]any)
	hidden := editor.must(200, "GET", history+"?hide_rollback=1", nil)["data"].(map[string]any)["items"].([]any)
	if len(all) != 2 || len(hidden) != 0 {
		t.Errorf("riwayat %d, tersembunyi %d", len(all), len(hidden))
	}
}

// mode SPA dulu membuat server panic karena routenya bentrok
func TestE2E_SPAModeServesIndexAndKeepsAPI(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("<title>spa</title>"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newE2EAppWith(t, func(cfg *config.Config) { cfg.WebDistDir = dist })

	// halaman HTML lama tidak boleh ikut aktif, termasuk eksekusi migration tanpa persetujuan
	for _, path := range []string{"/", "/databases", "/databases/1/migrations", "/login"} {
		resp, err := http.Get(a.base + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "<title>spa</title>") {
			t.Errorf("%s: %d %s", path, resp.StatusCode, body)
		}
	}
	client := newAPIClient(t, a.base)
	client.must(404, "GET", "/tidak-ada", nil)
	client.must(401, "GET", "/auth/me", nil)
}
