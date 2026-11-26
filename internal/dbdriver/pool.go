package dbdriver

import "time"

// pool koneksi pada satu saat, WaitCount dan WaitDuration bersifat kumulatif sejak pool dibuka
type PoolStats struct {
	MaxOpen int
	InUse int
	Idle int
	WaitCount int64
	WaitDuration time.Duration
}

// menjumlahkan dua potret, dipakai utk menggabungkan beberapa pool
func (s PoolStats) Add(o PoolStats) PoolStats {
	return PoolStats{
		MaxOpen:      s.MaxOpen + o.MaxOpen,
		InUse:        s.InUse + o.InUse,
		Idle:         s.Idle + o.Idle,
		WaitCount:    s.WaitCount + o.WaitCount,
		WaitDuration: s.WaitDuration + o.WaitDuration,
	}
}

// dipenuhi driver yang bisa melaporkan statistik pool koneksinya
type PoolStatter interface {
	PoolStats() PoolStats
}
