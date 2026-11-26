package queue_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"dbplatform/internal/queue"
)

func TestIntegration_CollectorReadsQueueFromDatabase(t *testing.T) {
	e := newEnv(t)
	e.enqueue(t, 0)
	e.enqueue(t, 0)
	if _, ok, _ := e.repo.Claim(context.Background(), "w1", lease, time.Now()); !ok {
		t.Fatal("claim gagal")
	}

	want := `
# HELP dbplatform_queue_jobs Jumlah job di queue per state (queued, running, done).
# TYPE dbplatform_queue_jobs gauge
dbplatform_queue_jobs{state="queued"} 1
dbplatform_queue_jobs{state="running"} 1
# HELP dbplatform_connection_locks_active Jumlah database yang sedang dikunci job.
# TYPE dbplatform_connection_locks_active gauge
dbplatform_connection_locks_active 1
`
	err := testutil.CollectAndCompare(queue.NewCollector(e.pool), strings.NewReader(want),
		"dbplatform_queue_jobs", "dbplatform_connection_locks_active")
	if err != nil {
		t.Fatal(err)
	}
	if n := testutil.CollectAndCount(queue.NewCollector(e.pool), "dbplatform_migration_executions"); n == 0 {
		t.Fatal("status eksekusi harus ikut dilaporkan")
	}
}

func TestIntegration_ClaimRecordsLockWait(t *testing.T) {
	e := newEnv(t)
	first := e.enqueue(t, 0)
	e.enqueue(t, first.ConnectionID)
	start := time.Now()
	ctx := context.Background()

	job, ok, err := e.repo.Claim(ctx, "w1", lease, start)
	if err != nil || !ok {
		t.Fatalf("claim pertama: ok=%v err=%v", ok, err)
	}
	if err := e.repo.Finish(ctx, job, "", start.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	before := lockWaitSum(t)
	if _, ok, err := e.repo.Claim(ctx, "w1", lease, start.Add(12*time.Second)); err != nil || !ok {
		t.Fatalf("claim kedua: ok=%v err=%v", ok, err)
	}
	// job kedua siap sejak awal, lock baru lepas di detik ke-10
	if got := lockWaitSum(t) - before; got < 9 || got > 11 {
		t.Fatalf("lock wait = %.2f detik, harusnya sekitar 10", got)
	}
}

func TestIntegration_CollectorReportsBatches(t *testing.T) {
	e := newEnv(t)
	job := e.enqueue(t, 0)
	_, err := e.pool.Exec(context.Background(),
		`UPDATE migration_executions SET batches_committed = 3, rows_committed = 250 WHERE id = $1`, job.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	want := `
# HELP dbplatform_migration_batches_committed Total batch yang sudah ter-commit oleh eksekusi migration batch.
# TYPE dbplatform_migration_batches_committed gauge
dbplatform_migration_batches_committed 3
`
	err = testutil.CollectAndCompare(queue.NewCollector(e.pool), strings.NewReader(want), "dbplatform_migration_batches_committed")
	if err != nil {
		t.Fatal(err)
	}
}

func lockWaitSum(t *testing.T) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() == "dbplatform_lock_wait_seconds" {
			return f.GetMetric()[0].GetHistogram().GetSampleSum()
		}
	}
	t.Fatal("dbplatform_lock_wait_seconds belum terdaftar")
	return 0
}
