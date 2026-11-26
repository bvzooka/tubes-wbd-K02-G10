package migration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"dbplatform/internal/appdb"
	"dbplatform/internal/domain"
	"dbplatform/internal/migration"
)

func (e repoEnv) newExecution(d migration.Definition, kind domain.ExecutionKind) domain.Execution {
	return domain.Execution{
		MigrationID: d.ID, ConnectionID: e.conn, Version: d.Version, Revision: d.Revision,
		Kind: kind, Mode: d.Mode, Batch: d.Batch, SQLSnapshot: d.UpSQL, Checksum: d.Checksum,
		RequestedBy: e.user.ID, RequestedAt: time.Now().UTC(), CorrelationID: "corr-test-1",
	}
}

func (e repoEnv) insertExecution(t *testing.T, in domain.Execution) domain.Execution {
	t.Helper()
	got, err := e.repo.InsertExecution(context.Background(), e.pool, in)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestExecutionRepo_Integration_InsertAndRead(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	e := newRepoEnv(t)
	ctx := context.Background()
	d := e.createDefinition(t, batchInput())

	got := e.insertExecution(t, e.newExecution(d, domain.KindApply))

	if got.ID == 0 || got.Status != domain.ExecPendingReview || got.RequestedByName != "budi" {
		t.Errorf("permintaan = %+v", got)
	}
	if got.SQLSnapshot != d.UpSQL || got.Checksum != d.Checksum || got.Batch == nil || *got.Batch != *d.Batch {
		t.Errorf("salinan isi tidak sama: %+v", got)
	}
	if got.RollsBackExecutionID != nil || got.QueuedAt != nil || got.CorrelationID != "corr-test-1" {
		t.Errorf("kolom opsional = %+v", got)
	}
	if _, err := e.repo.GetExecution(ctx, got.ID+100); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("id tidak ada: %v", err)
	}
}

func TestExecutionRepo_Integration_TxRollsBack(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	e := newRepoEnv(t)
	ctx := context.Background()
	d := e.createDefinition(t, normalInput())
	boom := errors.New("gagal di tengah")

	var id int64
	err := e.repo.InConnectionTx(ctx, e.conn, func(q appdb.DBTX) error {
		got, err := e.repo.InsertExecution(ctx, q, e.newExecution(d, domain.KindApply))
		if err != nil {
			return err
		}
		id = got.ID
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v", err)
	}
	if _, err := e.repo.GetExecution(ctx, id); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("permintaan harus ikut dibatalkan: %v", err)
	}
}

func TestExecutionRepo_Integration_StatusAndLatest(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	e := newRepoEnv(t)
	ctx := context.Background()
	d := e.createDefinition(t, normalInput())
	apply := e.insertExecution(t, e.newExecution(d, domain.KindApply))
	rollback := e.insertExecution(t, e.newExecution(d, domain.KindRollback))
	now := time.Now().UTC().Truncate(time.Second)

	if err := e.repo.SetExecutionStatus(ctx, e.pool, apply.ID, domain.ExecPendingReview, domain.ExecCancelled, now); err != nil {
		t.Fatal(err)
	}
	err := e.repo.SetExecutionStatus(ctx, e.pool, apply.ID, domain.ExecPendingReview, domain.ExecQueued, now)
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("status asal basi: %v", err)
	}
	got, err := e.repo.GetExecution(ctx, apply.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.ExecCancelled || got.FinishedAt == nil || !got.FinishedAt.Equal(now) || got.QueuedAt != nil {
		t.Errorf("setelah dibatalkan = %+v", got)
	}

	// rollback yang sukses tidak boleh dianggap sebagai eksekusi maju
	for _, id := range []int64{apply.ID, rollback.ID} {
		if _, err := e.pool.Exec(ctx, `UPDATE migration_executions SET status = 'success' WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := e.repo.LatestForwardExecution(ctx, e.pool, d.ID, domain.ExecSuccess)
	if err != nil || latest.ID != apply.ID {
		t.Errorf("eksekusi maju terakhir = %d, %v", latest.ID, err)
	}
	if _, err := e.repo.LatestForwardExecution(ctx, e.pool, d.ID, domain.ExecPartial); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("tidak ada yang partial: %v", err)
	}
}

func TestExecutionRepo_Integration_VersionStatesAndJob(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	e := newRepoEnv(t)
	ctx := context.Background()
	first := e.createDefinition(t, normalInput())
	e.createDefinition(t, normalInput())
	if err := e.repo.MoveDefinition(ctx, first, domain.StateReady); err != nil {
		t.Fatal(err)
	}

	states, err := e.repo.VersionStates(ctx, e.pool, e.conn)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 2 || states[0].Version != 1 || states[0].State != domain.StateReady || states[1].State != domain.StateDraft {
		t.Errorf("daftar status = %+v", states)
	}

	ex := e.insertExecution(t, e.newExecution(first, domain.KindApply))
	if _, err := e.repo.JobID(ctx, e.pool, ex.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("belum ada job: %v", err)
	}
	var want string
	err = e.pool.QueryRow(ctx,
		`INSERT INTO migration_jobs (execution_id, connection_id) VALUES ($1, $2) RETURNING id::text`,
		ex.ID, e.conn).Scan(&want)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := e.repo.JobID(ctx, e.pool, ex.ID); err != nil || got != want {
		t.Errorf("job = %q, %v", got, err)
	}
}
