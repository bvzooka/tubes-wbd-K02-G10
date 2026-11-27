package approval_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/approval"
	"dbplatform/internal/clock"
	"dbplatform/internal/domain"
	"dbplatform/internal/telemetry"
	"dbplatform/internal/testutil"
)

type env struct {
	pool   *pgxpool.Pool
	svc    *approval.Service
	connID int64
	editor domain.User // pemohon, anggota group "Edit"
	leadA  domain.User // leader group "Edit"
	leadB  domain.User // leader group "View"
	admin  domain.User
	groupA int64
	groupB int64
}

func newEnv(t *testing.T) env {
	t.Helper()
	if testing.Short() {
		t.Skip("integration")
	}
	pool := testutil.NewAppDB(t)
	e := env{pool: pool, svc: approval.NewService(pool, clock.NewFake(time.Now()), telemetry.NewLogger(io.Discard, "error"))}
	e.connID = testutil.SeedConnection(t, pool, "sampledb", true)
	e.editor = testutil.SeedUser(t, pool, "editor", false)
	e.leadA = testutil.SeedUser(t, pool, "lead-a", false)
	e.leadB = testutil.SeedUser(t, pool, "lead-b", false)
	e.admin = testutil.SeedUser(t, pool, "root", true)
	e.groupA = testutil.SeedGroup(t, pool, "Edit", e.editor.ID, e.leadA.ID)
	e.groupB = testutil.SeedGroup(t, pool, "View", e.leadB.ID)
	testutil.Grant(t, pool, e.groupA, e.connID, domain.AccessViewEdit)
	testutil.Grant(t, pool, e.groupB, e.connID, domain.AccessViewOnly)
	e.setLeader(t, e.groupA, e.leadA.ID)
	e.setLeader(t, e.groupB, e.leadB.ID)
	return e
}

func (e env) setLeader(t *testing.T, groupID, userID int64) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `UPDATE user_groups SET leader_id = $2 WHERE id = $1`, groupID, userID); err != nil {
		t.Fatal(err)
	}
}

// request membuat eksekusi pending_review beserta approval-nya, seperti yang dilakukan
// modul migration saat pengguna menekan "Ajukan".
func (e env) request(t *testing.T, requester int64) (int64, []domain.Approval, error) {
	t.Helper()
	ctx := context.Background()
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	var migID, execID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO db_migrations (connection_id, version, name, sql_text, checksum, created_by)
		VALUES ($1, (SELECT COALESCE(max(version), 0) + 1 FROM db_migrations WHERE connection_id = $1), 'm', 'SELECT 1', 'x', $2)
		RETURNING id`, e.connID, requester).Scan(&migID)
	if err != nil {
		t.Fatal(err)
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO migration_executions (migration_id, connection_id, version, revision, kind, mode, sql_snapshot, checksum, requested_by)
		VALUES ($1, $2, 1, 1, 'apply', 'normal', 'SELECT 1', 'x', $3) RETURNING id`,
		migID, e.connID, requester).Scan(&execID)
	if err != nil {
		t.Fatal(err)
	}
	approvals, err := e.svc.Create(ctx, tx, execID, e.connID, requester)
	if err != nil {
		return 0, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return execID, approvals, nil
}

func (e env) status(t *testing.T, execID int64) domain.ExecutionStatus {
	t.Helper()
	var s domain.ExecutionStatus
	if err := e.pool.QueryRow(context.Background(), `SELECT status FROM migration_executions WHERE id = $1`, execID).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func principal(u domain.User) domain.Principal {
	return domain.Principal{UserID: u.ID, Username: u.Username, IsPlatformAdmin: u.IsPlatformAdmin}
}

func TestIntegration_CreateSnapshotsEveryGroupWithAccess(t *testing.T) {
	e := newEnv(t)
	_, approvals, err := e.request(t, e.editor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(approvals) != 2 {
		t.Fatalf("view_only dan view_edit sama-sama wajib menyetujui: %+v", approvals)
	}
	for _, a := range approvals {
		if a.Status != domain.ApprovalPending || a.Auto {
			t.Errorf("pemohon bukan leader, semua harus pending: %+v", a)
		}
	}
}

func TestIntegration_GroupWithoutLeaderBlocksRequest(t *testing.T) {
	e := newEnv(t)
	if _, err := e.pool.Exec(context.Background(), `UPDATE user_groups SET leader_id = NULL WHERE id = $1`, e.groupB); err != nil {
		t.Fatal(err)
	}
	_, _, err := e.request(t, e.editor.ID)
	if !errors.Is(err, domain.ErrNoLeader) || !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v", err)
	}
	var ve *domain.ValidationError
	if !errors.As(err, &ve) || !strings.Contains(ve.Message, "View") {
		t.Fatalf("pesan harus menyebut nama group: %v", err)
	}
}

func TestIntegration_RequesterWhoIsLeaderAutoApproves(t *testing.T) {
	e := newEnv(t)
	_, approvals, err := e.request(t, e.leadA.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range approvals {
		wantAuto := a.GroupID == e.groupA
		if a.Auto != wantAuto || (a.Status == domain.ApprovalApproved) != wantAuto {
			t.Errorf("group %d: %+v", a.GroupID, a)
		}
	}
}

func TestIntegration_ReviewFlow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	execID, _, err := e.request(t, e.editor.ID)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.Review(ctx, principal(e.leadB), execID, e.groupA, true, ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("leader group lain: err = %v", err)
	}
	if _, err := e.svc.Review(ctx, principal(e.admin), execID, e.groupA, true, ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("admin non-leader: err = %v", err)
	}

	status, err := e.svc.Review(ctx, principal(e.leadA), execID, e.groupA, true, "")
	if err != nil || status != domain.ExecPendingReview {
		t.Fatalf("satu dari dua setuju: %v %v", status, err)
	}
	if _, err := e.svc.Review(ctx, principal(e.leadA), execID, e.groupA, true, ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("keputusan ganda: err = %v", err)
	}

	status, err = e.svc.Review(ctx, principal(e.leadB), execID, e.groupB, true, "")
	if err != nil || status != domain.ExecApproved || e.status(t, execID) != domain.ExecApproved {
		t.Fatalf("semua setuju: %v %v", status, err)
	}
}

func TestIntegration_RejectRequiresCommentAndRejectsExecution(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	execID, _, err := e.request(t, e.editor.ID)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.Review(ctx, principal(e.leadB), execID, e.groupB, false, "  "); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("tolak tanpa komentar: err = %v", err)
	}
	status, err := e.svc.Review(ctx, principal(e.leadB), execID, e.groupB, false, "berisiko")
	if err != nil || status != domain.ExecRejected {
		t.Fatalf("tolak: %v %v", status, err)
	}
	if _, err := e.svc.Review(ctx, principal(e.leadA), execID, e.groupA, true, ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("eksekusi yang sudah ditolak tidak bisa disetujui: %v", err)
	}
}

func TestIntegration_InboxAndRequiredNow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	execID, _, err := e.request(t, e.editor.ID)
	if err != nil {
		t.Fatal(err)
	}

	items, err := e.svc.Inbox(ctx, e.leadB.ID)
	if err != nil || len(items) != 1 || items[0].Execution.ID != execID || items[0].Database != "sampledb" {
		t.Fatalf("inbox lead-b: %+v %v", items, err)
	}
	if items, _ := e.svc.Inbox(ctx, e.editor.ID); len(items) != 0 {
		t.Fatalf("bukan leader, inbox harus kosong: %+v", items)
	}

	// kolomnya diisi langsung karena yang diuji cuma apakah inbox ikut membacanya
	if items[0].Execution.ResumesExecutionID != nil {
		t.Fatalf("permintaan biasa tidak boleh ditandai resume: %+v", items[0].Execution)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE migration_executions SET resumes_execution_id = id WHERE id = $1`, execID); err != nil {
		t.Fatal(err)
	}
	resumed, err := e.svc.Inbox(ctx, e.leadB.ID)
	if err != nil || len(resumed) != 1 || resumed[0].Execution.ResumesExecutionID == nil {
		t.Fatalf("inbox harus menandai resume: %+v %v", resumed, err)
	}

	groups, err := e.svc.RequiredNow(ctx, e.pool, e.connID)
	if err != nil || len(groups) != 2 {
		t.Fatalf("RequiredNow: %+v %v", groups, err)
	}
}
