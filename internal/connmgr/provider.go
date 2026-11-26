package connmgr

import (
	"context"
	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
	"fmt"
	"sync"
	"time"
)

// sumber data koneksi untuk Provider
type Store interface {
	// mengembalikan updated_at dan is_active, ErrNotFound kalau ga ada
	Stamp(ctx context.Context, id int64) (updatedAt time.Time, active bool, err error)
	//  mengambil baris penuh beserta password yang sudah didekripsi
	Load(ctx context.Context, id int64) (conn domain.Connection, password string, err error)
}

// membangun driver untuk satu koneksi.
type DriverFactory func(ctx context.Context, conn domain.Connection, password string) (dbdriver.Driver, error)

// cacheEntry menyimpan driver bersama updated_at koneksinya untuk mendeteksi perubahan
type cacheEntry struct {
	drv       dbdriver.Driver
	updatedAt time.Time
}

// memenuhi contract.DriverProvider dgn cache driver per koneksi
type Provider struct {
	store   Store
	factory DriverFactory

	mu    sync.Mutex
	cache map[int64]cacheEntry
}

// NewProvider dibuat dengan sumber data koneksi dan pembuat driver
func NewProvider(store Store, factory DriverFactory) *Provider {
	return &Provider{store: store, factory: factory, cache: map[int64]cacheEntry{}}
}

// mengembalikan driver untuk koneksi id, driver di-cache dan dibangun ulang kalau koneksinya berubah
func (p *Provider) Get(ctx context.Context, id int64) (dbdriver.Driver, error) {
	updatedAt, active, err := p.store.Stamp(ctx, id)
	if err != nil {
		p.evict(id)
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if !active {
		p.evictLocked(id)
		return nil, domain.ErrConnectionInactive
	}
	if e, ok := p.cache[id]; ok && e.updatedAt.Equal(updatedAt) {
		return e.drv, nil
	}

	conn, password, err := p.store.Load(ctx, id)
	if err != nil {
		return nil, err
	}
	drv, err := p.factory(ctx, conn, password)
	if err != nil {
		return nil, fmt.Errorf("build driver for connection %d: %w", id, err)
	}
	p.evictLocked(id)
	p.cache[id] = cacheEntry{drv: drv, updatedAt: conn.UpdatedAt}
	return drv, nil
}

// mengembalikan statistik pool tiap driver yang sedang di-cache
func (p *Provider) PoolStats() []dbdriver.PoolStats {
	p.mu.Lock()
	defer p.mu.Unlock()

	stats := make([]dbdriver.PoolStats, 0, len(p.cache))
	for _, e := range p.cache {
		if s, ok := e.drv.(dbdriver.PoolStatter); ok {
			stats = append(stats, s.PoolStats())
		}
	}
	return stats
}

// menutup semua driver yg di cache
func (p *Provider) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id := range p.cache {
		p.evictLocked(id)
	}
}

// evict menutup dan membuang driver dari cache, evictLocked dipakai kalau mu sudah dipegang
func (p *Provider) evict(id int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.evictLocked(id)
}

// evictLocked sama dengan evict tetapi dipakai saat mu sudah dipegang pemanggil.
// sync.Mutex tidak bisa dikunci dua kali oleh goroutine yang sama, jadi perlu versi tanpa Lock.
func (p *Provider) evictLocked(id int64) {
	if e, ok := p.cache[id]; ok {
		e.drv.Close()
		delete(p.cache, id)
	}
}
