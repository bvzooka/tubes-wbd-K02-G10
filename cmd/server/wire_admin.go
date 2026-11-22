package main

import (
	"net/http"

	"dbplatform/internal/admin"
	"dbplatform/internal/httpx"
)

func wireAdmin(d *httpx.Deps, a authBundle, mux *http.ServeMux, spaMode bool) *admin.APIHandler {
	users := admin.NewService(admin.NewUserRepo(d.Pool), a.hasher, a.sessions)
	groups := admin.NewGroupService(admin.NewGroupRepo(d.Pool), a.sessions)
	if !spaMode {
		admin.NewUserHandler(*d, users).Register(mux)
		admin.NewGroupHandler(*d, groups).Register(mux)
	}
	return admin.NewAPIHandler(users, groups)
}
