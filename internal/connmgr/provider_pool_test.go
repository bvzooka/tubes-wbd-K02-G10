package connmgr

import (
	"context"
	"testing"

	"dbplatform/internal/dbdriver"
)

func (d *fakeDriver) PoolStats() dbdriver.PoolStats {
	return dbdriver.PoolStats{MaxOpen: 5, InUse: 1, Idle: 2}
}

func TestProvider_PoolStatsFollowsCache(t *testing.T) {
	ctx := context.Background()
	st, f := newFakeStore(), &factoryRec{}
	st.put(1, true, t0, "pw")
	st.put(2, true, t0, "pw")
	p := NewProvider(st, f.build)

	if got := p.PoolStats(); len(got) != 0 {
		t.Fatalf("sebelum ada driver: %d pool, mau 0", len(got))
	}

	_, _ = p.Get(ctx, 1)
	_, _ = p.Get(ctx, 2)
	got := p.PoolStats()
	if len(got) != 2 || got[0] != (dbdriver.PoolStats{MaxOpen: 5, InUse: 1, Idle: 2}) {
		t.Fatalf("dua driver di cache: %+v", got)
	}

	st.put(2, false, t0, "pw") // dinonaktifkan: driver dibuang dari cache
	_, _ = p.Get(ctx, 2)
	if got := p.PoolStats(); len(got) != 1 {
		t.Fatalf("setelah koneksi 2 nonaktif: %d pool, mau 1", len(got))
	}
}
