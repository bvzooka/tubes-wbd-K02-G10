package explain

import (
	"context"
	"net/http"
	"strconv"

	"dbplatform/internal/contract"
	"dbplatform/internal/domain"
)

// Renderer adalah bagian httpx.Renderer yang dipakai handler ini.
type Renderer interface {
	Page(w http.ResponseWriter, r *http.Request, status int, name string, data any)
}

type runner interface {
	Run(ctx context.Context, p domain.Principal, dbID int64, sql string, analyze bool) (*Result, error)
}

// ResultPage adalah data template explain/result.html.
type ResultPage struct {
	DBID    int64
	SQL     string
	Analyze bool
	Error   string
	Result  *Result
}

const resultTemplate = "explain/result.html"

type Handler struct {
	svc       runner
	render    Renderer
	handleErr contract.ErrorHandler
	mw        contract.AuthMiddleware
}

func NewHandler(svc runner, render Renderer, handleErr contract.ErrorHandler, mw contract.AuthMiddleware) *Handler {
	return &Handler{svc: svc, render: render, handleErr: handleErr, mw: mw}
}

func (h *Handler) Register(mux *http.ServeMux) {
	edit := h.mw.RequireDBAccess(domain.AccessViewEdit)
	mux.Handle("POST /databases/{dbID}/explain", h.mw.Authenticate(h.mw.CSRF(edit(http.HandlerFunc(h.explain)))))
}

func (h *Handler) explain(w http.ResponseWriter, r *http.Request) {
	p, ok := domain.PrincipalFrom(r.Context())
	if !ok {
		h.handleErr(w, r, domain.ErrUnauthenticated)
		return
	}
	dbID, err := strconv.ParseInt(r.PathValue("dbID"), 10, 64)
	if err != nil || dbID <= 0 {
		h.handleErr(w, r, domain.ErrNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSQLBytes+64<<10)
	if err := r.ParseForm(); err != nil {
		h.render.Page(w, r, http.StatusRequestEntityTooLarge, resultTemplate,
			ResultPage{DBID: dbID, Error: "Permintaan terlalu besar (SQL maksimum 256 KB)."})
		return
	}
	page := ResultPage{
		DBID:    dbID,
		SQL:     r.PostFormValue("sql"),
		Analyze: checkboxOn(r.PostFormValue("analyze")),
	}

	res, err := h.svc.Run(r.Context(), p, dbID, page.SQL, page.Analyze)
	if err != nil {
		if msg := userMessage(err); msg != "" {
			page.Error = msg
			h.render.Page(w, r, http.StatusUnprocessableEntity, resultTemplate, page)
			return
		}
		h.handleErr(w, r, err)
		return
	}
	page.Result = res
	h.render.Page(w, r, http.StatusOK, resultTemplate, page)
}

func checkboxOn(v string) bool {
	switch v {
	case "on", "1", "true":
		return true
	}
	return false
}
