package queue_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/clock"
	"dbplatform/internal/contract"
	"dbplatform/internal/domain"
	"dbplatform/internal/queue"
	"dbplatform/internal/telemetry"
	"dbplatform/internal/testutil"
)

const lease = 30 * time.Second

type env struct {
	pool *pgxpool.Pool
	repo *queue.Repo
	user int64
}

func newEnv(t *testing.T) env {
	t.Helper()
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.NewAppDB(t)
	return env{pool: pool, repo: queue.NewRepo(pool, clock.System{}), user: testutil.SeedUser(t, pool, "editor", false).ID}
}

// enqueue membuat koneksi (bila connID 0), migration, eksekusi, lalu job-nya.
func (e env) enqueue(t *testing.T, connID int64) domain.Job {
	t.Helper()
	ctx := context.Background()
	if connID == 0 {
		connID = testutil.SeedConnection(t, e.pool, "db-"+time.Now().Format("150405.000000000"), true)
	}
	var migID, execID int64
	err := e.pool.QueryRow(ctx, `
		INSERT INTO db_migrations (connection_id, version, name, sql_text, checksum, created_by)
		VALUES ($1, (SELECT COALESCE(max(version), 0) + 1 FROM db_migrations WHERE connection_id = $1), 'm', 'SELECT 1', 'x', $2)
		RETURNING id`, connID, e.user).Scan(&migID)
	if err != nil {
		t.Fatal(err)
	}
	err = e.pool.QueryRow(ctx, `
		INSERT INTO migration_executions (migration_id, connection_id, version, revision, kind, mode, sql_snapshot, checksum, requested_by, status)
		VALUES ($1, $2, 1, 1, 'apply', 'normal', 'SELECT 1', 'x', $3, 'queued') RETURNING id`,
		migID, connID, e.user).Scan(&execID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := e.repo.Enqueue(ctx, e.pool, domain.EnqueueInput{ExecutionID: execID, ConnectionID: connID})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestIntegration_EnqueueIsIdempotent(t *testing.T) {
	e := newEnv(t)
	job := e.enqueue(t, 0)
	again, err := e.repo.Enqueue(context.Background(), e.pool, domain.EnqueueInput{ExecutionID: job.ExecutionID, ConnectionID: job.ConnectionID})
	if err != nil || again.ID != job.ID || job.MaxAttempts != 3 || job.State != domain.JobQueued {
		t.Fatalf("job=%+v again=%+v err=%v", job, again, err)
	}
	got, err := e.repo.Get(context.Background(), job.ID)
	if err != nil || got.ExecutionID != job.ExecutionID {
		t.Fatalf("Get: %+v %v", got, err)
	}
}

func TestIntegration_ConcurrentClaimHasOneWinner(t *testing.T) {
	e := newEnv(t)
	e.enqueue(t, 0)

	var winners atomic.Int32
	var wg sync.WaitGroup
	now := time.Now()
	for i := range 8 {
		wg.Go(func() {
			_, ok, err := e.repo.Claim(context.Background(), "w"+string(rune('a'+i)), lease, now)
			if err != nil {
				t.Error(err)
			}
			if ok {
				winners.Add(1)
			}
		})
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("pemenang = %d, harus 1", winners.Load())
	}
}

func TestIntegration_OneJobPerConnection(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	first := e.enqueue(t, 0)
	e.enqueue(t, first.ConnectionID)
	other := e.enqueue(t, 0)
	now := time.Now()

	a, ok, _ := e.repo.Claim(ctx, "w1", lease, now)
	if !ok || a.ID != first.ID {
		t.Fatalf("claim pertama harus job tertua: %+v", a)
	}
	b, ok, _ := e.repo.Claim(ctx, "w2", lease, now)
	if !ok || b.ID != other.ID {
		t.Fatalf("koneksi berbeda harus bisa jalan paralel, dapat %+v", b)
	}
	if _, ok, _ := e.repo.Claim(ctx, "w3", lease, now); ok {
		t.Fatal("job kedua pada koneksi yang sama tidak boleh jalan bersamaan")
	}

	if err := e.repo.Finish(ctx, a, "", now); err != nil {
		t.Fatal(err)
	}
	if c, ok, _ := e.repo.Claim(ctx, "w3", lease, now); !ok || c.ConnectionID != first.ConnectionID {
		t.Fatalf("setelah job pertama selesai, job berikutnya boleh jalan: %+v", c)
	}
}

func TestIntegration_HeartbeatAndFencing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.enqueue(t, 0)
	now := time.Now()
	job, _, _ := e.repo.Claim(ctx, "w1", lease, now)

	if ok, err := e.repo.Heartbeat(ctx, job, lease, now.Add(20*time.Second)); err != nil || !ok {
		t.Fatalf("heartbeat: %v %v", ok, err)
	}
	if expired, _ := e.repo.Expired(ctx, now.Add(40*time.Second)); len(expired) != 0 {
		t.Fatal("heartbeat harus memperpanjang lease")
	}

	expired, _ := e.repo.Expired(ctx, now.Add(2*time.Minute))
	if len(expired) != 1 {
		t.Fatalf("lease habis harus terdeteksi: %+v", expired)
	}
	taken, ok, err := e.repo.TakeOver(ctx, expired[0], "reaper", lease, now.Add(2*time.Minute))
	if err != nil || !ok || taken.Fence <= job.Fence {
		t.Fatalf("take over: %+v %v %v", taken, ok, err)
	}

	if ok, _ := e.repo.Heartbeat(ctx, job, lease, now.Add(2*time.Minute)); ok {
		t.Fatal("heartbeat dengan fence lama harus ditolak")
	}
	if err := e.repo.Finish(ctx, job, "", now); !errors.Is(err, queue.ErrLeaseLost) {
		t.Fatalf("finish dengan fence lama: %v", err)
	}
	if err := e.repo.Finish(ctx, taken, "", now); err != nil {
		t.Fatalf("finish dengan fence baru: %v", err)
	}
}

func TestIntegration_RescheduleKeepsOrConsumesAttempt(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.enqueue(t, 0)
	now := time.Now()

	job, _, _ := e.repo.Claim(ctx, "w1", lease, now)
	if err := e.repo.Reschedule(ctx, job, now, "target sibuk", false); err != nil {
		t.Fatal(err)
	}
	job, _, _ = e.repo.Claim(ctx, "w1", lease, now)
	if job.Attempt != 1 {
		t.Fatalf("requeue tidak boleh menghabiskan attempt: %d", job.Attempt)
	}

	if err := e.repo.Reschedule(ctx, job, now.Add(time.Minute), "putus", true); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := e.repo.Claim(ctx, "w1", lease, now); ok {
		t.Fatal("job belum boleh diambil sebelum run_after")
	}
	job, ok, _ := e.repo.Claim(ctx, "w1", lease, now.Add(time.Minute))
	if !ok || job.Attempt != 2 || job.LastError != "putus" {
		t.Fatalf("retry: %+v", job)
	}
}

type scriptedRunner struct {
	mu      sync.Mutex
	results []domain.RunResult
	runs    int
}

func (r *scriptedRunner) Run(context.Context, domain.Job, contract.Lease) domain.RunResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := r.results[min(r.runs, len(r.results)-1)]
	r.runs++
	return res
}

func (r *scriptedRunner) Reconcile(context.Context, domain.Job) domain.RunResult {
	return domain.RunResult{Outcome: domain.OutcomeSuccess}
}

func (r *scriptedRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runs
}

func runWorker(t *testing.T, e env, runner contract.ExecutionRunner) context.CancelFunc {
	t.Helper()
	w := queue.NewWorker(e.repo, runner, queue.Config{
		ID: "test-worker", Concurrency: 2, PollInterval: 10 * time.Millisecond,
		Lease: lease, Heartbeat: 10 * time.Second, RetryBase: time.Millisecond, ReapInterval: time.Hour,
	}, clock.System{}, telemetry.NewLogger(io.Discard, "error"))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return cancel
}

func waitJobState(t *testing.T, e env, id, state string) domain.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, err := e.repo.Get(context.Background(), id)
		if err == nil && job.State == state {
			return job
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %s tidak mencapai state %s", id, state)
	return domain.Job{}
}

func TestIntegration_WorkerRetriesTransientFailureThenFinishes(t *testing.T) {
	e := newEnv(t)
	job := e.enqueue(t, 0)
	runner := &scriptedRunner{results: []domain.RunResult{
		{Outcome: domain.OutcomeFailed, Retryable: true, Err: errors.New("koneksi putus")},
		{Outcome: domain.OutcomeSuccess},
	}}
	runWorker(t, e, runner)

	done := waitJobState(t, e, job.ID, domain.JobDone)
	if runner.count() != 2 || done.Attempt != 2 {
		t.Fatalf("runs=%d attempt=%d", runner.count(), done.Attempt)
	}
}

func TestIntegration_WorkerStopsAfterMaxAttempts(t *testing.T) {
	e := newEnv(t)
	job := e.enqueue(t, 0)
	runner := &scriptedRunner{results: []domain.RunResult{
		{Outcome: domain.OutcomeFailed, Retryable: true, Err: errors.New("koneksi putus")},
	}}
	runWorker(t, e, runner)

	done := waitJobState(t, e, job.ID, domain.JobDone)
	if runner.count() != 3 || done.LastError != "koneksi putus" {
		t.Fatalf("runs=%d last_error=%q", runner.count(), done.LastError)
	}
}

func TestIntegration_WorkerDoesNotRetrySQLErrors(t *testing.T) {
	e := newEnv(t)
	job := e.enqueue(t, 0)
	runner := &scriptedRunner{results: []domain.RunResult{{Outcome: domain.OutcomeFailed, Err: errors.New("syntax error")}}}
	runWorker(t, e, runner)

	waitJobState(t, e, job.ID, domain.JobDone)
	if runner.count() != 1 {
		t.Fatalf("error SQL tidak boleh di-retry, runs=%d", runner.count())
	}
}

func TestIntegration_ReaperReconcilesAbandonedJob(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	job := e.enqueue(t, 0)
	// Worker lain mengklaim job lalu "mati": lease-nya sudah lewat.
	if _, ok, _ := e.repo.Claim(ctx, "dead-worker", time.Millisecond, time.Now()); !ok {
		t.Fatal("claim gagal")
	}

	w := queue.NewWorker(e.repo, &scriptedRunner{}, queue.Config{ID: "reaper", Concurrency: 1, Lease: lease, Heartbeat: 10 * time.Second},
		clock.System{}, telemetry.NewLogger(io.Discard, "error"))
	w.ReapOnce(ctx)

	if got, _ := e.repo.Get(ctx, job.ID); got.State != domain.JobDone {
		t.Fatalf("job harus ditutup setelah rekonsiliasi: %+v", got)
	}
}
