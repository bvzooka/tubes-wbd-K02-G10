package queue

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	jobsClaimed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "dbplatform_jobs_claimed_total",
		Help: "Jumlah job yang diambil worker (termasuk yang diambil alih reaper).",
	})
	jobsRunning = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "dbplatform_jobs_running",
		Help: "Jumlah job yang sedang dijalankan worker ini.",
	})
	jobsFinished = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "dbplatform_jobs_finished_total",
		Help: "Jumlah attempt job per hasil (success, failed, partial, retry, requeue, lease_lost, ...).",
	}, []string{"outcome"})
	jobsReconciled = promauto.NewCounter(prometheus.CounterOpts{
		Name: "dbplatform_jobs_reconciled_total",
		Help: "Jumlah job yang direkonsiliasi reaper setelah worker-nya mati.",
	})
	jobDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "dbplatform_job_duration_seconds",
		Help:    "Durasi satu attempt job, dari claim sampai runner selesai.",
		Buckets: prometheus.ExponentialBuckets(0.05, 3, 10), // 50 ms sampai ~16 menit
	}, []string{"outcome"})
	queueWait = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "dbplatform_queue_wait_seconds",
		Help:    "Lama job menunggu di antrean sejak siap dijalankan sampai diambil worker.",
		Buckets: prometheus.ExponentialBuckets(0.05, 3, 10),
	})
	lockWait = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "dbplatform_lock_wait_seconds",
		Help:    "Lama job yang sudah siap menunggu lock koneksinya dilepas job lain.",
		Buckets: prometheus.ExponentialBuckets(0.05, 3, 10),
	})
)

// Collector ngitung isi queue langsung dari database tiap kali Prometheus scrape
type Collector struct {
	pool       *pgxpool.Pool
	jobs       *prometheus.Desc
	executions *prometheus.Desc
	locks      *prometheus.Desc
	batches    *prometheus.Desc
	rows       *prometheus.Desc
}

func NewCollector(pool *pgxpool.Pool) *Collector {
	return &Collector{
		pool: pool,
		jobs: prometheus.NewDesc("dbplatform_queue_jobs",
			"Jumlah job di queue per state (queued, running, done).", []string{"state"}, nil),
		executions: prometheus.NewDesc("dbplatform_migration_executions",
			"Jumlah eksekusi migration per status.", []string{"status"}, nil),
		locks: prometheus.NewDesc("dbplatform_connection_locks_active",
			"Jumlah database yang sedang dikunci job.", nil, nil),
		batches: prometheus.NewDesc("dbplatform_migration_batches_committed",
			"Total batch yang sudah ter-commit oleh eksekusi migration batch.", nil, nil),
		rows: prometheus.NewDesc("dbplatform_migration_rows_committed",
			"Total baris yang sudah ter-commit oleh eksekusi migration batch.", nil, nil),
	}
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.jobs
	ch <- c.executions
	ch <- c.locks
	ch <- c.batches
	ch <- c.rows
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	ctx := context.Background()
	c.collectGrouped(ctx, ch, c.jobs, `SELECT state, count(*) FROM migration_jobs GROUP BY state`)
	c.collectGrouped(ctx, ch, c.executions, `SELECT status, count(*) FROM migration_executions GROUP BY status`)
	c.collectSingle(ctx, ch, c.locks, `SELECT count(*) FROM connection_locks WHERE expires_at > now()`)
	c.collectSingle(ctx, ch, c.batches, `SELECT COALESCE(sum(batches_committed), 0) FROM migration_executions`)
	c.collectSingle(ctx, ch, c.rows, `SELECT COALESCE(sum(rows_committed), 0) FROM migration_executions`)
}

func (c *Collector) collectGrouped(ctx context.Context, ch chan<- prometheus.Metric, desc *prometheus.Desc, sql string) {
	rows, err := c.pool.Query(ctx, sql)
	if err != nil {
		ch <- prometheus.NewInvalidMetric(desc, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var label string
		var n float64
		if err := rows.Scan(&label, &n); err != nil {
			ch <- prometheus.NewInvalidMetric(desc, err)
			return
		}
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, n, label)
	}
}

func (c *Collector) collectSingle(ctx context.Context, ch chan<- prometheus.Metric, desc *prometheus.Desc, sql string) {
	var n float64
	if err := c.pool.QueryRow(ctx, sql).Scan(&n); err != nil {
		ch <- prometheus.NewInvalidMetric(desc, err)
		return
	}
	ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, n)
}
