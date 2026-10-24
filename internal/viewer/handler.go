package viewer

import (
	"net/http"
	"strconv"
	"dbplatform/internal/domain"
	"dbplatform/internal/httpx"
)

type DatabasesPage struct {
	Databases []DatabaseItem
}

type DataPage struct {
	View *DataView
	Pager httpx.Pager
}

type Handler struct {
	svc *Service
	d httpx.Deps
}

func NewHandler(svc *Service, d httpx.Deps) *Handler { return &Handler{svc: svc, d: d} }

func (h *Handler) Register(mux *http.ServeMux) {
	view := h.d.DBView()
	mux.Handle("GET /databases", h.d.Authed()(http.HandlerFunc(h.databases)))
	mux.Handle("GET /databases/{dbID}", view(http.HandlerFunc(h.overview)))
	mux.Handle("GET /databases/{dbID}/tables/{schema}/{table}", view(http.HandlerFunc(h.table)))
	mux.Handle("GET /databases/{dbID}/tables/{schema}/{table}/data", view(http.HandlerFunc(h.data)))
}

func (h *Handler) principal(w http.ResponseWriter, r *http.Request) (domain.Principal, bool) {
	p, ok := domain.PrincipalFrom(r.Context())
	if !ok {
		h.d.HandleErr(w, r, domain.ErrUnauthenticated)
	}
	return p, ok
}

func (h *Handler) dbID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("dbID"), 10, 64)
	if err != nil || id < 1 {
		h.d.HandleErr(w, r, domain.ErrNotFound)
		return 0, false
	}
	return id, true
}

func tableRef(r *http.Request) domain.TableRef {
	return domain.TableRef{Schema: r.PathValue("schema"), Table: r.PathValue("table")}
}

func (h *Handler) databases(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}
	dbs, err := h.svc.Databases(r.Context(), p)
	if err != nil {
		h.d.HandleErr(w, r, err)
		return
	}
	h.d.Render.Page(w, r, http.StatusOK, "viewer/databases.html", DatabasesPage{Databases: dbs})
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}
	id, ok := h.dbID(w, r)
	if !ok {
		return
	}
	ov, err := h.svc.Overview(r.Context(), p, id)
	if err != nil {
		h.d.HandleErr(w, r, err)
		return
	}
	h.d.Render.Page(w, r, http.StatusOK, "viewer/overview.html", ov)
}

func (h *Handler) table(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}
	id, ok := h.dbID(w, r)
	if !ok {
		return
	}
	t, err := h.svc.Table(r.Context(), p, id, tableRef(r))
	if err != nil {
		h.d.HandleErr(w, r, err)
		return
	}
	h.d.Render.Page(w, r, http.StatusOK, "viewer/table.html", t)
}

func (h *Handler) data(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}
	id, ok := h.dbID(w, r)
	if !ok {
		return
	}
	page := httpx.ParsePage(r)
	v, err := h.svc.Data(r.Context(), p, id, tableRef(r), page)
	if err != nil {
		h.d.HandleErr(w, r, err)
		return
	}
	rr := r.Clone(r.Context())
	rr.URL.Path = r.URL.EscapedPath()
	pager := httpx.NewPager(rr, page, v.Result.HasNext)
	h.d.Render.Page(w, r, http.StatusOK, "viewer/data.html", DataPage{View: v, Pager: pager})
}
