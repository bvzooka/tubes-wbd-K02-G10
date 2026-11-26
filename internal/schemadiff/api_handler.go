package schemadiff

import (
	"context"
	"net/http"

	"dbplatform/internal/apiv1"
	"dbplatform/internal/domain"
	"dbplatform/internal/migration"
)

type draftCreator interface {
	Create(ctx context.Context, actor domain.Principal, connID int64, in migration.DraftInput) (migration.Definition, error)
}

type APIHandler struct {
	svc    *Service
	drafts draftCreator
}

func NewAPIHandler(svc *Service, drafts draftCreator) *APIHandler {
	return &APIHandler{svc: svc, drafts: drafts}
}

type sourceJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type changeJSON struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Object      string `json:"object"`
	SQL         string `json:"sql"`
	Destructive bool   `json:"destructive"`
}

// Sources mengembalikan database yang bisa dipakai sebagai pembanding
func (h *APIHandler) Sources(w http.ResponseWriter, r *http.Request) error {
	targetID, err := apiv1.PathID(r, "dbID")
	if err != nil {
		return err
	}
	databases, err := h.svc.Sources(r.Context(), apiv1.Actor(r), targetID)
	if err != nil {
		return err
	}
	sources := make([]sourceJSON, 0, len(databases))
	for _, d := range databases {
		sources = append(sources, sourceJSON{ID: d.ConnectionID, Name: d.Name})
	}
	return apiv1.OK(w, map[string]any{"sources": sources})
}

// Preview menghitung perbedaan tanpa mengubah apa pun, POST karena membaca dua database target
func (h *APIHandler) Preview(w http.ResponseWriter, r *http.Request) error {
	targetID, err := apiv1.PathID(r, "dbID")
	if err != nil {
		return err
	}
	var in struct {
		SourceID int64 `json:"source_id"`
	}
	if err := apiv1.Decode(w, r, &in); err != nil {
		return err
	}
	result, err := h.svc.Preview(r.Context(), apiv1.Actor(r), targetID, in.SourceID)
	if err != nil {
		return err
	}
	changes := make([]changeJSON, 0, len(result.Changes))
	for _, c := range result.Changes {
		changes = append(changes, changeJSON{ID: c.ID, Kind: c.Kind, Object: c.Object, SQL: c.SQL, Destructive: c.Destructive})
	}
	return apiv1.OK(w, map[string]any{
		"target_name": result.TargetName, "source_name": result.SourceName, "changes": changes,
	})
}

// Create membuat draft dari perubahan terpilih, browser hanya mengirim ID sehingga SQL-nya selalu disusun server
func (h *APIHandler) Create(w http.ResponseWriter, r *http.Request) error {
	targetID, err := apiv1.PathID(r, "dbID")
	if err != nil {
		return err
	}
	var in struct {
		SourceID int64    `json:"source_id"`
		Changes  []string `json:"changes"`
	}
	if err := apiv1.Decode(w, r, &in); err != nil {
		return err
	}
	ctx, actor := r.Context(), apiv1.Actor(r)
	name, sql, err := h.svc.DraftSQL(ctx, actor, targetID, in.SourceID, in.Changes)
	if err != nil {
		return err
	}
	d, err := h.drafts.Create(ctx, actor, targetID, migration.DraftInput{Name: name, UpSQL: sql})
	if err != nil {
		return err
	}
	return apiv1.Created(w, map[string]any{"version": d.Version, "name": d.Name})
}
