package admin

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
	usersTemplate    = "admin/users.html"
	userFormTemplate = "admin/user_form.html"
	userEditTemplate = "admin/user_edit.html"

	maxFormBytes = 64 << 10

	msgSelfDemote     = "Anda tidak dapat mencabut peran admin dari diri sendiri."
	msgSelfDeactivate = "Anda tidak dapat menonaktifkan akun Anda sendiri."
	msgLastAdmin      = "Tidak dapat dilakukan: harus tersisa minimal satu Platform Admin aktif."
)

type userService interface {
	CreateUser(ctx context.Context, actor domain.Principal, in UserInput) (domain.User, error)
	ListUsers(ctx context.Context, actor domain.Principal) ([]domain.User, error)
	GetUser(ctx context.Context, actor domain.Principal, id int64) (domain.User, error)
	UpdateUser(ctx context.Context, actor domain.Principal, id int64, in UserUpdate) (domain.User, error)
	DeactivateUser(ctx context.Context, actor domain.Principal, id int64) (domain.User, error)
	ActivateUser(ctx context.Context, actor domain.Principal, id int64) (domain.User, error)
}

type UsersPage struct {
	Users         []domain.User
	CurrentUserID int64
}

type UserFormPage struct {
	Username        string
	IsPlatformAdmin bool
	Errors          map[string]string
}

type UserEditPage struct {
	User            domain.User
	IsSelf          bool
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

// Semua route dibungkus Admin()
func (h *UserHandler) Register(mux *http.ServeMux) {
	admin := h.deps.Admin()
	mux.Handle("GET /admin/users", admin(http.HandlerFunc(h.list)))
	mux.Handle("GET /admin/users/new", admin(http.HandlerFunc(h.newForm)))
	mux.Handle("POST /admin/users", admin(http.HandlerFunc(h.create)))
	mux.Handle("GET /admin/users/{userID}/edit", admin(http.HandlerFunc(h.editForm)))
	mux.Handle("POST /admin/users/{userID}", admin(http.HandlerFunc(h.update)))
	mux.Handle("POST /admin/users/{userID}/deactivate", admin(http.HandlerFunc(h.deactivate)))
	mux.Handle("POST /admin/users/{userID}/activate", admin(http.HandlerFunc(h.activate)))
}

func (h *UserHandler) actor(w http.ResponseWriter, r *http.Request) (domain.Principal, bool) {
	p, ok := domain.PrincipalFrom(r.Context())
	if !ok {
		h.deps.HandleErr(w, r, domain.ErrUnauthenticated)
	}
	return p, ok
}

func (h *UserHandler) userID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil || id <= 0 {
		h.deps.HandleErr(w, r, domain.ErrNotFound)
		return 0, false
	}
	return id, true
}

func (h *UserHandler) list(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	users, err := h.svc.ListUsers(r.Context(), actor)
	if err != nil {
		h.deps.HandleErr(w, r, err)
		return
	}
	h.deps.Render.Page(w, r, http.StatusOK, usersTemplate, UsersPage{Users: users, CurrentUserID: actor.UserID})
}

func (h *UserHandler) newForm(w http.ResponseWriter, r *http.Request) {
	h.deps.Render.Page(w, r, http.StatusOK, userFormTemplate, UserFormPage{})
}

func (h *UserHandler) create(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
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
	// Redirest setelah POST biar refresh ga kirim ulang form
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (h *UserHandler) editForm(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := h.userID(w, r)
	if !ok {
		return
	}
	u, err := h.svc.GetUser(r.Context(), actor, id)
	if err != nil {
		h.deps.HandleErr(w, r, err)
		return
	}
	h.deps.Render.Page(w, r, http.StatusOK, userEditTemplate, UserEditPage{
		User:            u,
		IsSelf:          u.ID == actor.UserID,
		IsPlatformAdmin: u.IsPlatformAdmin,
	})
}

func (h *UserHandler) update(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := h.userID(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		h.renderEdit(w, r, actor, id, http.StatusBadRequest, false, map[string]string{"form": "Formulir tidak dapat dibaca. Silakan coba lagi."})
		return
	}

	in := UserUpdate{
		Password:        r.PostFormValue("password"),
		IsPlatformAdmin: r.PostFormValue("is_platform_admin") == "on",
	}
	u, err := h.svc.UpdateUser(r.Context(), actor, id, in)
	if err != nil {
		var verr *domain.ValidationError
		switch {
		case errors.As(err, &verr):
			h.renderEdit(w, r, actor, id, http.StatusUnprocessableEntity, in.IsPlatformAdmin, verr.Fields)
		case errors.Is(err, ErrSelfDemote):
			h.renderEdit(w, r, actor, id, http.StatusUnprocessableEntity, in.IsPlatformAdmin, map[string]string{"is_platform_admin": msgSelfDemote})
		case errors.Is(err, ErrLastAdmin):
			h.renderEdit(w, r, actor, id, http.StatusUnprocessableEntity, in.IsPlatformAdmin, map[string]string{"is_platform_admin": msgLastAdmin})
		default:
			h.deps.HandleErr(w, r, err)
		}
		return
	}

	msg := fmt.Sprintf("User %q berhasil diperbarui.", u.Username)
	if in.Password != "" {
		msg += " Password diganti dan semua sesi user ini diakhiri."
	}
	h.deps.Flash.Set(w, httpx.FlashSuccess, msg)
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (h *UserHandler) renderEdit(w http.ResponseWriter, r *http.Request, actor domain.Principal, id int64, status int, isAdmin bool, errs map[string]string) {
	u, err := h.svc.GetUser(r.Context(), actor, id)
	if err != nil {
		h.deps.HandleErr(w, r, err)
		return
	}
	h.deps.Render.Page(w, r, status, userEditTemplate, UserEditPage{
		User:            u,
		IsSelf:          u.ID == actor.UserID,
		IsPlatformAdmin: isAdmin,
		Errors:          errs,
	})
}

func (h *UserHandler) deactivate(w http.ResponseWriter, r *http.Request) {
	h.toggle(w, r, h.svc.DeactivateUser, "User %q dinonaktifkan dan semua sesinya diakhiri.")
}

func (h *UserHandler) activate(w http.ResponseWriter, r *http.Request) {
	h.toggle(w, r, h.svc.ActivateUser, "User %q diaktifkan kembali.")
}

func (h *UserHandler) toggle(w http.ResponseWriter, r *http.Request, op func(context.Context, domain.Principal, int64) (domain.User, error), okFormat string) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := h.userID(w, r)
	if !ok {
		return
	}
	u, err := op(r.Context(), actor, id)
	switch {
	case err == nil:
		h.deps.Flash.Set(w, httpx.FlashSuccess, fmt.Sprintf(okFormat, u.Username))
	case errors.Is(err, ErrSelfDeactivate):
		h.deps.Flash.Set(w, httpx.FlashError, msgSelfDeactivate)
	case errors.Is(err, ErrLastAdmin):
		h.deps.Flash.Set(w, httpx.FlashError, msgLastAdmin)
	default:
		h.deps.HandleErr(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}
