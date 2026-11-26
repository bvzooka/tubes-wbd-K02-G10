package obs

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"dbplatform/internal/dbdriver"
)

func TestPoolCollector_SumsPoolsPerGroup(t *testing.T) {
	c := NewPoolCollector(map[string]PoolSource{
		"target": func() []dbdriver.PoolStats {
			return []dbdriver.PoolStats{
				{MaxOpen: 5, InUse: 2, Idle: 1, WaitCount: 3, WaitDuration: 1500 * time.Millisecond},
				{MaxOpen: 5, InUse: 1, Idle: 4, WaitCount: 1, WaitDuration: 500 * time.Millisecond},
			}
		},
		"appdb": func() []dbdriver.PoolStats { return nil },
	})
	want := `
# HELP dbplatform_db_pool_connections Jumlah koneksi per state: in_use (dipakai) atau idle (menganggur).
# TYPE dbplatform_db_pool_connections gauge
dbplatform_db_pool_connections{pool="appdb",state="idle"} 0
dbplatform_db_pool_connections{pool="appdb",state="in_use"} 0
dbplatform_db_pool_connections{pool="target",state="idle"} 5
dbplatform_db_pool_connections{pool="target",state="in_use"} 3
# HELP dbplatform_db_pool_max_connections Batas jumlah koneksi, dijumlahkan dari semua pool.
# TYPE dbplatform_db_pool_max_connections gauge
dbplatform_db_pool_max_connections{pool="appdb"} 0
dbplatform_db_pool_max_connections{pool="target"} 10
# HELP dbplatform_db_pool_wait_seconds_total Total lama menunggu tersebut.
# TYPE dbplatform_db_pool_wait_seconds_total counter
dbplatform_db_pool_wait_seconds_total{pool="appdb"} 0
dbplatform_db_pool_wait_seconds_total{pool="target"} 2
# HELP dbplatform_db_pool_waits_total Berapa kali peminjam harus menunggu karena tidak ada koneksi menganggur (koneksi baru sedang dibuat atau pool penuh).
# TYPE dbplatform_db_pool_waits_total counter
dbplatform_db_pool_waits_total{pool="appdb"} 0
dbplatform_db_pool_waits_total{pool="target"} 4
# HELP dbplatform_db_pools Jumlah pool koneksi yang sedang terbuka.
# TYPE dbplatform_db_pools gauge
dbplatform_db_pools{pool="appdb"} 0
dbplatform_db_pools{pool="target"} 2
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(want)); err != nil {
		t.Fatal(err)
	}
}
