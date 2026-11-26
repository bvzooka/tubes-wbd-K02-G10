package migration_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"dbplatform/internal/access"
	"dbplatform/internal/approval"
	"dbplatform/internal/clock"
	"dbplatform/internal/domain"
	"dbplatform/internal/migration"
	"dbplatform/internal/queue"
)

type flowEnv struct {
	serviceEnv
	executions *migration.ExecutionService
	approvals  *approval.Service
}

// approval dan antrean memakai implementasi asli supaya sambungannya ikut teruji
// dua group punya akses: dev dipimpin editor, analis dipimpin viewer
func newFlowEnv(t *testing.T) flowEnv {
	t.Helper()
	e := newServiceEnv(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	approvals := approval.NewService(e.pool, clock.System{}, logger)
	f := flowEnv{
		serviceEnv: e,
		approvals:  approvals,
		executions: migration.NewExecutionService(migration.ExecutionDeps{
			Store: e.repo, DB: e.pool, Authz: access.New(e.pool), Approvals: approvals,
			Queue: queue.NewRepo(e.pool, clock.System{}), Clock: clock.System{}, Logger: logger, MaxAttempts: 3,
		}),
	}
	f.setLeader(t, "dev", f.editor.UserID)
	f.setLeader(t, "analis", f.viewer.UserID)
	return f
}

func (f flowEnv) setLeader(t *testing.T, group string, userID int64) {
	t.Helper()
	_, err := f.pool.Exec(context.Background(), `UPDATE user_groups SET leader_id = $2 WHERE name = $1`, group, userID)
	if err != nil {
		t.Fatal(err)
	}
}

func (f flowEnv) ready(t *testing.T) migration.Definition {
	t.Helper()
	in := normalInput()
	in.DownSQL = "DROP TABLE a"
	ctx := context.Background()
	d, err := f.service.Create(ctx, f.editor, f.conn, in)
	if err != nil {
		t.Fatal(err)
	}
	d, err = f.service.Freeze(ctx, f.editor, f.conn, d.Version, d.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func (f flowEnv) apply(d migration.Definition) (domain.Execution, error) {
	return f.executions.Request(context.Background(), f.editor, f.conn, d.Version, domain.KindApply, false)
}

// approveByViewer menyetujui sisa persetujuan lewat alur approval yang dipakai tombol Setuju
func (f flowEnv) approveByViewer(t *testing.T, executionID int64) {
	t.Helper()
	ctx := context.Background()
	var groupID int64
	if err := f.pool.QueryRow(ctx, `SELECT id FROM user_groups WHERE name = 'analis'`).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.approvals.Review(ctx, f.viewer, executionID, groupID, true, ""); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionService_Integration_RequestApproveRun(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	f := newFlowEnv(t)
	ctx := context.Background()
	d := f.ready(t)

	ex, err := f.apply(d)
	if err != nil {
		t.Fatal(err)
	}
	if ex.Status != domain.ExecPendingReview || ex.SQLSnapshot != d.UpSQL || ex.Checksum != d.Checksum {
		t.Errorf("permintaan = %+v", ex)
	}
	if _, err := f.apply(d); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("permintaan ganda: %v", err)
	}
	if _, err := f.executions.Run(ctx, f.editor, ex.ID); !errors.Is(err, domain.ErrApprovalRequired) {
		t.Errorf("jalankan sebelum disetujui: %v", err)
	}

	f.approveByViewer(t, ex.ID)
	first, err := f.executions.Run(ctx, f.editor, ex.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.executions.Run(ctx, f.editor, ex.ID)
	if err != nil || second != first || first == "" {
		t.Errorf("job = %q lalu %q, %v", first, second, err)
	}
	got, err := f.repo.GetExecution(ctx, ex.ID)
	if err != nil || got.Status != domain.ExecQueued || got.QueuedAt == nil {
		t.Errorf("setelah dijalankan = %+v, %v", got, err)
	}
	if err := f.executions.Cancel(ctx, f.editor, ex.ID); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("batalkan yang sudah antre: %v", err)
	}
}

func TestExecutionService_Integration_Rejections(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	f := newFlowEnv(t)
	ctx := context.Background()

	draft := f.createDefinition(t, normalInput())
	if _, err := f.apply(draft); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("apply draft: %v", err)
	}
	if _, err := f.service.Cancel(ctx, f.editor, f.conn, draft.Version); err != nil {
		t.Fatal(err)
	}
	d := f.ready(t)
	if _, err := f.executions.Request(ctx, f.viewer, f.conn, d.Version, domain.KindApply, false); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("view only mengajukan: %v", err)
	}

	f.exec(t, `UPDATE user_groups SET leader_id = NULL WHERE name = 'analis'`)
	if _, err := f.apply(d); !errors.Is(err, domain.ErrNoLeader) {
		t.Errorf("group tanpa leader: %v", err)
	}
	var count int
	if err := f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM migration_executions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("permintaan yatim tersisa %d", count)
	}
	f.setLeader(t, "analis", f.viewer.UserID)

	ex, err := f.apply(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.executions.Cancel(ctx, f.outsider, ex.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("orang luar membatalkan: %v", err)
	}
	if err := f.executions.Cancel(ctx, f.editor, ex.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.executions.Run(ctx, f.editor, ex.ID); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("jalankan yang dibatalkan: %v", err)
	}
}

func TestExecutionService_Integration_AutoApproveAndRollback(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	f := newFlowEnv(t)
	ctx := context.Background()
	f.exec(t, `DELETE FROM group_database_access WHERE group_id = (SELECT id FROM user_groups WHERE name = 'analis')`)
	d := f.ready(t)

	applied, err := f.apply(d)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Status != domain.ExecApproved {
		t.Fatalf("pengaju satu-satunya leader, status = %s", applied.Status)
	}

	// seolah runner sudah berhasil menjalankannya
	f.exec(t, `UPDATE migration_executions SET status = 'success'`)
	f.exec(t, `UPDATE db_migrations SET state = 'applied'`)

	rollback, err := f.executions.Request(ctx, f.editor, f.conn, d.Version, domain.KindRollback, false)
	if err != nil {
		t.Fatal(err)
	}
	if rollback.SQLSnapshot != d.DownSQL || rollback.RollsBackExecutionID == nil || *rollback.RollsBackExecutionID != applied.ID {
		t.Errorf("rollback = %+v", rollback)
	}
	if rollback.RootExecutionID == nil || *rollback.RootExecutionID != applied.ID {
		t.Errorf("root = %v", rollback.RootExecutionID)
	}
}

func (f flowEnv) exec(t *testing.T, sql string) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), sql); err != nil {
		t.Fatal(err)
	}
}
