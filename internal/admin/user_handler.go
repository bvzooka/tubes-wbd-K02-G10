package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"dbplatform/internal/domain"
	"dbplatform/internal/httpx"
)

const (
	usersTemplate    = "admin/users.html"
	userFormTemplate = "admin/user_form.html"

	maxFormBytes = 64 << 10
)

type userService interface {
	CreateUser(ctx context.Context, actor domain.Principal, in UserInput) (domain.User, error)
	ListUsers(ctx context.Context, actor domain.Principal) ([]domain.User, error)
}

type UsersPage struct {
	Users []domain.User
}

type UserFormPage struct {
	Username        string
	IsPlatformAdmin bool
	Errors          map[string]string
}

type UserHandler struct {
	deps httpx.Deps
	svc  userService
}

func NewUserHandler(d httpx.Deps, svc userService) *UserHandler {
	return &UserHandler{deps: d, svc: svc}
}

func (h *UserHandler) Register(mux *http.ServeMux) {
	admin := h.deps.Admin()
	mux.Handle("GET /admin/users", admin(http.HandlerFunc(h.list)))
	mux.Handle("GET /admin/users/new", admin(http.HandlerFunc(h.newForm)))
	mux.Handle("POST /admin/users", admin(http.HandlerFunc(h.create)))
}

func (h *UserHandler) list(w http.ResponseWriter, r *http.Request) {
	actor, ok := domain.PrincipalFrom(r.Context())
	if !ok {
		h.deps.HandleErr(w, r, domain.ErrUnauthenticated)
		return
	}
	users, err := h.svc.ListUsers(r.Context(), actor)
	if err != nil {
		h.deps.HandleErr(w, r, err)
		return
	}
	h.deps.Render.Page(w, r, http.StatusOK, usersTemplate, UsersPage{Users: users})
}

func (h *UserHandler) newForm(w http.ResponseWriter, r *http.Request) {
	h.deps.Render.Page(w, r, http.StatusOK, userFormTemplate, UserFormPage{})
}

func (h *UserHandler) create(w http.ResponseWriter, r *http.Request) {
	actor, ok := domain.PrincipalFrom(r.Context())
	if !ok {
		h.deps.HandleErr(w, r, domain.ErrUnauthenticated)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		h.deps.Render.Page(w, r, http.StatusBadRequest, userFormTemplate, UserFormPage{
			Errors: map[string]string{"form": "Formulir tidak dapat dibaca. Silakan coba lagi."},
		})
		return
	}

	in := UserInput{
		Username:        r.PostFormValue("username"),
		Password:        r.PostFormValue("password"),
		IsPlatformAdmin: r.PostFormValue("is_platform_admin") == "on",
	}
	u, err := h.svc.CreateUser(r.Context(), actor, in)
	if err != nil {
		var verr *domain.ValidationError
		if errors.As(err, &verr) {
			h.deps.Render.Page(w, r, http.StatusUnprocessableEntity, userFormTemplate, UserFormPage{
				Username:        NormalizeUsername(in.Username),
				IsPlatformAdmin: in.IsPlatformAdmin,
				Errors:          verr.Fields,
			})
			return
		}
		h.deps.HandleErr(w, r, err)
		return
	}

	h.deps.Flash.Set(w, httpx.FlashSuccess, fmt.Sprintf("User %q berhasil dibuat.", u.Username))
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}
