package main

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/config"
	"dbplatform/internal/connmgr"
	"dbplatform/internal/dbdriver/obs"
)

// merakit service koneksi untuk API JSON. service yang sama dipakai halaman HTML lama di wireLegacyPages sampai halaman dihapus
func wireConnections(cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger) *connmgr.APIHandler {
	repo := connmgr.NewRepo(pool, cfg.SecretKey)
	return connmgr.NewAPIHandler(connmgr.NewService(repo, observedFactory(logger)))
}

// membuat tes koneksi tercatat di log dan trace seperti SQL lain
func observedFactory(logger *slog.Logger) connmgr.DriverFactory {
	return obs.WrapFactory(connmgr.DefaultFactory, logger)
}
