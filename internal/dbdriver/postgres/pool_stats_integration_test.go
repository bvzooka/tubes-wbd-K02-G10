package postgres

import (
	"context"
	"testing"

	"dbplatform/internal/testutil"
)

func TestPoolStats_Integration_CountsAcquiredConnections(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test butuh Postgres (make up)")
	}
	ctx := context.Background()
	pool := testutil.NewTargetDB(t)
	d := NewFromPool(pool)

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.PoolStats(); got.InUse != 1 || got.MaxOpen != int(pool.Config().MaxConns) {
		t.Fatalf("saat satu koneksi dipinjam: %+v", got)
	}

	conn.Release()
	if got := d.PoolStats(); got.InUse != 0 || got.Idle < 1 {
		t.Fatalf("setelah koneksi dikembalikan: %+v", got)
	}
}
