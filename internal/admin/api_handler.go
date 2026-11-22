package admin

import (
	"context"
	"net/http"
	"time"

	"dbplatform/internal/apiv1"
	"dbplatform/internal/domain"
)

// APIHandler melayani /api/v1/admin/users dan /api/v1/admin/groups.
type APIHandler struct {
	users  *Service
	groups *GroupService
}

func NewAPIHandler(users *Service, groups *GroupService) *APIHandler {
	return &APIHandler{users: users, groups: groups}
}

type userJSON struct {
	ID              int64     `json:"id"`
	Username        string    `json:"username"`
	IsPlatformAdmin bool      `json:"is_platform_admin"`
	IsActive        bool      `json:"is_active"`
	CreatedAt       time.Time `json:"created_at"`
}

type groupJSON struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	LeaderID    *int64    `json:"leader_id"`
	LeaderName  string    `json:"leader_name,omitempty"`
	MemberCount int       `json:"member_count"`
	AccessCount int       `json:"access_count"`
	CreatedAt   time.Time `json:"created_at"`
}

type memberJSON struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	IsActive bool   `json:"is_active"`
	IsLeader bool   `json:"is_leader"`
}

type accessJSON struct {
	ConnectionID   int64              `json:"connection_id"`
	ConnectionName string             `json:"connection_name"`
	Level          domain.AccessLevel `json:"access_level"`
}

type optionJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type groupDetailJSON struct {
	groupJSON
	Members              []memberJSON `json:"members"`
	Accesses             []accessJSON `json:"accesses"`
	AvailableUsers       []optionJSON `json:"available_users"`
	AvailableConnections []optionJSON `json:"available_connections"`
	CanDelete            bool         `json:"can_delete"`
}

// ---- Users ----

func (h *APIHandler) ListUsers(w http.ResponseWriter, r *http.Request) error {
	users, err := h.users.ListUsers(r.Context(), apiv1.Actor(r))
	if err != nil {
		return err
	}
	out := make([]userJSON, 0, len(users))
	for _, u := range users {
		out = append(out, toUserJSON(u))
	}
	return apiv1.OK(w, out)
}

func (h *APIHandler) CreateUser(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Username        string `json:"username"`
		Password        string `json:"password"`
		IsPlatformAdmin bool   `json:"is_platform_admin"`
	}
	if err := apiv1.Decode(w, r, &in); err != nil {
		return err
	}
	u, err := h.users.CreateUser(r.Context(), apiv1.Actor(r), UserInput(in))
	if err != nil {
		return err
	}
	return apiv1.Created(w, toUserJSON(u))
}

func (h *APIHandler) GetUser(w http.ResponseWriter, r *http.Request) error {
	id, err := apiv1.PathID(r, "userID")
	if err != nil {
		return err
	}
	u, err := h.users.GetUser(r.Context(), apiv1.Actor(r), id)
	if err != nil {
		return err
	}
	return apiv1.OK(w, toUserJSON(u))
}

func (h *APIHandler) UpdateUser(w http.ResponseWriter, r *http.Request) error {
	id, err := apiv1.PathID(r, "userID")
	if err != nil {
		return err
	}
	var in struct {
		Password        string `json:"password"` // kosong = tidak diubah
		IsPlatformAdmin bool   `json:"is_platform_admin"`
	}
	if err := apiv1.Decode(w, r, &in); err != nil {
		return err
	}
	u, err := h.users.UpdateUser(r.Context(), apiv1.Actor(r), id, UserUpdate(in))
	if err != nil {
		return err
	}
	return apiv1.OK(w, toUserJSON(u))
}

func (h *APIHandler) ActivateUser(w http.ResponseWriter, r *http.Request) error {
	return h.setUserActive(w, r, h.users.ActivateUser)
}

func (h *APIHandler) DeactivateUser(w http.ResponseWriter, r *http.Request) error {
	return h.setUserActive(w, r, h.users.DeactivateUser)
}

func (h *APIHandler) setUserActive(w http.ResponseWriter, r *http.Request, op func(context.Context, domain.Principal, int64) (domain.User, error)) error {
	id, err := apiv1.PathID(r, "userID")
	if err != nil {
		return err
	}
	u, err := op(r.Context(), apiv1.Actor(r), id)
	if err != nil {
		return err
	}
	return apiv1.OK(w, toUserJSON(u))
}

// ---- Groups ----

func (h *APIHandler) ListGroups(w http.ResponseWriter, r *http.Request) error {
	rows, err := h.groups.ListGroups(r.Context(), apiv1.Actor(r))
	if err != nil {
		return err
	}
	out := make([]groupJSON, 0, len(rows))
	for _, g := range rows {
		out = append(out, groupJSON{
			ID: g.ID, Name: g.Name, Description: g.Description, LeaderName: g.LeaderName,
			MemberCount: g.MemberCount, AccessCount: g.AccessCount, CreatedAt: g.CreatedAt,
		})
	}
	return apiv1.OK(w, out)
}

func (h *APIHandler) CreateGroup(w http.ResponseWriter, r *http.Request) error {
	var in GroupInput
	if err := decodeGroupInput(w, r, &in); err != nil {
		return err
	}
	g, err := h.groups.CreateGroup(r.Context(), apiv1.Actor(r), in)
	if err != nil {
		return err
	}
	return apiv1.Created(w, toGroupJSON(g))
}

func (h *APIHandler) GetGroup(w http.ResponseWriter, r *http.Request) error {
	id, err := apiv1.PathID(r, "groupID")
	if err != nil {
		return err
	}
	d, err := h.groups.GroupDetail(r.Context(), apiv1.Actor(r), id)
	if err != nil {
		return err
	}
	return apiv1.OK(w, toGroupDetailJSON(d))
}

func (h *APIHandler) UpdateGroup(w http.ResponseWriter, r *http.Request) error {
	id, err := apiv1.PathID(r, "groupID")
	if err != nil {
		return err
	}
	var in GroupInput
	if err := decodeGroupInput(w, r, &in); err != nil {
		return err
	}
	g, err := h.groups.UpdateGroup(r.Context(), apiv1.Actor(r), id, in)
	if err != nil {
		return err
	}
	return apiv1.OK(w, toGroupJSON(g))
}

func (h *APIHandler) DeleteGroup(w http.ResponseWriter, r *http.Request) error {
	id, err := apiv1.PathID(r, "groupID")
	if err != nil {
		return err
	}
	if err := h.groups.DeleteGroup(r.Context(), apiv1.Actor(r), id); err != nil {
		return err
	}
	return apiv1.NoContent(w)
}

func (h *APIHandler) AddMember(w http.ResponseWriter, r *http.Request) error {
	groupID, err := apiv1.PathID(r, "groupID")
	if err != nil {
		return err
	}
	var in struct {
		UserID int64 `json:"user_id"`
	}
	if err := apiv1.Decode(w, r, &in); err != nil {
		return err
	}
	if err := h.groups.AddMember(r.Context(), apiv1.Actor(r), groupID, in.UserID); err != nil {
		return err
	}
	return apiv1.NoContent(w)
}

func (h *APIHandler) RemoveMember(w http.ResponseWriter, r *http.Request) error {
	groupID, err := apiv1.PathID(r, "groupID")
	if err != nil {
		return err
	}
	userID, err := apiv1.PathID(r, "userID")
	if err != nil {
		return err
	}
	if err := h.groups.RemoveMember(r.Context(), apiv1.Actor(r), groupID, userID); err != nil {
		return err
	}
	return apiv1.NoContent(w)
}

func (h *APIHandler) SetLeader(w http.ResponseWriter, r *http.Request) error {
	groupID, err := apiv1.PathID(r, "groupID")
	if err != nil {
		return err
	}
	var in struct {
		UserID int64 `json:"user_id"`
	}
	if err := apiv1.Decode(w, r, &in); err != nil {
		return err
	}
	if err := h.groups.SetLeader(r.Context(), apiv1.Actor(r), groupID, in.UserID); err != nil {
		return err
	}
	return apiv1.NoContent(w)
}

func (h *APIHandler) GrantAccess(w http.ResponseWriter, r *http.Request) error {
	groupID, err := apiv1.PathID(r, "groupID")
	if err != nil {
		return err
	}
	dbID, err := apiv1.PathID(r, "dbID")
	if err != nil {
		return err
	}
	var in struct {
		Level domain.AccessLevel `json:"access_level"`
	}
	if err := apiv1.Decode(w, r, &in); err != nil {
		return err
	}
	if err := h.groups.GrantAccess(r.Context(), apiv1.Actor(r), groupID, dbID, in.Level); err != nil {
		return err
	}
	return apiv1.NoContent(w)
}

func (h *APIHandler) RevokeAccess(w http.ResponseWriter, r *http.Request) error {
	groupID, err := apiv1.PathID(r, "groupID")
	if err != nil {
		return err
	}
	dbID, err := apiv1.PathID(r, "dbID")
	if err != nil {
		return err
	}
	if err := h.groups.RevokeAccess(r.Context(), apiv1.Actor(r), groupID, dbID); err != nil {
		return err
	}
	return apiv1.NoContent(w)
}

// ---- Konversi ----

func decodeGroupInput(w http.ResponseWriter, r *http.Request, in *GroupInput) error {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := apiv1.Decode(w, r, &body); err != nil {
		return err
	}
	*in = GroupInput(body)
	return nil
}

func toUserJSON(u domain.User) userJSON {
	return userJSON{ID: u.ID, Username: u.Username, IsPlatformAdmin: u.IsPlatformAdmin, IsActive: u.IsActive, CreatedAt: u.CreatedAt}
}

func toGroupJSON(g domain.Group) groupJSON {
	return groupJSON{ID: g.ID, Name: g.Name, Description: g.Description, LeaderID: g.LeaderID, CreatedAt: g.CreatedAt}
}

func toGroupDetailJSON(d GroupDetail) groupDetailJSON {
	out := groupDetailJSON{
		groupJSON:            toGroupJSON(d.Group),
		Members:              []memberJSON{},
		Accesses:             []accessJSON{},
		AvailableUsers:       []optionJSON{},
		AvailableConnections: []optionJSON{},
		CanDelete:            d.CanDelete,
	}
	for _, m := range d.Members {
		isLeader := d.Group.LeaderID != nil && *d.Group.LeaderID == m.UserID
		if isLeader {
			out.LeaderName = m.Username
		}
		out.Members = append(out.Members, memberJSON{UserID: m.UserID, Username: m.Username, IsActive: m.IsActive, IsLeader: isLeader})
	}
	for _, a := range d.Accesses {
		out.Accesses = append(out.Accesses, accessJSON(a))
	}
	for _, u := range d.AvailableUsers {
		out.AvailableUsers = append(out.AvailableUsers, optionJSON{ID: u.ID, Name: u.Username})
	}
	for _, c := range d.AvailableConnections {
		out.AvailableConnections = append(out.AvailableConnections, optionJSON(c))
	}
	out.MemberCount, out.AccessCount = len(d.Members), len(d.Accesses)
	return out
}
