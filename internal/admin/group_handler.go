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
	groupsTemplate      = "admin/groups.html"
	groupFormTemplate   = "admin/group_form.html"
	groupDetailTemplate = "admin/group_detail.html"

	msgGroupNotEmpty  = "Group tidak dapat dihapus selagi masih memiliki anggota atau akses database. Kosongkan dulu."
	msgFormUnreadable = "Formulir tidak dapat dibaca. Silakan coba lagi."
)

type groupService interface {
	CreateGroup(ctx context.Context, actor domain.Principal, in GroupInput) (domain.Group, error)
	ListGroups(ctx context.Context, actor domain.Principal) ([]GroupRow, error)
	GroupDetail(ctx context.Context, actor domain.Principal, id int64) (GroupDetail, error)
	UpdateGroup(ctx context.Context, actor domain.Principal, id int64, in GroupInput) (domain.Group, error)
	DeleteGroup(ctx context.Context, actor domain.Principal, id int64) error
	AddMember(ctx context.Context, actor domain.Principal, groupID, userID int64) error
	RemoveMember(ctx context.Context, actor domain.Principal, groupID, userID int64) error
	GrantAccess(ctx context.Context, actor domain.Principal, groupID, connID int64, level domain.AccessLevel) error
	RevokeAccess(ctx context.Context, actor domain.Principal, groupID, connID int64) error
}

type GroupsPage struct {
	Groups []GroupRow
}

type GroupFormPage struct {
	Name        string
	Description string
	Errors      map[string]string
}

type GroupDetailPage struct {
	Group                domain.Group
	Name                 string
	Description          string
	Members              []GroupMember
	Accesses             []GroupAccess
	AvailableUsers       []UserOption
	AvailableConnections []ConnOption
	CanDelete            bool
	Errors               map[string]string
}

type GroupHandler struct {
	deps httpx.Deps
	svc  groupService
}

func NewGroupHandler(d httpx.Deps, svc groupService) *GroupHandler {
	return &GroupHandler{deps: d, svc: svc}
}

func (h *GroupHandler) Register(mux *http.ServeMux) {
	admin := h.deps.Admin()
	mux.Handle("GET /admin/groups", admin(http.HandlerFunc(h.list)))
	mux.Handle("GET /admin/groups/new", admin(http.HandlerFunc(h.newForm)))
	mux.Handle("POST /admin/groups", admin(http.HandlerFunc(h.create)))
	mux.Handle("GET /admin/groups/{groupID}", admin(http.HandlerFunc(h.detail)))
	mux.Handle("POST /admin/groups/{groupID}", admin(http.HandlerFunc(h.update)))
	mux.Handle("POST /admin/groups/{groupID}/delete", admin(http.HandlerFunc(h.delete)))
	mux.Handle("POST /admin/groups/{groupID}/members", admin(http.HandlerFunc(h.addMember)))
	mux.Handle("POST /admin/groups/{groupID}/members/{userID}/delete", admin(http.HandlerFunc(h.removeMember)))
	mux.Handle("POST /admin/groups/{groupID}/access", admin(http.HandlerFunc(h.grantAccess)))
	mux.Handle("POST /admin/groups/{groupID}/access/{dbID}/delete", admin(http.HandlerFunc(h.revokeAccess)))
}

func (h *GroupHandler) actor(w http.ResponseWriter, r *http.Request) (domain.Principal, bool) {
	p, ok := domain.PrincipalFrom(r.Context())
	if !ok {
		h.deps.HandleErr(w, r, domain.ErrUnauthenticated)
	}
	return p, ok
}

func (h *GroupHandler) pathID(w http.ResponseWriter, r *http.Request, key string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(key), 10, 64)
	if err != nil || id <= 0 {
		h.deps.HandleErr(w, r, domain.ErrNotFound)
		return 0, false
	}
	return id, true
}

func (h *GroupHandler) parseForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, msgFormUnreadable, http.StatusBadRequest)
		return false
	}
	return true
}

func formID(v string) (int64, bool) {
	id, err := strconv.ParseInt(v, 10, 64)
	return id, err == nil && id > 0
}

func (h *GroupHandler) redirectDetail(w http.ResponseWriter, r *http.Request, id int64) {
	http.Redirect(w, r, fmt.Sprintf("/admin/groups/%d", id), http.StatusSeeOther)
}

func (h *GroupHandler) list(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	groups, err := h.svc.ListGroups(r.Context(), actor)
	if err != nil {
		h.deps.HandleErr(w, r, err)
		return
	}
	h.deps.Render.Page(w, r, http.StatusOK, groupsTemplate, GroupsPage{Groups: groups})
}

func (h *GroupHandler) newForm(w http.ResponseWriter, r *http.Request) {
	h.deps.Render.Page(w, r, http.StatusOK, groupFormTemplate, GroupFormPage{})
}

func (h *GroupHandler) create(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok || !h.parseForm(w, r) {
		return
	}
	in := GroupInput{Name: r.PostFormValue("name"), Description: r.PostFormValue("description")}
	g, err := h.svc.CreateGroup(r.Context(), actor, in)
	if err != nil {
		var verr *domain.ValidationError
		if errors.As(err, &verr) {
			h.deps.Render.Page(w, r, http.StatusUnprocessableEntity, groupFormTemplate, GroupFormPage{
				Name: in.Name, Description: in.Description, Errors: verr.Fields,
			})
			return
		}
		h.deps.HandleErr(w, r, err)
		return
	}
	h.deps.Flash.Set(w, httpx.FlashSuccess, fmt.Sprintf("Group %q berhasil dibuat.", g.Name))
	h.redirectDetail(w, r, g.ID)
}

func (h *GroupHandler) renderDetail(w http.ResponseWriter, r *http.Request, actor domain.Principal, id int64, status int, errs map[string]string, form *GroupInput) {
	d, err := h.svc.GroupDetail(r.Context(), actor, id)
	if err != nil {
		h.deps.HandleErr(w, r, err)
		return
	}
	page := GroupDetailPage{
		Group:                d.Group,
		Name:                 d.Group.Name,
		Description:          d.Group.Description,
		Members:              d.Members,
		Accesses:             d.Accesses,
		AvailableUsers:       d.AvailableUsers,
		AvailableConnections: d.AvailableConnections,
		CanDelete:            d.CanDelete,
		Errors:               errs,
	}
	if form != nil {
		page.Name, page.Description = form.Name, form.Description
	}
	h.deps.Render.Page(w, r, status, groupDetailTemplate, page)
}

func (h *GroupHandler) detail(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "groupID")
	if !ok {
		return
	}
	h.renderDetail(w, r, actor, id, http.StatusOK, nil, nil)
}

func (h *GroupHandler) update(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "groupID")
	if !ok || !h.parseForm(w, r) {
		return
	}
	in := GroupInput{Name: r.PostFormValue("name"), Description: r.PostFormValue("description")}
	g, err := h.svc.UpdateGroup(r.Context(), actor, id, in)
	if err != nil {
		var verr *domain.ValidationError
		if errors.As(err, &verr) {
			h.renderDetail(w, r, actor, id, http.StatusUnprocessableEntity, verr.Fields, &in)
			return
		}
		h.deps.HandleErr(w, r, err)
		return
	}
	h.deps.Flash.Set(w, httpx.FlashSuccess, fmt.Sprintf("Group %q berhasil diperbarui.", g.Name))
	h.redirectDetail(w, r, id)
}

func (h *GroupHandler) delete(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "groupID")
	if !ok {
		return
	}
	err := h.svc.DeleteGroup(r.Context(), actor, id)
	switch {
	case err == nil:
		h.deps.Flash.Set(w, httpx.FlashSuccess, "Group berhasil dihapus.")
		http.Redirect(w, r, "/admin/groups", http.StatusSeeOther)
	case errors.Is(err, ErrGroupNotEmpty):
		h.deps.Flash.Set(w, httpx.FlashError, msgGroupNotEmpty)
		h.redirectDetail(w, r, id)
	default:
		h.deps.HandleErr(w, r, err)
	}
}

func (h *GroupHandler) addMember(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "groupID")
	if !ok || !h.parseForm(w, r) {
		return
	}
	userID, ok := formID(r.PostFormValue("user_id"))
	if !ok {
		h.renderDetail(w, r, actor, id, http.StatusUnprocessableEntity, map[string]string{"user_id": msgUserMissing}, nil)
		return
	}
	if err := h.svc.AddMember(r.Context(), actor, id, userID); err != nil {
		var verr *domain.ValidationError
		if errors.As(err, &verr) {
			h.renderDetail(w, r, actor, id, http.StatusUnprocessableEntity, verr.Fields, nil)
			return
		}
		h.deps.HandleErr(w, r, err)
		return
	}
	h.deps.Flash.Set(w, httpx.FlashSuccess, "User ditambahkan ke group.")
	h.redirectDetail(w, r, id)
}

func (h *GroupHandler) removeMember(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "groupID")
	if !ok {
		return
	}
	userID, ok := h.pathID(w, r, "userID")
	if !ok {
		return
	}
	if err := h.svc.RemoveMember(r.Context(), actor, id, userID); err != nil {
		h.deps.HandleErr(w, r, err)
		return
	}
	h.deps.Flash.Set(w, httpx.FlashSuccess, "User dikeluarkan dari group.")
	h.redirectDetail(w, r, id)
}

func (h *GroupHandler) grantAccess(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "groupID")
	if !ok || !h.parseForm(w, r) {
		return
	}
	connID, ok := formID(r.PostFormValue("connection_id"))
	if !ok {
		h.renderDetail(w, r, actor, id, http.StatusUnprocessableEntity, map[string]string{"connection_id": msgConnectionMissing}, nil)
		return
	}
	level := domain.AccessLevel(r.PostFormValue("access_level"))
	if err := h.svc.GrantAccess(r.Context(), actor, id, connID, level); err != nil {
		var verr *domain.ValidationError
		if errors.As(err, &verr) {
			h.renderDetail(w, r, actor, id, http.StatusUnprocessableEntity, verr.Fields, nil)
			return
		}
		h.deps.HandleErr(w, r, err)
		return
	}
	h.deps.Flash.Set(w, httpx.FlashSuccess, "Akses database disimpan.")
	h.redirectDetail(w, r, id)
}

func (h *GroupHandler) revokeAccess(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.actor(w, r)
	if !ok {
		return
	}
	id, ok := h.pathID(w, r, "groupID")
	if !ok {
		return
	}
	connID, ok := h.pathID(w, r, "dbID")
	if !ok {
		return
	}
	if err := h.svc.RevokeAccess(r.Context(), actor, id, connID); err != nil {
		h.deps.HandleErr(w, r, err)
		return
	}
	h.deps.Flash.Set(w, httpx.FlashSuccess, "Akses database dicabut.")
	h.redirectDetail(w, r, id)
}
