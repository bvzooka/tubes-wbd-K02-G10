package migration_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"dbplatform/internal/domain"
	"dbplatform/internal/migration"
)

// queued menyiapkan permintaan beserta jobnya, seolah sudah disetujui dan diambil worker
func (f flowEnv) queued(t *testing.T, d migration.Definition, kind domain.ExecutionKind, undoes *int64) (domain.Execution, domain.Job) {
	t.Helper()
	in := f.newExecution(d, kind)
	in.RollsBackExecutionID = undoes
	ex := f.insertExecution(t, in)
	job := domain.Job{ExecutionID: ex.ID, ConnectionID: f.conn}
	err := f.pool.QueryRow(context.Background(),
		`INSERT INTO migration_jobs (execution_id, connection_id) VALUES ($1, $2) RETURNING id::text`,
		ex.ID, f.conn).Scan(&job.ID)
	if err != nil {
		t.Fatal(err)
	}
	return ex, job
}

func (f flowEnv) record(t *testing.T, e domain.Execution, job domain.Job, a migration.Attempt) {
	t.Helper()
	a.StartedAt, a.FinishedAt = time.Now().UTC(), time.Now().UTC()
	if err := f.repo.RecordAttempt(context.Background(), job, e, a); err != nil {
		t.Fatal(err)
	}
}

// run membuat satu eksekusi yang langsung selesai dengan hasil tertentu
func (f flowEnv) run(t *testing.T, d migration.Definition, kind domain.ExecutionKind, undoes *int64, outcome domain.RunOutcome) int64 {
	t.Helper()
	ex, job := f.queued(t, d, kind, undoes)
	f.record(t, ex, job, migration.Attempt{Number: 1, Outcome: outcome, SQLExecuted: ex.SQLSnapshot})
	return ex.ID
}

func (f flowEnv) history(t *testing.T, filter migration.HistoryFilter) []int64 {
	t.Helper()
	entries, _, err := f.executions.History(context.Background(), f.viewer, f.conn, filter)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, h := range entries {
		ids = append(ids, h.ExecutionID)
	}
	return ids
}

// contoh dari spesifikasi
func TestHistory_Integration_HideRollback(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	f := newFlowEnv(t)
	a := f.createDefinition(t, normalInput())
	b := f.createDefinition(t, normalInput())
	c := f.createDefinition(t, normalInput())

	e1 := f.run(t, a, domain.KindApply, nil, domain.OutcomeSuccess)
	e2 := f.run(t, b, domain.KindApply, nil, domain.OutcomeSuccess)
	e3 := f.run(t, b, domain.KindRollback, &e2, domain.OutcomeSuccess)
	e4 := f.run(t, b, domain.KindForward, nil, domain.OutcomeSuccess)
	e5 := f.run(t, c, domain.KindApply, nil, domain.OutcomeSuccess)
	// rollback yang gagal tidak boleh menyembunyikan apa pun
	e6 := f.run(t, c, domain.KindRollback, &e5, domain.OutcomeFailed)

	if got, want := f.history(t, migration.HistoryFilter{}), []int64{e1, e2, e3, e4, e5, e6}; !reflect.DeepEqual(got, want) {
		t.Errorf("riwayat penuh = %v, mau %v", got, want)
	}
	if got, want := f.history(t, migration.HistoryFilter{HideRollback: true}), []int64{e1, e4, e5, e6}; !reflect.DeepEqual(got, want) {
		t.Errorf("hide rollback = %v, mau %v", got, want)
	}
	if got := f.history(t, migration.HistoryFilter{Status: "failed"}); !reflect.DeepEqual(got, []int64{e6}) {
		t.Errorf("saring gagal = %v", got)
	}

	_, _, err := f.executions.History(context.Background(), f.outsider, f.conn, migration.HistoryFilter{})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("orang luar: %v", err)
	}
}

func TestHistory_Integration_RetryFenceAndAppendOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	f := newFlowEnv(t)
	ctx := context.Background()
	d := f.createDefinition(t, normalInput())
	ex, job := f.queued(t, d, domain.KindApply, nil)

	f.record(t, ex, job, migration.Attempt{Number: 1, Outcome: domain.OutcomeFailed, WillRetry: true, FailureCode: "08006"})
	got, err := f.repo.GetExecution(ctx, ex.ID)
	if err != nil || got.Status != domain.ExecQueued || got.FinishedAt != nil || got.StartedAt == nil {
		t.Errorf("setelah gagal sementara = %+v, %v", got, err)
	}

	f.record(t, ex, job, migration.Attempt{Number: 2, Outcome: domain.OutcomeSuccess, Committed: true})
	after, err := f.repo.GetDefinition(ctx, f.conn, d.Version)
	if err != nil || after.State != domain.StateApplied {
		t.Errorf("status migration = %s, %v", after.State, err)
	}
	if got := f.history(t, migration.HistoryFilter{ExecutionID: ex.ID}); len(got) != 2 {
		t.Errorf("dua attempt harus tercatat, dapat %d", len(got))
	}

	stale := job
	stale.Fence = job.Fence + 1
	err = f.repo.RecordAttempt(ctx, stale, ex, migration.Attempt{Number: 3, Outcome: domain.OutcomeFailed})
	if !errors.Is(err, migration.ErrStaleFence) {
		t.Errorf("fence basi: %v", err)
	}
	for _, sql := range []string{`UPDATE migration_history SET status = 'failed'`, `DELETE FROM migration_history`} {
		if _, err := f.pool.Exec(ctx, sql); err == nil {
			t.Errorf("riwayat harus menolak: %s", sql)
		}
	}
}
