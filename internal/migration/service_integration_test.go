package migration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/access"
	"dbplatform/internal/domain"
	"dbplatform/internal/migration"
	"dbplatform/internal/testutil"
)

type svcEnv struct {
	pool *pgxpool.Pool
	svc  *migration.Service
	repo *migration.Repo
}

func newSvcEnv(t *testing.T) svcEnv {
	t.Helper()
	pool := testutil.NewAppDB(t)
	repo := migration.NewRepo(pool)
	return svcEnv{pool: pool, repo: repo, svc: migration.NewService(repo, access.New(pool))}
}

func (e svcEnv) actor(t *testing.T, username string, admin bool) domain.Principal {
	t.Helper()
	u := testutil.SeedUser(t, e.pool, username, admin)
	return domain.Principal{UserID: u.ID, Username: username, IsPlatformAdmin: admin}
}

func (e svcEnv) give(t *testing.T, p domain.Principal, connID int64, level domain.AccessLevel) {
	t.Helper()
	g := testutil.SeedGroup(t, e.pool, p.Username+"-grup", p.UserID)
	testutil.Grant(t, e.pool, g, connID, level)
}

func (e svcEnv) rowCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM db_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

var valid = migration.Input{Name: "tambah tabel", SQL: "CREATE TABLE a (id int)"}

func TestService_Integration_EditorCanManageDrafts(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newSvcEnv(t)
	conn := testutil.SeedConnection(t, e.pool, "sample", true)
	editor := e.actor(t, "penyunting", false)
	e.give(t, editor, conn, domain.AccessViewEdit)

	m, err := e.svc.Create(ctx, editor, conn, valid)
	if err != nil || m.Version != 1 || m.Status != domain.MigrationDraft || m.CreatedByName != "penyunting" {
		t.Fatalf("Create = %+v, %v", m, err)
	}

	updated, err := e.svc.Update(ctx, editor, conn, 1, migration.Input{Name: "diubah", SQL: "CREATE TABLE b (id int)"})
	if err != nil || updated.Name != "diubah" || updated.Checksum != migration.Checksum("CREATE TABLE b (id int)") {
		t.Fatalf("Update = %+v, %v", updated, err)
	}

	if err := e.svc.Cancel(ctx, editor, conn, 1); err != nil {
		t.Fatal(err)
	}
	got, _ := e.svc.Get(ctx, editor, conn, 1)
	if got.Status != domain.MigrationCancelled {
		t.Errorf("status = %s, mau cancelled", got.Status)
	}
	if _, err := e.svc.Update(ctx, editor, conn, 1, valid); !errors.Is(err, domain.ErrMigrationNotDraft) {
		t.Errorf("mengubah yang sudah dibatalkan: err = %v, mau ErrMigrationNotDraft", err)
	}
}

func TestService_Integration_ViewOnlyCanReadButNotWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newSvcEnv(t)
	conn := testutil.SeedConnection(t, e.pool, "sample", true)
	editor := e.actor(t, "penyunting", false)
	reader := e.actor(t, "pembaca", false)
	e.give(t, editor, conn, domain.AccessViewEdit)
	e.give(t, reader, conn, domain.AccessViewOnly)
	m, err := e.svc.Create(ctx, editor, conn, valid)
	if err != nil {
		t.Fatal(err)
	}

	list, err := e.svc.List(ctx, reader, conn)
	if err != nil || len(list) != 1 {
		t.Errorf("view_only harus bisa melihat riwayat: %v, %v", list, err)
	}
	if got, err := e.svc.Get(ctx, reader, conn, m.Version); err != nil || got.SQL != valid.SQL {
		t.Errorf("view_only harus bisa melihat detail: %+v, %v", got, err)
	}

	before := e.rowCount(t)
	if _, err := e.svc.Create(ctx, reader, conn, valid); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("Create: err = %v", err)
	}
	if _, err := e.svc.Update(ctx, reader, conn, m.Version, migration.Input{Name: "retas", SQL: "DROP TABLE a"}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("Update: err = %v", err)
	}
	if err := e.svc.Cancel(ctx, reader, conn, m.Version); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("Cancel: err = %v", err)
	}
	if e.rowCount(t) != before {
		t.Error("jumlah baris tidak boleh berubah")
	}
	after, _ := e.repo.Get(ctx, conn, m.Version)
	if after.Name != valid.Name || after.SQL != valid.SQL || after.Status != domain.MigrationDraft {
		t.Errorf("data tidak boleh berubah oleh view_only: %+v", after)
	}
}

func TestService_Integration_NoAccessIsDeniedIncludingAdmins(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newSvcEnv(t)
	conn := testutil.SeedConnection(t, e.pool, "sample", true)
	editor := e.actor(t, "penyunting", false)
	e.give(t, editor, conn, domain.AccessViewEdit)
	m, _ := e.svc.Create(ctx, editor, conn, valid)

	loner := e.actor(t, "sendirian", false)
	admin := e.actor(t, "root", true)
	for name, actor := range map[string]domain.Principal{"user tanpa group": loner, "Platform Admin tanpa group": admin} {
		t.Run(name, func(t *testing.T) {
			if _, err := e.svc.List(ctx, actor, conn); !errors.Is(err, domain.ErrForbidden) {
				t.Errorf("List: err = %v", err)
			}
			if _, err := e.svc.Get(ctx, actor, conn, m.Version); !errors.Is(err, domain.ErrForbidden) {
				t.Errorf("Get: err = %v", err)
			}
			if _, err := e.svc.Get(ctx, actor, conn, 999); !errors.Is(err, domain.ErrForbidden) {
				t.Errorf("Get versi tak ada: err = %v, mau ErrForbidden (tidak membocorkan keberadaan)", err)
			}
			if _, err := e.svc.Create(ctx, actor, conn, valid); !errors.Is(err, domain.ErrForbidden) {
				t.Errorf("Create: err = %v", err)
			}
		})
	}
}

func TestService_Integration_HighestLevelAcrossGroupsWins(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newSvcEnv(t)
	conn := testutil.SeedConnection(t, e.pool, "sample", true)
	user := e.actor(t, "ganda", false)

	readers := testutil.SeedGroup(t, e.pool, "pembaca", user.UserID)
	testutil.Grant(t, e.pool, readers, conn, domain.AccessViewOnly)
	if _, err := e.svc.Create(ctx, user, conn, valid); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("hanya view_only: err = %v", err)
	}

	editors := testutil.SeedGroup(t, e.pool, "penyunting", user.UserID)
	testutil.Grant(t, e.pool, editors, conn, domain.AccessViewEdit)
	if _, err := e.svc.Create(ctx, user, conn, valid); err != nil {
		t.Errorf("view_only dan view_edit di dua group: level tertinggi menang, err = %v", err)
	}
}

func TestService_Integration_AccessDoesNotCrossConnections(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newSvcEnv(t)
	connA := testutil.SeedConnection(t, e.pool, "a", true)
	connB := testutil.SeedConnection(t, e.pool, "b", true)
	editorA := e.actor(t, "penyunting-a", false)
	editorB := e.actor(t, "penyunting-b", false)
	e.give(t, editorA, connA, domain.AccessViewEdit)
	e.give(t, editorB, connB, domain.AccessViewEdit)
	mB, err := e.svc.Create(ctx, editorB, connB, valid)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.Create(ctx, editorA, connB, valid); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("editor A membuat di koneksi B: err = %v", err)
	}
	if _, err := e.svc.Get(ctx, editorA, connB, mB.Version); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("editor A membaca koneksi B: err = %v", err)
	}
	if _, err := e.svc.Get(ctx, editorA, connA, mB.Version); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("memakai nomor versi milik B lewat jalur koneksi A: err = %v, mau ErrNotFound (IDOR)", err)
	}
	if _, err := e.svc.Update(ctx, editorA, connA, mB.Version, migration.Input{Name: "retas", SQL: "DROP TABLE x"}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Update lintas koneksi: err = %v, mau ErrNotFound", err)
	}
	if err := e.svc.Cancel(ctx, editorA, connA, mB.Version); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Cancel lintas koneksi: err = %v, mau ErrNotFound", err)
	}
	if got, _ := e.repo.Get(ctx, connB, mB.Version); got.Name != valid.Name || got.Status != domain.MigrationDraft {
		t.Errorf("migration koneksi B tidak boleh berubah: %+v", got)
	}
}

func TestService_Integration_InactiveConnectionAndInactiveUserLoseAccess(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newSvcEnv(t)
	conn := testutil.SeedConnection(t, e.pool, "sample", true)
	editor := e.actor(t, "penyunting", false)
	e.give(t, editor, conn, domain.AccessViewEdit)
	if _, err := e.svc.Create(ctx, editor, conn, valid); err != nil {
		t.Fatal(err)
	}

	testutil.SetUserActive(t, e.pool, editor.UserID, false)
	if _, err := e.svc.List(ctx, editor, conn); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("user nonaktif: err = %v", err)
	}
	testutil.SetUserActive(t, e.pool, editor.UserID, true)

	if _, err := e.pool.Exec(ctx, `UPDATE connections SET is_active = FALSE WHERE id = $1`, conn); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Create(ctx, editor, conn, valid); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("koneksi nonaktif: err = %v", err)
	}
}

func TestService_Integration_ValidationAndCreateForUnknownUserOrConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newSvcEnv(t)
	conn := testutil.SeedConnection(t, e.pool, "sample", true)
	editor := e.actor(t, "penyunting", false)
	e.give(t, editor, conn, domain.AccessViewEdit)

	_, err := e.svc.Create(ctx, editor, conn, migration.Input{Name: "x", SQL: "CREATE TABLE a (id int); COMMIT;"})
	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Fields["sql"] == "" {
		t.Fatalf("err = %v, mau error validasi pada sql", err)
	}
	if e.rowCount(t) != 0 {
		t.Error("migration tidak valid tidak boleh tersimpan")
	}
}
