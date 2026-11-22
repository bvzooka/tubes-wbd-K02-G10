package main

import (
	"dbplatform/internal/httpx"
	"dbplatform/internal/viewer"
)

// merakit service viewer untuk API JSON, memakai authorizer dan provider driver yg sama dengan halaman HTML lama
func wireViewer(d *httpx.Deps) *viewer.APIHandler {
	return viewer.NewAPIHandler(viewer.NewService(d.Authz, d.Drivers))
}
