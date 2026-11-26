package obs

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"dbplatform/internal/dbdriver"
	"dbplatform/internal/dbdriver/postgres"
)

// mengembalikan statistik semua pool dalam satu kelompok
type PoolSource func() []dbdriver.PoolStats

// membawa statistik pool koneksi ke Prometheus setiap kali di-scrape.
// Label pool hanya berisi nama kelompok (appdb dan target), bukan ID atau biar kardinalitasnya rendah. Pool satu kelompok dijumlahkan.
type PoolCollector struct {
	sources     map[string]PoolSource
	pools       *prometheus.Desc
	maxOpen     *prometheus.Desc
	connections *prometheus.Desc
	waits       *prometheus.Desc
	waitSeconds *prometheus.Desc
}

// menerima kelompok pool dengan namanya sebagai nilai label pool
func NewPoolCollector(sources map[string]PoolSource) *PoolCollector {
	pool := []string{"pool"}
	return &PoolCollector{
		sources: sources,
		pools: prometheus.NewDesc("dbplatform_db_pools",
			"Jumlah pool koneksi yang sedang terbuka.", pool, nil),
		maxOpen: prometheus.NewDesc("dbplatform_db_pool_max_connections",
			"Batas jumlah koneksi, dijumlahkan dari semua pool.", pool, nil),
		connections: prometheus.NewDesc("dbplatform_db_pool_connections",
			"Jumlah koneksi per state: in_use (dipakai) atau idle (menganggur).", []string{"pool", "state"}, nil),

		waits: prometheus.NewDesc("dbplatform_db_pool_waits_total",
			"Berapa kali peminjam harus menunggu karena tidak ada koneksi menganggur (koneksi baru sedang dibuat atau pool penuh).", pool, nil),
		waitSeconds: prometheus.NewDesc("dbplatform_db_pool_wait_seconds_total",
			"Total lama menunggu tersebut.", pool, nil),
	}
}

// susunan yg dipakai server dan worker: satu pool db platform (label appdb) dan semua pool db target yg sedang di-cache (label target).
func PlatformPools(appdb *pgxpool.Pool, targets PoolSource) *PoolCollector {
	return NewPoolCollector(map[string]PoolSource{
		"appdb":  func() []dbdriver.PoolStats { return []dbdriver.PoolStats{postgres.StatsOf(appdb)} },
		"target": targets,
	})
}

func (c *PoolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.pools
	ch <- c.maxOpen
	ch <- c.connections
	ch <- c.waits
	ch <- c.waitSeconds
}

func (c *PoolCollector) Collect(ch chan<- prometheus.Metric) {
	for name, source := range c.sources {
		stats := source()
		var total dbdriver.PoolStats
		for _, s := range stats {
			total = total.Add(s)
		}

		gauge := func(desc *prometheus.Desc, v float64, labels ...string) {
			ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, v, append([]string{name}, labels...)...)
		}
		counter := func(desc *prometheus.Desc, v float64) {
			ch <- prometheus.MustNewConstMetric(desc, prometheus.CounterValue, v, name)
		}

		gauge(c.pools, float64(len(stats)))
		gauge(c.maxOpen, float64(total.MaxOpen))
		gauge(c.connections, float64(total.InUse), "in_use")
		gauge(c.connections, float64(total.Idle), "idle")
		counter(c.waits, float64(total.WaitCount))
		counter(c.waitSeconds, total.WaitDuration.Seconds())
	}
}
