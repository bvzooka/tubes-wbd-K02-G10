package queue_test

import (
	"context"
	"strings"
	"testing"
	"time"

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
