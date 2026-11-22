package main

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/config"
	"dbplatform/internal/connmgr"
)

// merakit service koneksi untuk API JSON. service yang sama dipakai halaman HTML lama di wireLegacyPages sampai halaman dihapus
func wireConnections(cfg config.Config, pool *pgxpool.Pool) *connmgr.APIHandler {
	repo := connmgr.NewRepo(pool, cfg.SecretKey)
	return connmgr.NewAPIHandler(connmgr.NewService(repo, connmgr.DefaultFactory))
}
