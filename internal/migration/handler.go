package migration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"dbplatform/internal/domain"
	"dbplatform/internal/httpx"
)

const (
	listTemplate   = "migration/list.html"
	detailTemplate = "migration/detail.html"
	editorTemplate = "migration/editor.html"

	maxFormBytes = 512 << 10

	actionExecute = "execute"

	msgNotDraft   = "Hanya migration berstatus draft yang dapat diubah, dijalankan, atau dibatalkan."
	msgOutOfOrder = "Jalankan migration versi %d lebih dulu."
)

type migrationService interface {
	List(ctx context.Context, actor domain.Principal, connID int64) ([]domain.Migration, error)
	Get(ctx context.Context, actor domain.Principal, connID int64, version int) (domain.Migration, error)
	Create(ctx context.Context, actor domain.Principal, connID int64, in Input) (domain.Migration, error)
	Update(ctx context.Context, actor domain.Principal, connID int64, version int, in Input) (domain.Migration, error)
	Cancel(ctx context.Context, actor domain.Principal, connID int64, version int) error
	Execute(ctx context.Context, actor domain.Principal, connID int64, version int) (domain.Migration, error)
}

type ListPage struct {
	DBID       int64
	DBName     string
	Migrations []domain.Migration
	CanEdit    bool
}

type DetailPage struct {
	DBID      int64
	DBName    string
	Migration domain.Migration
	CanEdit   bool
}

type EditorPage struct {
	DBID    int64
	DBName  string
	Edit    bool
	Version int
	Name    string
	SQL     string
	Errors  map[string]string
}

type Handler struct {
	deps httpx.Deps
	svc  migrationService
}

func NewHandler(d httpx.Deps, svc migrationService) *Handler {
	return &Handler{deps: d, svc: svc}
}

// Lihat pakai DBView, ubah pakai DBEdit
func (h *Handler) Register(mux *http.ServeMux) {
	view := h.deps.DBView()
	edit := h.deps.DBEdit()
	mux.Handle("GET /databases/{dbID}/migrations", view(http.HandlerFunc(h.list)))
	mux.Handle("GET /databases/{dbID}/migrations/new", edit(http.HandlerFunc(h.newEditor)))
	mux.Handle("POST /databases/{dbID}/migrations", edit(http.HandlerFunc(h.create)))
	mux.Handle("GET /databases/{dbID}/migrations/{version}", view(http.HandlerFunc(h.detail)))
	mux.Handle("GET /databases/{dbID}/migrations/{version}/edit", edit(http.HandlerFunc(h.editEditor)))
	mux.Handle("POST /databases/{dbID}/migrations/{version}", edit(http.HandlerFunc(h.update)))
	mux.Handle("POST /databases/{dbID}/migrations/{version}/execute", edit(http.HandlerFunc(h.execute)))
	mux.Handle("POST /databases/{dbID}/migrations/{version}/cancel", edit(http.HandlerFunc(h.cancel)))
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

func (h *Handler) version(w http.ResponseWriter, r *http.Request) (int, bool) {
	v, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || v <= 0 {
		h.deps.HandleErr(w, r, domain.ErrNotFound)
		return 0, false
	}
	return v, true
}

func (h *Handler) parseForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Formulir tidak dapat dibaca. SQL maksimal 256 KB.", http.StatusBadRequest)
		return false
	}
	return true
}

// Cuma buat tampilan, penjaga sebenernya ada di route dan service
func (h *Handler) database(ctx context.Context, userID, dbID int64) (string, bool, error) {
	list, err := h.deps.Authz.ListAccessible(ctx, userID)
	if err != nil {
		return "", false, err
	}
	for _, d := range list {
		if d.ConnectionID == dbID {
			return d.Name, d.Level.AtLeast(domain.AccessViewEdit), nil
		}
	}
	return fmt.Sprintf("Database %d", dbID), false, nil
}

func (h *Handler) redirectDetail(w http.ResponseWriter, r *http.Request, dbID int64, version int) {
	http.Redirect(w, r, fmt.Sprintf("/databases/%d/migrations/%d", dbID, version), http.StatusSeeOther)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	dbID, ok := h.dbID(w, r)
	if !ok {
		return
	}
	migrations, err := h.svc.List(r.Context(), actor, dbID)
	if err != nil {
		h.deps.HandleErr(w, r, err)
		return
	}
	name, canEdit, err := h.database(r.Context(), actor.UserID, dbID)
	if err != nil {
		h.deps.HandleErr(w, r, err)
		return
	}
	h.deps.Render.Page(w, r, http.StatusOK, listTemplate, ListPage{DBID: dbID, DBName: name, Migrations: migrations, CanEdit: canEdit})
}

func (h *Handler) detail(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	dbID, ok := h.dbID(w, r)
	if !ok {
		return
	}
	version, ok := h.version(w, r)
	if !ok {
		return
	}
	m, err := h.svc.Get(r.Context(), actor, dbID, version)
	if err != nil {
		h.deps.HandleErr(w, r, err)
		return
	}
	name, canEdit, err := h.database(r.Context(), actor.UserID, dbID)
	if err != nil {
		h.deps.HandleErr(w, r, err)
		return
	}
	h.deps.Render.Page(w, r, http.StatusOK, detailTemplate, DetailPage{DBID: dbID, DBName: name, Migration: m, CanEdit: canEdit})
}

func (h *Handler) renderEditor(w http.ResponseWriter, r *http.Request, actor domain.Principal, status int, page EditorPage) {
	name, _, err := h.database(r.Context(), actor.UserID, page.DBID)
	if err != nil {
		h.deps.HandleErr(w, r, err)
		return
	}
	page.DBName = name
	h.deps.Render.Page(w, r, status, editorTemplate, page)
}

func (h *Handler) newEditor(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	dbID, ok := h.dbID(w, r)
	if !ok {
		return
	}
	page := EditorPage{DBID: dbID}
	// ?from=N mengisi editor dari migration lama, buat nyalin yang gagal
	if from, err := strconv.Atoi(r.URL.Query().Get("from")); err == nil && from > 0 {
		m, err := h.svc.Get(r.Context(), actor, dbID, from)
		if err != nil {
			h.deps.HandleErr(w, r, err)
			return
		}
		page.Name = truncate(fmt.Sprintf("Salinan versi %d: %s", m.Version, m.Name), maxNameChars)
		page.SQL = m.SQL
	}
	h.renderEditor(w, r, actor, http.StatusOK, page)
}

func (h *Handler) editEditor(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	dbID, ok := h.dbID(w, r)
	if !ok {
		return
	}
	version, ok := h.version(w, r)
	if !ok {
		return
	}
	m, err := h.svc.Get(r.Context(), actor, dbID, version)
	if err != nil {
		h.deps.HandleErr(w, r, err)
		return
	}
	if m.Status != domain.MigrationDraft {
		h.deps.Flash.Set(w, httpx.FlashError, msgNotDraft)
		h.redirectDetail(w, r, dbID, version)
		return
	}
	h.renderEditor(w, r, actor, http.StatusOK, EditorPage{DBID: dbID, Edit: true, Version: version, Name: m.Name, SQL: m.SQL})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	dbID, ok := h.dbID(w, r)
	if !ok || !h.parseForm(w, r) {
		return
	}
	in := Input{Name: r.PostFormValue("name"), SQL: r.PostFormValue("sql")}
	m, err := h.svc.Create(r.Context(), actor, dbID, in)
	if err != nil {
		var verr *domain.ValidationError
		if errors.As(err, &verr) {
			h.renderEditor(w, r, actor, http.StatusUnprocessableEntity, EditorPage{DBID: dbID, Name: in.Name, SQL: in.SQL, Errors: verr.Fields})
			return
		}
		h.deps.HandleErr(w, r, err)
		return
	}
	h.afterSave(w, r, actor, dbID, m.Version)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	dbID, ok := h.dbID(w, r)
	if !ok {
		return
	}
	version, ok := h.version(w, r)
	if !ok || !h.parseForm(w, r) {
		return
	}
	in := Input{Name: r.PostFormValue("name"), SQL: r.PostFormValue("sql")}
	_, err := h.svc.Update(r.Context(), actor, dbID, version, in)
	if err != nil {
		var verr *domain.ValidationError
		switch {
		case errors.As(err, &verr):
			h.renderEditor(w, r, actor, http.StatusUnprocessableEntity, EditorPage{DBID: dbID, Edit: true, Version: version, Name: in.Name, SQL: in.SQL, Errors: verr.Fields})
		case errors.Is(err, domain.ErrMigrationNotDraft):
			h.deps.Flash.Set(w, httpx.FlashError, msgNotDraft)
			h.redirectDetail(w, r, dbID, version)
		default:
			h.deps.HandleErr(w, r, err)
		}
		return
	}
	h.afterSave(w, r, actor, dbID, version)
}

// Tombol Simpan lalu jalanin kirim action=execute
func (h *Handler) afterSave(w http.ResponseWriter, r *http.Request, actor domain.Principal, dbID int64, version int) {
	if r.PostFormValue("action") == actionExecute {
		h.run(w, r, actor, dbID, version)
		return
	}
	h.deps.Flash.Set(w, httpx.FlashSuccess, fmt.Sprintf("Draft versi %d disimpan.", version))
	h.redirectDetail(w, r, dbID, version)
}

func (h *Handler) execute(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	dbID, ok := h.dbID(w, r)
	if !ok {
		return
	}
	version, ok := h.version(w, r)
	if !ok {
		return
	}
	h.run(w, r, actor, dbID, version)
}

// Hasil eksekusi dijadiin pesan flash
func (h *Handler) run(w http.ResponseWriter, r *http.Request, actor domain.Principal, dbID int64, version int) {
	m, err := h.svc.Execute(r.Context(), actor, dbID, version)
	var ooo *OutOfOrderError
	switch {
	case err == nil && m.Status == domain.MigrationSuccess:
		h.deps.Flash.Set(w, httpx.FlashSuccess, fmt.Sprintf("Migration versi %d berhasil dijalankan.", version))
	case err == nil && m.Warning != "":
		h.deps.Flash.Set(w, httpx.FlashError, fmt.Sprintf("Migration versi %d gagal. Perhatikan peringatan di halaman ini.", version))
	case err == nil:
		h.deps.Flash.Set(w, httpx.FlashError, fmt.Sprintf("Migration versi %d gagal; seluruh perubahan dibatalkan.", version))
	case errors.As(err, &ooo):
		h.deps.Flash.Set(w, httpx.FlashError, fmt.Sprintf(msgOutOfOrder, ooo.Blocking))
	case errors.Is(err, domain.ErrMigrationNotDraft):
		h.deps.Flash.Set(w, httpx.FlashError, msgNotDraft)
	default:
		h.deps.HandleErr(w, r, err)
		return
	}
	h.redirectDetail(w, r, dbID, version)
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	dbID, ok := h.dbID(w, r)
	if !ok {
		return
	}
	version, ok := h.version(w, r)
	if !ok {
		return
	}
	err := h.svc.Cancel(r.Context(), actor, dbID, version)
	switch {
	case err == nil:
		h.deps.Flash.Set(w, httpx.FlashSuccess, fmt.Sprintf("Draft versi %d dibatalkan.", version))
	case errors.Is(err, domain.ErrMigrationNotDraft):
		h.deps.Flash.Set(w, httpx.FlashError, msgNotDraft)
	default:
		h.deps.HandleErr(w, r, err)
		return
	}
	h.redirectDetail(w, r, dbID, version)
}
