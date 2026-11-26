package main

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/config"
	"dbplatform/internal/connmgr"
	"dbplatform/internal/contract"
	"dbplatform/internal/dbdriver/obs"
)

type driverBundle struct {
	Provider  contract.DriverProvider
	PoolStats obs.PoolSource
	Close     func()
}

// wireDrivers membungkus provider koneksi dengan decorator obs agar semua SQL ke
// database target tercatat di log dan trace.
func wireDrivers(cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger) driverBundle {
	cached := connmgr.NewProvider(connmgr.NewRepo(pool, cfg.SecretKey), connmgr.DefaultFactory)
	return driverBundle{
		Provider:  obs.WrapProvider(cached, obs.NamesFromDB(pool), logger),
		PoolStats: cached.PoolStats,
		Close:     cached.Close,
	}
}
