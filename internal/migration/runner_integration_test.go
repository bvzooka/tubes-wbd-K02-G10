package migration_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"dbplatform/internal/clock"
	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
	"dbplatform/internal/migration"
	"dbplatform/internal/queue"
)

type fakeLease struct{ ctx context.Context }

func (l fakeLease) Context() context.Context { return l.ctx }
func (l fakeLease) Fence() int64             { return 0 }
func (l fakeLease) Lost() bool               { return false }

func (r runEnv) runner() *migration.Runner {
	return migration.NewRunner(migration.RunnerDeps{
		Store: r.repo, Revalidator: r.revalidator(), Drivers: r.drivers, Clock: clock.System{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func (r runEnv) queue() *queue.Repo { return queue.NewRepo(r.pool, clock.System{}) }

// enqueue melewati alur asli lalu mengambil job seperti yang dilakukan worker
func (r runEnv) enqueue(t *testing.T, d migration.Definition, kind domain.ExecutionKind) domain.Job {
	t.Helper()
	return r.claim(t, d, kind, false)
}

func (r runEnv) claim(t *testing.T, d migration.Definition, kind domain.ExecutionKind, resume bool) domain.Job {
	t.Helper()
	ctx := context.Background()
	ex, err := r.executions.Request(ctx, r.editor, r.conn, d.Version, kind, resume)
	if err != nil {
		t.Fatal(err)
	}
	r.approveByViewer(t, ex.ID)
	if _, err := r.executions.Run(ctx, r.editor, ex.ID); err != nil {
		t.Fatal(err)
	}
	job, ok, err := r.queue().Claim(ctx, "worker-test", time.Minute, time.Now().Add(time.Second))
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	return job
}

func (r runEnv) run(job domain.Job) domain.RunResult {
	return r.runner().Run(context.Background(), job, fakeLease{context.Background()})
}

func (r runEnv) readyWith(t *testing.T, up, down string) migration.Definition {
	t.Helper()
	ctx := context.Background()
	d, err := r.service.Create(ctx, r.editor, r.conn, migration.DraftInput{Name: "uji", UpSQL: up, DownSQL: down})
	if err != nil {
		t.Fatal(err)
	}
	d, err = r.service.Freeze(ctx, r.editor, r.conn, d.Version, d.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func (r runEnv) tableExists(t *testing.T, name string) bool {
	t.Helper()
	var found bool
	err := r.target.QueryRow(context.Background(), `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&found)
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// check memastikan status permintaan, status migration, dan jumlah baris riwayat
func (r runEnv) check(t *testing.T, job domain.Job, status domain.ExecutionStatus, state domain.MigrationState, history int) {
	t.Helper()
	ctx := context.Background()
	e, err := r.repo.GetExecution(ctx, job.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	d, err := r.repo.GetDefinition(ctx, r.conn, e.Version)
	if err != nil {
		t.Fatal(err)
	}
	rows := r.history(t, migration.HistoryFilter{ExecutionID: e.ID})
	if e.Status != status || d.State != state || len(rows) != history {
		t.Errorf("permintaan %s, migration %s, riwayat %d; mau %s, %s, %d", e.Status, d.State, len(rows), status, state, history)
	}
}

func TestRunner_Integration_ApplyThenRollback(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	r := newRunEnv(t)
	ctx := context.Background()
	d := r.ready(t)

	job := r.enqueue(t, d, domain.KindApply)
	if got := r.run(job); got.Outcome != domain.OutcomeSuccess {
		t.Fatalf("apply = %+v", got)
	}
	if !r.tableExists(t, "a") {
		t.Fatal("tabel tidak dibuat")
	}
	r.check(t, job, domain.ExecSuccess, domain.StateApplied, 1)

	// pesan yang sama datang lagi, tidak boleh dijalankan ulang
	if got := r.run(job); got.Outcome != domain.OutcomeSuccess {
		t.Errorf("duplikat = %+v", got)
	}
	r.check(t, job, domain.ExecSuccess, domain.StateApplied, 1)
	if err := r.queue().Finish(ctx, job, "", time.Now()); err != nil {
		t.Fatal(err)
	}

	back := r.enqueue(t, d, domain.KindRollback)
	if got := r.run(back); got.Outcome != domain.OutcomeSuccess {
		t.Fatalf("rollback = %+v", got)
	}
	if r.tableExists(t, "a") {
		t.Error("tabel masih ada setelah rollback")
	}
	r.check(t, back, domain.ExecSuccess, domain.StateRolledBack, 1)
}

func TestRunner_Integration_FailureLeavesTargetUnchanged(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	r := newRunEnv(t)
	d := r.readyWith(t, "CREATE TABLE separuh (id int); SELECT * FROM tidak_ada", "DROP TABLE separuh")
	job := r.enqueue(t, d, domain.KindApply)

	got := r.run(job)

	if got.Outcome != domain.OutcomeFailed || got.Retryable || got.Err == nil {
		t.Errorf("hasil = %+v", got)
	}
	if r.tableExists(t, "separuh") {
		t.Error("perintah pertama harus ikut dibatalkan")
	}
	r.check(t, job, domain.ExecFailed, domain.StateFailed, 1)
}

// foreign key yang ditunda baru diperiksa saat commit, jadi lewat jalur pencarian bukti
func TestRunner_Integration_CommitFailureIsNotRetried(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	r := newRunEnv(t)
	d := r.readyWith(t, `CREATE TABLE induk (id int PRIMARY KEY);
		CREATE TABLE anak (induk_id int REFERENCES induk DEFERRABLE INITIALLY DEFERRED);
		INSERT INTO anak VALUES (1)`, "DROP TABLE anak; DROP TABLE induk")
	job := r.enqueue(t, d, domain.KindApply)

	got := r.run(job)

	if got.Outcome != domain.OutcomeFailed || got.Retryable {
		t.Errorf("hasil = %+v", got)
	}
	if r.tableExists(t, "anak") {
		t.Error("commit gagal harus membatalkan semuanya")
	}
	r.check(t, job, domain.ExecFailed, domain.StateFailed, 1)
}

func TestRunner_Integration_RevalidationFailureIsRecorded(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	r := newRunEnv(t)
	job := r.enqueue(t, r.ready(t), domain.KindApply)
	r.exec(t, `UPDATE group_database_access SET access_level = 'view_only'`)

	got := r.run(job)

	if got.Outcome != domain.OutcomeFailed || got.Retryable {
		t.Errorf("hasil = %+v", got)
	}
	if r.tableExists(t, "a") {
		t.Error("target tidak boleh disentuh")
	}
	r.check(t, job, domain.ExecFailed, domain.StateFailed, 1)
}

func TestRunner_Integration_BusyTargetRequeues(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	r := newRunEnv(t)
	ctx := context.Background()
	job := r.enqueue(t, r.ready(t), domain.KindApply)

	other, err := r.target.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Release()
	if _, err := other.Exec(ctx, `SELECT pg_advisory_lock($1)`, dbdriver.PlatformMigrationLockKey); err != nil {
		t.Fatal(err)
	}

	if got := r.run(job); got.Outcome != domain.OutcomeRequeue {
		t.Errorf("hasil = %+v", got)
	}
	r.check(t, job, domain.ExecQueued, domain.StateReady, 0)
	if _, err := other.Exec(ctx, `SELECT pg_advisory_unlock_all()`); err != nil {
		t.Fatal(err)
	}
}

func TestRunner_Integration_ReconcileUsesProof(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	tests := []struct {
		name      string
		committed bool
		status    domain.ExecutionStatus
		state     domain.MigrationState
	}{
		{"bukti ada berarti sukses", true, domain.ExecSuccess, domain.StateApplied},
		{"bukti tidak ada berarti diantrekan lagi", false, domain.ExecQueued, domain.StateReady},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRunEnv(t)
			ctx := context.Background()
			job := r.enqueue(t, r.ready(t), domain.KindApply)
			// seolah worker mati setelah menandai running
			if err := r.repo.MarkRunning(ctx, job, job.ExecutionID, time.Now()); err != nil {
				t.Fatal(err)
			}
			if tt.committed {
				if _, err := r.target.Exec(ctx, `CREATE SCHEMA _dbplatform;
					CREATE TABLE _dbplatform.executions (execution_id BIGINT PRIMARY KEY, kind TEXT, attempt INT,
						checksum TEXT, committed_at TIMESTAMPTZ DEFAULT now())`); err != nil {
					t.Fatal(err)
				}
				if _, err := r.target.Exec(ctx,
					`INSERT INTO _dbplatform.executions (execution_id) VALUES ($1)`, job.ExecutionID); err != nil {
					t.Fatal(err)
				}
			}

			r.runner().Reconcile(ctx, job)

			r.check(t, job, tt.status, tt.state, 1)
		})
	}
}
