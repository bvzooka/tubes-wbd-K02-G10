package apiv1

import (
	"net/http"
	"strings"

	"dbplatform/internal/contract"
	"dbplatform/internal/domain"
	"dbplatform/internal/middleware"
)

const Prefix = "/api/v1"

// Handler mengembalikan error alih-alih menulisnya sendiri; Router yang menerjemahkan
// error itu menjadi respons JSON.
type Handler func(w http.ResponseWriter, r *http.Request) error

// Router mendaftarkan route /api/v1 dengan penjaga akses yang terbaca dari nama methodnya:
//
//	api.Public("POST /auth/login", h.Login)
//	api.Admin("GET /admin/users", h.ListUsers)
type Router struct {
	mux      *http.ServeMux
	auth     contract.AuthMiddleware
	writeErr contract.ErrorHandler
}

func NewRouter(mux *http.ServeMux, auth contract.AuthMiddleware, writeErr contract.ErrorHandler) *Router {
	rt := &Router{mux: mux, auth: auth, writeErr: writeErr}
	mux.Handle("/api/", rt.serve(func(http.ResponseWriter, *http.Request) error { return domain.ErrNotFound }))
	return rt
}

// Public: tanpa login.
func (rt *Router) Public(route string, h Handler) {
	rt.handle(route, h)
}

// Authed: harus login; method selain GET wajib membawa X-CSRF-Token.
func (rt *Router) Authed(route string, h Handler) {
	rt.handle(route, h, rt.auth.Authenticate, rt.auth.CSRF)
}

// Admin: Authed + Platform Admin.
func (rt *Router) Admin(route string, h Handler) {
	rt.handle(route, h, rt.auth.Authenticate, rt.auth.CSRF, rt.auth.RequireAdmin)
}

// DBView: Authed + minimal view_only pada database {dbID}.
func (rt *Router) DBView(route string, h Handler) {
	rt.handle(route, h, rt.auth.Authenticate, rt.auth.CSRF, rt.auth.RequireDBAccess(domain.AccessViewOnly))
}

// DBEdit: Authed + view_edit pada database {dbID}.
func (rt *Router) DBEdit(route string, h Handler) {
	rt.handle(route, h, rt.auth.Authenticate, rt.auth.CSRF, rt.auth.RequireDBAccess(domain.AccessViewEdit))
}

// handle mengubah "GET /auth/me" menjadi pola mux "GET /api/v1/auth/me".
func (rt *Router) handle(route string, h Handler, guards ...middleware.Middleware) {
	method, path, ok := strings.Cut(route, " ")
	if !ok {
		panic("apiv1: route harus berbentuk \"METHOD /path\": " + route)
	}
	rt.mux.Handle(method+" "+Prefix+path, middleware.Chain(guards...)(rt.serve(h)))
}

func (rt *Router) serve(h Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			rt.writeErr(w, r, err)
		}
	})
}

// IsAPIRequest dipakai error handler global untuk memilih respons JSON atau HTML.
func IsAPIRequest(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/api/")
}
