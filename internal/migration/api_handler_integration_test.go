package migration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dbplatform/internal/apiv1"
	"dbplatform/internal/domain"
	"dbplatform/internal/migration"
)

type apiCall struct {
	actor  domain.Principal
	target string
	path   map[string]string
	body   string
}

// call memanggil handler langsung, penjaga akses di router diuji terpisah di cmd/server
func call(t *testing.T, h apiv1.Handler, in apiCall) (int, map[string]any, error) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/x"+in.target, strings.NewReader(in.body))
	for k, v := range in.path {
		req.SetPathValue(k, v)
	}
	req = req.WithContext(domain.WithPrincipal(req.Context(), in.actor))
	rec := httptest.NewRecorder()
	if err := h(rec, req); err != nil {
		return 0, nil, err
	}
	var out struct {
		Data map[string]any `json:"data"`
	}
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("respons bukan JSON: %s", rec.Body.String())
		}
	}
	return rec.Code, out.Data, nil
}

func TestAPIHandler_Integration_DraftToQueue(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	f := newFlowEnv(t)
	api := migration.NewAPIHandler(f.service, f.executions)
	db := map[string]string{"dbID": fmt.Sprint(f.conn)}
	version := map[string]string{"dbID": fmt.Sprint(f.conn), "version": "1"}

	code, data, err := call(t, api.Create, apiCall{actor: f.editor, path: db,
		body: `{"name":"buat tabel","sql_up":"CREATE TABLE a (id int)","sql_down":"DROP TABLE a"}`})
	if err != nil || code != http.StatusCreated || data["state"] != "draft" {
		t.Fatalf("create = %d %v %v", code, data, err)
	}
	if _, data, err = call(t, api.Freeze, apiCall{actor: f.editor, path: version, body: `{"revision":1}`}); err != nil || data["state"] != "ready" {
		t.Fatalf("freeze = %v %v", data, err)
	}
	code, data, err = call(t, api.Request, apiCall{actor: f.editor, path: version, body: `{"kind":"apply"}`})
	if err != nil || code != http.StatusCreated || data["status"] != "pending_review" {
		t.Fatalf("request = %d %v %v", code, data, err)
	}
	execID := int64(data["id"].(float64))
	exec := map[string]string{"execID": fmt.Sprint(execID)}

	_, data, err = call(t, api.Detail, apiCall{actor: f.viewer, path: exec})
	if err != nil || len(data["approvals"].([]any)) != 2 || data["can_edit"] != false || data["job_id"] != "" {
		t.Fatalf("detail = %v %v", data, err)
	}
	f.approveByViewer(t, execID)
	code, data, err = call(t, api.Run, apiCall{actor: f.editor, path: exec})
	if err != nil || code != http.StatusAccepted {
		t.Fatalf("run = %d %v", code, err)
	}
	jobID := data["job_id"].(string)

	_, data, err = call(t, api.Job, apiCall{actor: f.viewer, path: map[string]string{"jobID": jobID}})
	if err != nil || data["execution_status"] != "queued" || data["state"] != "queued" {
		t.Errorf("job = %v %v", data, err)
	}
	_, data, err = call(t, api.Get, apiCall{actor: f.viewer, path: version})
	if err != nil || len(data["executions"].([]any)) != 1 {
		t.Errorf("detail versi = %v %v", data, err)
	}
	_, data, err = call(t, api.History, apiCall{actor: f.viewer, path: db, target: "?hide_rollback=1&page=abc"})
	if err != nil || len(data["items"].([]any)) != 0 || data["page"] != float64(1) {
		t.Errorf("riwayat kosong harus [] di halaman 1: %v %v", data, err)
	}
}

func TestAPIHandler_Integration_Rejects(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	f := newFlowEnv(t)
	api := migration.NewAPIHandler(f.service, f.executions)
	d := f.ready(t)
	db := map[string]string{"dbID": fmt.Sprint(f.conn)}
	ex, err := f.apply(d)
	if err != nil {
		t.Fatal(err)
	}
	exec := map[string]string{"execID": fmt.Sprint(ex.ID)}

	tests := []struct {
		name    string
		handler apiv1.Handler
		in      apiCall
		want    error
	}{
		{"orang luar melihat daftar", api.List, apiCall{actor: f.outsider, path: db}, domain.ErrForbidden},
		{"orang luar melihat permintaan", api.Detail, apiCall{actor: f.outsider, path: exec}, domain.ErrForbidden},
		{"view only menjalankan", api.Run, apiCall{actor: f.viewer, path: exec}, domain.ErrForbidden},
		{"view only membatalkan", api.CancelExecution, apiCall{actor: f.viewer, path: exec}, domain.ErrForbidden},
		{"field tidak dikenal", api.Create, apiCall{actor: f.editor, path: db, body: `{"nama":"x"}`}, domain.ErrValidation},
		{"versi bukan angka", api.Get, apiCall{actor: f.editor, path: map[string]string{"dbID": "1", "version": "x"}}, domain.ErrNotFound},
		{"job id ngawur", api.Job, apiCall{actor: f.editor, path: map[string]string{"jobID": "1; DROP TABLE"}}, domain.ErrNotFound},
		{"job tidak ada", api.Job, apiCall{actor: f.editor, path: map[string]string{"jobID": "00000000-0000-0000-0000-000000000000"}}, domain.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := call(t, tt.handler, tt.in); !errors.Is(err, tt.want) {
				t.Errorf("error = %v, mau %v", err, tt.want)
			}
		})
	}
	if _, err := f.repo.GetExecution(context.Background(), ex.ID); err != nil {
		t.Fatal(err)
	}
}
