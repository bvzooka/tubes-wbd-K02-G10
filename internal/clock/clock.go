package clock

import (
	"sync"
	"time"
)

// Clock membungkus waktu supaya kode yang bergantung pada waktu bisa dites dengan waktu palsu
type Clock interface {
	Now() time.Time
}

// System memakai jam asli komputer, dipakai saat aplikasi berjalan normal
type System struct{}

func (System) Now() time.Time { return time.Now() }

// Fake memakai waktu yang diatur sendiri di test dan baru berubah kalau dimajukan lewat Advance
type Fake struct {
	// mu menjaga t karena test bisa memanggilnya dari beberapa goroutine
	mu sync.Mutex
	t  time.Time
}

func NewFake(t time.Time) *Fake { return &Fake{t: t} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

// Advance memajukan waktu palsu tanpa perlu menunggu
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	f.t = f.t.Add(d)
	f.mu.Unlock()
}
