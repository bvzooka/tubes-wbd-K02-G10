package schemadiff

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"dbplatform/internal/domain"
	"dbplatform/internal/httpx"
)

const (
	pageTemplate = "migration/schema_diff.html"

	maxFormBytes = 256 << 10

	actionCreate = "create"

	msgPickSource = "Pilih database sumber."
)

type diffService interface {
	Sources(ctx context.Context, actor domain.Principal, targetID int64) ([]domain.AccessibleDatabase, error)
	Preview(ctx context.Context, actor domain.Principal, targetID, sourceID int64) (*Result, error)
	CreateDraft(ctx context.Context, actor domain.Principal, targetID, sourceID int64, selected []string) (domain.Migration, error)
}

type ChangeView struct {
	ID          string
	Label       string
	Object      string
	SQL         string
	Destructive bool
	Checked     bool
}

type Page struct {
	DBID             int64
	DBName           string
	Sources          []domain.AccessibleDatabase
	SourceID         int64
	SourceName       string
	Compared         bool
	Changes          []ChangeView
	DestructiveCount int
	Errors           map[string]string
}

type Handler struct {
	deps httpx.Deps
	svc  diffService
}

func NewHandler(d httpx.Deps, svc diffService) *Handler {
	return &Handler{deps: d, svc: svc}
}

func (h *Handler) Register(mux *http.ServeMux) {
	edit := h.deps.DBEdit()
	mux.Handle("GET /databases/{dbID}/schema-diff", edit(http.HandlerFunc(h.pick)))
	mux.Handle("POST /databases/{dbID}/schema-diff", edit(http.HandlerFunc(h.submit)))
}

func kindLabel(kind string) string {
	switch kind {
	case KindCreateSchema:
		return "Buat schema"
	case KindCreateTable:
		return "Buat tabel"
	case KindAddColumn:
		return "Tambah kolom"
	case KindAlterColumnType:
		return "Ubah tipe kolom"
	case KindAlterColumnNull:
		return "Ubah aturan NULL kolom"
	case KindAlterColumnDefault:
		return "Ubah nilai default kolom"
	case KindDropConstraint:
		return "Hapus constraint"
	case KindAddConstraint:
		return "Tambah constraint"
	case KindDropIndex:
		return "Hapus index"
	case KindCreateIndex:
		return "Buat index"
	case KindDropColumn:
		return "Hapus kolom"
	case KindDropTable:
		return "Hapus tabel"
	}
	return kind
}

func (h *Handler) actor(w http.ResponseWriter, r *http.Request) (domain.Principal, bool) {
	p, ok := domain.PrincipalFrom(r.Context())
	if !ok {
		h.deps.HandleErr(w, r, domain.ErrUnauthenticated)
	}
	return p, ok
}

func (h *Handler) dbID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("dbID"), 10, 64)
	if err != nil || id <= 0 {
		h.deps.HandleErr(w, r, domain.ErrNotFound)
		return 0, false
	}
	return id, true
}

func (h *Handler) databaseName(ctx context.Context, userID, dbID int64) (string, error) {
	list, err := h.deps.Authz.ListAccessible(ctx, userID)
	if err != nil {
		return "", err
	}
	for _, d := range list {
		if d.ConnectionID == dbID {
			return d.Name, nil
		}
	}
	return fmt.Sprintf("Database %d", dbID), nil
}

func (h *Handler) base(ctx context.Context, actor domain.Principal, dbID int64) (Page, error) {
	sources, err := h.svc.Sources(ctx, actor, dbID)
	if err != nil {
		return Page{}, err
	}
	name, err := h.databaseName(ctx, actor.UserID, dbID)
	if err != nil {
		return Page{}, err
	}
	return Page{DBID: dbID, DBName: name, Sources: sources}, nil
}

func (h *Handler) renderPick(w http.ResponseWriter, r *http.Request, actor domain.Principal, dbID, sourceID int64, status int, errs map[string]string) {
	page, err := h.base(r.Context(), actor, dbID)
	if err != nil {
		h.deps.HandleErr(w, r, err)
		return
	}
	page.SourceID, page.Errors = sourceID, errs
	h.deps.Render.Page(w, r, status, pageTemplate, page)
}

func (h *Handler) pick(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	dbID, ok := h.dbID(w, r)
	if !ok {
		return
	}
	h.renderPick(w, r, actor, dbID, 0, http.StatusOK, nil)
}

func (h *Handler) submit(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	dbID, ok := h.dbID(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Formulir tidak dapat dibaca.", http.StatusBadRequest)
		return
	}
	sourceID, err := strconv.ParseInt(r.PostFormValue("source_id"), 10, 64)
	if err != nil || sourceID <= 0 {
		h.renderPick(w, r, actor, dbID, 0, http.StatusUnprocessableEntity, map[string]string{"source_id": msgPickSource})
		return
	}

	if r.PostFormValue("action") == actionCreate {
		h.create(w, r, actor, dbID, sourceID)
		return
	}
	h.renderPreview(w, r, actor, dbID, sourceID, nil, http.StatusOK, nil)
}

func (h *Handler) renderPreview(w http.ResponseWriter, r *http.Request, actor domain.Principal, dbID, sourceID int64, checked map[string]bool, status int, errs map[string]string) {
	result, err := h.svc.Preview(r.Context(), actor, dbID, sourceID)
	if err != nil {
		h.previewError(w, r, actor, dbID, sourceID, err)
		return
	}
	page, err := h.base(r.Context(), actor, dbID)
	if err != nil {
		h.deps.HandleErr(w, r, err)
		return
	}
	page.SourceID, page.SourceName, page.Compared, page.Errors = sourceID, result.SourceName, true, errs
	for _, c := range result.Changes {
		v := ChangeView{ID: c.ID, Label: kindLabel(c.Kind), Object: c.Object, SQL: c.SQL, Destructive: c.Destructive}
		if checked == nil {
			// Yang bisa hapus data ga dicentang default
			v.Checked = !c.Destructive
		} else {
			v.Checked = checked[c.ID]
		}
		if c.Destructive {
			page.DestructiveCount++
		}
		page.Changes = append(page.Changes, v)
	}
	h.deps.Render.Page(w, r, status, pageTemplate, page)
}

func (h *Handler) previewError(w http.ResponseWriter, r *http.Request, actor domain.Principal, dbID, sourceID int64, err error) {
	var verr *domain.ValidationError
	switch {
	case errors.As(err, &verr):
		h.renderPick(w, r, actor, dbID, sourceID, http.StatusUnprocessableEntity, verr.Fields)
	case errors.Is(err, domain.ErrUnsupported):
		h.renderPick(w, r, actor, dbID, sourceID, http.StatusUnprocessableEntity, map[string]string{"source_id": "Schema diff hanya mendukung database PostgreSQL."})
	default:
		h.deps.HandleErr(w, r, err)
	}
}

// Habis dibuat diarahin ke editor biar SQL ditinjau dulu
func (h *Handler) create(w http.ResponseWriter, r *http.Request, actor domain.Principal, dbID, sourceID int64) {
	selected := r.PostForm["change"]
	m, err := h.svc.CreateDraft(r.Context(), actor, dbID, sourceID, selected)
	if err == nil {
		h.deps.Flash.Set(w, httpx.FlashSuccess, fmt.Sprintf("Draft versi %d dibuat dari schema diff. Tinjau SQL-nya sebelum menjalankan.", m.Version))
		http.Redirect(w, r, fmt.Sprintf("/databases/%d/migrations/%d/edit", dbID, m.Version), http.StatusSeeOther)
		return
	}

	var verr *domain.ValidationError
	if errors.As(err, &verr) && verr.Fields["source_id"] == "" {
		checked := make(map[string]bool, len(selected))
		for _, id := range selected {
			checked[id] = true
		}
		h.renderPreview(w, r, actor, dbID, sourceID, checked, http.StatusUnprocessableEntity, map[string]string{"changes": firstMessage(verr.Fields)})
		return
	}
	h.previewError(w, r, actor, dbID, sourceID, err)
}

func firstMessage(fields map[string]string) string {
	if msg := fields["changes"]; msg != "" {
		return msg
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fields[k])
	}
	return strings.Join(parts, " ")
}
