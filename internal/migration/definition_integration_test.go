package migration_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"dbplatform/internal/access"
	"dbplatform/internal/domain"
	"dbplatform/internal/migration"
	"dbplatform/internal/testutil"
)

type serviceEnv struct {
	repoEnv
	service  *migration.DefinitionService
	editor   domain.Principal
	viewer   domain.Principal
	outsider domain.Principal
}

// otorisasi memakai implementasi asli supaya aturan group ikut teruji
func newServiceEnv(t *testing.T) serviceEnv {
	t.Helper()
	e := newRepoEnv(t)
	viewer := testutil.SeedUser(t, e.pool, "vina", false)
	outsider := testutil.SeedUser(t, e.pool, "oki", false)
	testutil.Grant(t, e.pool, testutil.SeedGroup(t, e.pool, "dev", e.user.ID), e.conn, domain.AccessViewEdit)
	testutil.Grant(t, e.pool, testutil.SeedGroup(t, e.pool, "analis", viewer.ID), e.conn, domain.AccessViewOnly)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	principal := func(u domain.User) domain.Principal {
		return domain.Principal{UserID: u.ID, Username: u.Username}
	}
	return serviceEnv{
		repoEnv:  e,
		service:  migration.NewDefinitionService(e.repo, access.New(e.pool), logger),
		editor:   principal(e.user),
		viewer:   principal(viewer),
		outsider: principal(outsider),
	}
}

// semua baris ikut berubah
func (e serviceEnv) setState(t *testing.T, state domain.MigrationState) {
	t.Helper()
	testutil.MustExec(t, e.pool, "UPDATE db_migrations SET state = '"+string(state)+"'")
}

func wantField(t *testing.T, err error, field string) {
	t.Helper()
	var invalid *domain.ValidationError
	if !errors.As(err, &invalid) || invalid.Fields[field] == "" {
		t.Errorf("error = %v, mau pesan di field %s", err, field)
	}
}

func TestDefinitionService_Integration_Access(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	e := newServiceEnv(t)
	ctx := context.Background()

	d, err := e.service.Create(ctx, e.editor, e.conn, normalInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.Create(ctx, e.viewer, e.conn, normalInput()); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("view only membuat draft: %v", err)
	}
	if _, err := e.service.Freeze(ctx, e.viewer, e.conn, d.Version, d.Revision); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("view only freeze: %v", err)
	}
	if _, err := e.service.Get(ctx, e.viewer, e.conn, d.Version); err != nil {
		t.Errorf("view only harus bisa membaca: %v", err)
	}
	if _, err := e.service.List(ctx, e.outsider, e.conn); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("orang luar melihat daftar: %v", err)
	}
	// akses dicek lebih dulu, jadi orang luar tidak mendapat pesan validasi
	if _, err := e.service.Create(ctx, e.outsider, e.conn, migration.DraftInput{}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("orang luar dengan input kosong: %v", err)
	}
}

func TestDefinitionService_Integration_DraftToReady(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	e := newServiceEnv(t)
	ctx := context.Background()

	_, err := e.service.Create(ctx, e.editor, e.conn, migration.DraftInput{UpSQL: "SELECT 1"})
	wantField(t, err, "name")

	d, err := e.service.Create(ctx, e.editor, e.conn, normalInput())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.service.Freeze(ctx, e.editor, e.conn, d.Version, d.Revision)
	wantField(t, err, "sql_down")

	complete := normalInput()
	complete.DownSQL = "DROP TABLE a"
	updated, err := e.service.Update(ctx, e.editor, e.conn, d.Version, d.Revision, complete)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.Freeze(ctx, e.editor, e.conn, d.Version, d.Revision); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("freeze dengan revisi basi: %v", err)
	}
	ready, err := e.service.Freeze(ctx, e.editor, e.conn, d.Version, updated.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if ready.State != domain.StateReady {
		t.Errorf("status = %s", ready.State)
	}
	if _, err := e.service.Update(ctx, e.editor, e.conn, d.Version, ready.Revision, complete); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("ubah yang sudah beku: %v", err)
	}
	if _, err := e.service.Cancel(ctx, e.editor, e.conn, d.Version); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("batalkan yang sudah beku: %v", err)
	}

	reopened, err := e.service.Reopen(ctx, e.editor, e.conn, d.Version)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.State != domain.StateDraft || reopened.Revision != ready.Revision+1 {
		t.Errorf("dibuka lagi = %s revisi %d", reopened.State, reopened.Revision)
	}
	cancelled, err := e.service.Cancel(ctx, e.editor, e.conn, d.Version)
	if err != nil || cancelled.State != domain.StateCancelled {
		t.Errorf("batalkan = %s, %v", cancelled.State, err)
	}
}

func TestDefinitionService_Integration_OpenRequestBlocksChange(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	e := newServiceEnv(t)
	ctx := context.Background()
	d := e.createDefinition(t, normalInput())
	e.setState(t, domain.StateFailed)

	_, err := e.pool.Exec(ctx, `
		INSERT INTO migration_executions
			(migration_id, connection_id, version, revision, kind, mode, sql_snapshot, checksum, requested_by)
		VALUES ($1, $2, $3, 1, 'forward', 'normal', 'SELECT 1', 'x', $4)`,
		d.ID, e.conn, d.Version, e.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.Supersede(ctx, e.editor, e.conn, d.Version); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("supersede saat ada permintaan: %v", err)
	}
	// daftar harus memberi tahu bahwa ada permintaan yang masih menunggu
	list, err := e.service.List(ctx, e.viewer, e.conn)
	if err != nil || len(list) != 1 || list[0].OpenStatus != domain.ExecPendingReview {
		t.Errorf("daftar = %+v, %v; mau status pending_review", list, err)
	}

	testutil.MustExec(t, e.pool, "UPDATE migration_executions SET status = 'rejected'")
	got, err := e.service.Supersede(ctx, e.editor, e.conn, d.Version)
	if err != nil || got.State != domain.StateSuperseded || got.OpenStatus != "" {
		t.Errorf("supersede = %s %q, %v", got.State, got.OpenStatus, err)
	}
}
