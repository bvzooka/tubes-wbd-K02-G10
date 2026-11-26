package main

import (
	"context"
	"net/http"

	"dbplatform/internal/config"
	"dbplatform/internal/connmgr"
	"dbplatform/internal/explain"
	"dbplatform/internal/httpx"
	"dbplatform/internal/migration"
	"dbplatform/internal/schemadiff"
	"dbplatform/internal/viewer"
)

// wireLegacyPages mendaftarkan halaman HTML yang belum punya pengganti di SPA.
// Hapus per modul begitu halaman SPA-nya selesai.
func wireLegacyPages(ctx context.Context, cfg config.Config, d *httpx.Deps, mux *http.ServeMux, spaMode bool) error {
	// di mode SPA semua halaman lama dimatikan, termasuk eksekusi migration M1 yang tidak lewat persetujuan
	if spaMode {
		return nil
	}
	connRepo := connmgr.NewRepo(d.Pool, cfg.SecretKey)
	connmgr.NewHandler(*d, connmgr.NewService(connRepo, connmgr.DefaultFactory)).Register(mux)
	viewer.NewHandler(*d, viewer.NewService(d.Authz, d.Drivers)).Register(mux)
	explain.NewHandler(explain.NewService(d.Authz, d.Drivers), d.Render, d.HandleErr, d.Auth).Register(mux)

	migrationSvc := migration.NewService(migration.NewRepo(d.Pool), d.Authz, d.Drivers, d.Clock)
	recovered, err := migrationSvc.RecoverStuck(ctx)
	if err != nil {
		return err
	}
	if recovered > 0 {
		d.Logger.Warn("migration yang terputus ditandai gagal", "jumlah", recovered)
	}
	migration.NewHandler(*d, migrationSvc).Register(mux)
	schemadiff.NewHandler(*d, schemadiff.NewService(d.Authz, d.Drivers, migrationSvc)).Register(mux)
	return nil
}
