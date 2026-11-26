package main

import (
	"net/http"

	"dbplatform/internal/config"
	"dbplatform/internal/connmgr"
	"dbplatform/internal/explain"
	"dbplatform/internal/httpx"
	"dbplatform/internal/viewer"
)

// wireLegacyPages mendaftarkan halaman HTML yang belum punya pengganti di SPA.
// Hapus per modul begitu halaman SPA-nya selesai.
// migration dan schema diff sudah tidak punya halaman lama, jadi hanya bisa dipakai lewat SPA
func wireLegacyPages(cfg config.Config, d *httpx.Deps, mux *http.ServeMux, spaMode bool) {
	// di mode SPA semua halaman lama dimatikan
	if spaMode {
		return
	}
	connRepo := connmgr.NewRepo(d.Pool, cfg.SecretKey)
	connmgr.NewHandler(*d, connmgr.NewService(connRepo, connmgr.DefaultFactory)).Register(mux)
	viewer.NewHandler(*d, viewer.NewService(d.Authz, d.Drivers)).Register(mux)
	explain.NewHandler(explain.NewService(d.Authz, d.Drivers), d.Render, d.HandleErr, d.Auth).Register(mux)
}
