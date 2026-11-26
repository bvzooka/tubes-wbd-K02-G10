package schemadiff_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dbplatform/internal/access"
	"dbplatform/internal/apiv1"
	"dbplatform/internal/domain"
	"dbplatform/internal/migration"
	"dbplatform/internal/schemadiff"
)

func (e diffEnv) api() (*schemadiff.APIHandler, *migration.Repo) {
	repo := migration.NewRepo(e.app)
	definitions := migration.NewDefinitionService(repo, access.New(e.app), slog.New(slog.NewTextHandler(io.Discard, nil)))
	return schemadiff.NewAPIHandler(e.svc, definitions), repo
}

func (e diffEnv) call(t *testing.T, h apiv1.Handler, actor domain.Principal, body string) (int, map[string]any, error) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	req.SetPathValue("dbID", fmt.Sprint(e.targetID))
	req = req.WithContext(domain.WithPrincipal(req.Context(), actor))
	rec := httptest.NewRecorder()
	if err := h(rec, req); err != nil {
		return 0, nil, err
	}
	var out struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("respons bukan JSON: %s", rec.Body.String())
	}
	return rec.Code, out.Data, nil
}

func TestAPIHandler_Integration_PreviewThenDraft(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	e := newDiffEnv(t)
	api, repo := e.api()

	_, data, err := e.call(t, api.Sources, e.editor, "")
	if err != nil || len(data["sources"].([]any)) != 1 {
		t.Fatalf("sumber = %v %v", data, err)
	}
	preview := fmt.Sprintf(`{"source_id":%d}`, e.sourceID)
	_, data, err = e.call(t, api.Preview, e.editor, preview)
	if err != nil || data["source_name"] != "sumber" {
		t.Fatalf("pratinjau = %v %v", data, err)
	}
	var chosen []string
	for _, raw := range data["changes"].([]any) {
		change := raw.(map[string]any)
		if change["destructive"] == false {
			chosen = append(chosen, change["id"].(string))
		}
	}
	body, _ := json.Marshal(map[string]any{"source_id": e.sourceID, "changes": chosen})

	code, data, err := e.call(t, api.Create, e.editor, string(body))
	if err != nil || code != http.StatusCreated || data["version"] != float64(1) {
		t.Fatalf("buat draft = %d %v %v", code, data, err)
	}
	d, err := repo.GetDefinition(context.Background(), e.targetID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if d.State != domain.StateDraft || d.Name != "Schema diff dari sumber" || d.UpSQL == "" || d.DownSQL != "" {
		t.Errorf("draft = %+v", d)
	}
}

func TestAPIHandler_Integration_Rejects(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	e := newDiffEnv(t)
	api, _ := e.api()
	viewer := e.user(t, "pembaca", map[int64]domain.AccessLevel{e.targetID: domain.AccessViewOnly, e.sourceID: domain.AccessViewOnly})
	preview := fmt.Sprintf(`{"source_id":%d}`, e.sourceID)

	tests := []struct {
		name    string
		handler apiv1.Handler
		actor   domain.Principal
		body    string
		want    error
	}{
		{"view only melihat sumber", api.Sources, viewer, "", domain.ErrForbidden},
		{"view only pratinjau", api.Preview, viewer, preview, domain.ErrForbidden},
		{"sumber sama dengan tujuan", api.Preview, e.editor, fmt.Sprintf(`{"source_id":%d}`, e.targetID), domain.ErrValidation},
		{"tanpa pilihan", api.Create, e.editor, preview, domain.ErrValidation},
		{"ID perubahan asing", api.Create, e.editor, fmt.Sprintf(`{"source_id":%d,"changes":["000000000000"]}`, e.sourceID), domain.ErrValidation},
		{"SQL dari browser ditolak", api.Create, e.editor, `{"sql":"DROP TABLE people"}`, domain.ErrValidation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := e.call(t, tt.handler, tt.actor, tt.body); !errors.Is(err, tt.want) {
				t.Errorf("error = %v, mau %v", err, tt.want)
			}
		})
	}
	if n := e.draftCount(t); n != 0 {
		t.Errorf("tidak boleh ada draft, ada %d", n)
	}
}
