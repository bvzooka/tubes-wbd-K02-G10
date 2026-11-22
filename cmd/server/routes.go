package main

import (
	"dbplatform/internal/admin"
	"dbplatform/internal/apiv1"
	"dbplatform/internal/approval"
	"dbplatform/internal/auth"
	"dbplatform/internal/connmgr"
	"dbplatform/internal/viewer"
)

type apiHandlers struct {
	auth        *auth.APIHandler
	admin       *admin.APIHandler
	approvals   *approval.APIHandler
	connections *connmgr.APIHandler
	viewer      *viewer.APIHandler
}

// peta lengkap /api/v1. Nama method router adalah syarat
// aksesnya: Public, Authed, Admin, DBView, DBEdit.
func registerAPIRoutes(api *apiv1.Router, h apiHandlers) {
	if h.auth != nil {
		api.Public("GET /auth/csrf", h.auth.LoginCSRF)
		api.Public("POST /auth/login", h.auth.Login)
		api.Authed("POST /auth/logout", h.auth.Logout)
		api.Authed("GET /auth/me", h.auth.Me)
	}

	api.Admin("GET /admin/users", h.admin.ListUsers)
	api.Admin("POST /admin/users", h.admin.CreateUser)
	api.Admin("GET /admin/users/{userID}", h.admin.GetUser)
	api.Admin("PUT /admin/users/{userID}", h.admin.UpdateUser)
	api.Admin("POST /admin/users/{userID}/activate", h.admin.ActivateUser)
	api.Admin("POST /admin/users/{userID}/deactivate", h.admin.DeactivateUser)

	api.Admin("GET /admin/groups", h.admin.ListGroups)
	api.Admin("POST /admin/groups", h.admin.CreateGroup)
	api.Admin("GET /admin/groups/{groupID}", h.admin.GetGroup)
	api.Admin("PUT /admin/groups/{groupID}", h.admin.UpdateGroup)
	api.Admin("DELETE /admin/groups/{groupID}", h.admin.DeleteGroup)
	api.Admin("POST /admin/groups/{groupID}/members", h.admin.AddMember)
	api.Admin("DELETE /admin/groups/{groupID}/members/{userID}", h.admin.RemoveMember)
	api.Admin("PUT /admin/groups/{groupID}/leader", h.admin.SetLeader)
	api.Admin("PUT /admin/groups/{groupID}/access/{dbID}", h.admin.GrantAccess)
	api.Admin("DELETE /admin/groups/{groupID}/access/{dbID}", h.admin.RevokeAccess)

	api.Admin("GET /admin/connections", h.connections.List)
	api.Admin("POST /admin/connections", h.connections.Create)
	api.Admin("POST /admin/connections/test", h.connections.Test)
	api.Admin("GET /admin/connections/{dbID}", h.connections.Get)
	api.Admin("PUT /admin/connections/{dbID}", h.connections.Update)
	api.Admin("POST /admin/connections/{dbID}/activate", h.connections.Activate)
	api.Admin("POST /admin/connections/{dbID}/deactivate", h.connections.Deactivate)

	api.Authed("GET /databases", h.viewer.ListDatabases)
	api.DBView("GET /databases/{dbID}/schema", h.viewer.Schema)
	api.DBView("GET /databases/{dbID}/tables/{schema}/{table}", h.viewer.Table)
	api.DBView("GET /databases/{dbID}/tables/{schema}/{table}/data", h.viewer.Data)

	api.Authed("GET /approvals", h.approvals.Inbox)
	api.Authed("POST /executions/{execID}/approvals/{groupID}/approve", h.approvals.Approve)
	api.Authed("POST /executions/{execID}/approvals/{groupID}/reject", h.approvals.Reject)
}
