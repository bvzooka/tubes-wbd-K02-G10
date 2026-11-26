package postgres

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/dbdriver"
)

var _ dbdriver.PoolStatter = (*Driver)(nil)

// melaporkan statistik pool koneksi ke database target ini
func (d *Driver) PoolStats() dbdriver.PoolStats { return StatsOf(d.pool) }

// membaca statistik pgxpool. Dipakai juga untuk pool database platform (appdb)
func StatsOf(pool *pgxpool.Pool) dbdriver.PoolStats {
	s := pool.Stat()
	return dbdriver.PoolStats{
		MaxOpen:      int(s.MaxConns()),
		InUse:        int(s.AcquiredConns()),
		Idle:         int(s.IdleConns()),
		WaitCount:    s.EmptyAcquireCount(),
		WaitDuration: s.EmptyAcquireWaitTime(),
	}
}
