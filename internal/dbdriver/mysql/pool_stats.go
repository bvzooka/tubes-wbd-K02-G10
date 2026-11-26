package mysql

import (
	"database/sql"

	"dbplatform/internal/dbdriver"
)

var _ dbdriver.PoolStatter = (*Driver)(nil)

// menjumlahkan pool baca (ro) dan pool tulis (rw) bila rw sudah dibuka
func (d *Driver) PoolStats() dbdriver.PoolStats {
	d.mu.Lock()
	defer d.mu.Unlock()

	stats := statsOf(d.ro)
	if d.rw != nil {
		stats = stats.Add(statsOf(d.rw))
	}
	return stats
}

func statsOf(db *sql.DB) dbdriver.PoolStats {
	s := db.Stats()
	return dbdriver.PoolStats{
		MaxOpen:      s.MaxOpenConnections,
		InUse:        s.InUse,
		Idle:         s.Idle,
		WaitCount:    s.WaitCount,
		WaitDuration: s.WaitDuration,
	}
}
