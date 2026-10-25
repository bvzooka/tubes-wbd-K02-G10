package schemadiff_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/access"
	"dbplatform/internal/clock"
	"dbplatform/internal/dbdriver/postgres"
	"dbplatform/internal/domain"
	"dbplatform/internal/migration"
	"dbplatform/internal/schemadiff"
	"dbplatform/internal/testutil"
)

type diffEnv struct {
	app        *pgxpool.Pool
	target     *pgxpool.Pool
	source     *pgxpool.Pool
	svc        *schemadiff.Service
	migrations *migration.Service
	targetID   int64
	sourceID   int64
	editor     domain.Principal
}

func newDiffEnv(t *testing.T) diffEnv {
	t.Helper()
	app := testutil.NewAppDB(t)
	e := diffEnv{
		app:      app,
		target:   testutil.NewTargetDB(t, oldSchemaSQL),
		source:   testutil.NewTargetDB(t, newSchemaSQL),
		targetID: testutil.SeedConnection(t, app, "tujuan", true),
		sourceID: testutil.SeedConnection(t, app, "sumber", true),
	}
	drivers := testutil.NewFakeDriverProvider()
	drivers.Set(e.targetID, postgres.NewFromPool(e.target))
	drivers.Set(e.sourceID, postgres.NewFromPool(e.source))

	authz := access.New(app)
	e.migrations = migration.NewService(migration.NewRepo(app), authz, drivers, clock.System{})
	e.svc = schemadiff.NewService(authz, drivers, e.migrations)
	e.editor = e.user(t, "penyunting", map[int64]domain.AccessLevel{e.targetID: domain.AccessViewEdit, e.sourceID: domain.AccessViewOnly})
	return e
}

func (e diffEnv) user(t *testing.T, name string, grants map[int64]domain.AccessLevel) domain.Principal {
	t.Helper()
	u := testutil.SeedUser(t, e.app, name, false)
	g := testutil.SeedGroup(t, e.app, name+"-grup", u.ID)
	for conn, level := range grants {
		testutil.Grant(t, e.app, g, conn, level)
	}
	return domain.Principal{UserID: u.ID, Username: name}
}

func (e diffEnv) draftCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.app.QueryRow(context.Background(), `SELECT count(*) FROM db_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func ids(changes []schemadiff.Change) []string {
	out := make([]string, len(changes))
	for i, c := range changes {
		out[i] = c.ID
	}
	return out
}

func TestService_Integration_PreviewDraftExecuteConverges(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newDiffEnv(t)
	sourceSchema := introspect(t, e.source)

	preview, err := e.svc.Preview(ctx, e.editor, e.targetID, e.sourceID)
	if err != nil || len(preview.Changes) == 0 {
		t.Fatalf("preview = %v, %v", preview, err)
	}
	if preview.SourceName != "sumber" || preview.TargetName != "tujuan" {
		t.Errorf("nama = %q dan %q", preview.SourceName, preview.TargetName)
	}

	safe := nonDestructive(preview.Changes)
	draft, err := e.svc.CreateDraft(ctx, e.editor, e.targetID, e.sourceID, ids(safe))
	if err != nil {
		t.Fatal(err)
	}
	if draft.Status != domain.MigrationDraft || draft.Name != "Schema diff dari sumber" || draft.ConnectionID != e.targetID || draft.CreatedByName != "penyunting" {
		t.Errorf("draft = %+v", draft)
	}
	if draft.SQL != schemadiff.SQL(safe) {
		t.Error("SQL draft harus sama dengan SQL perubahan yang dipilih")
	}
	if rowCount(t, e.target, "people") != 2 {
		t.Fatal("membuat draft tidak boleh mengubah target")
	}

	done, err := e.migrations.Execute(ctx, e.editor, e.targetID, draft.Version)
	if err != nil || done.Status != domain.MigrationSuccess {
		t.Fatalf("eksekusi draft: %+v, %v", done, err)
	}

	rest, err := e.svc.Preview(ctx, e.editor, e.targetID, e.sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest.Changes) != 2 {
		t.Fatalf("sisa = %v, mau drop_column dan drop_table saja", kindsOf(rest.Changes))
	}
	for _, c := range rest.Changes {
		if !c.Destructive {
			t.Errorf("sisa non-destruktif: %s %s", c.Kind, c.Object)
		}
	}

	second, err := e.svc.CreateDraft(ctx, e.editor, e.targetID, e.sourceID, ids(rest.Changes))
	if err != nil || second.Version != 2 {
		t.Fatalf("draft kedua = %+v, %v", second, err)
	}
	if done, err := e.migrations.Execute(ctx, e.editor, e.targetID, second.Version); err != nil || done.Status != domain.MigrationSuccess {
		t.Fatalf("eksekusi draft kedua: %+v, %v", done, err)
	}
	assertConverged(t, e.target, sourceSchema)
}

func TestService_Integration_AccessRulesWithRealAuthorizer(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newDiffEnv(t)
	preview, _ := e.svc.Preview(ctx, e.editor, e.targetID, e.sourceID)
	chosen := ids(preview.Changes[:1])

	reader := e.user(t, "pembaca", map[int64]domain.AccessLevel{e.targetID: domain.AccessViewOnly, e.sourceID: domain.AccessViewOnly})
	noSource := e.user(t, "tanpa-sumber", map[int64]domain.AccessLevel{e.targetID: domain.AccessViewEdit})
	loner := e.user(t, "sendirian", nil)
	root := testutil.SeedUser(t, e.app, "root", true)
	admin := domain.Principal{UserID: root.ID, IsPlatformAdmin: true}

	for name, actor := range map[string]domain.Principal{"view_only di target": reader, "tanpa akses ke sumber": noSource, "tanpa group": loner, "Platform Admin tanpa group": admin} {
		if _, err := e.svc.Preview(ctx, actor, e.targetID, e.sourceID); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("%s: Preview err = %v", name, err)
		}
		if _, err := e.svc.CreateDraft(ctx, actor, e.targetID, e.sourceID, chosen); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("%s: CreateDraft err = %v", name, err)
		}
		list, err := e.svc.Sources(ctx, actor, e.targetID)
		if actor.UserID == noSource.UserID {
			if err != nil || len(list) != 0 {
				t.Errorf("%s: Sources = %v, %v; mau daftar kosong tanpa error (berhak di target, tidak ada sumber yang boleh dibaca)", name, list, err)
			}
		} else if !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("%s: Sources err = %v", name, err)
		}
	}
	if e.draftCount(t) != 0 {
		t.Error("tidak boleh ada draft yang tersimpan untuk yang tidak berhak")
	}

	sources, err := e.svc.Sources(ctx, e.editor, e.targetID)
	if err != nil || len(sources) != 1 || sources[0].ConnectionID != e.sourceID {
		t.Errorf("sumber = %+v, %v; mau hanya database sumber (bukan target)", sources, err)
	}
	if _, err := e.svc.Preview(ctx, e.editor, e.targetID, e.targetID); err == nil {
		t.Error("sumber sama dengan target harus ditolak")
	}
}

func TestService_Integration_ChoiceBecomesStaleOnceItIsApplied(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newDiffEnv(t)
	preview, _ := e.svc.Preview(ctx, e.editor, e.targetID, e.sourceID)

	apply(t, e.target, preview.Changes[:1])

	_, err := e.svc.CreateDraft(ctx, e.editor, e.targetID, e.sourceID, ids(preview.Changes[:1]))
	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Fields["changes"] == "" {
		t.Errorf("err = %v, mau pilihan kedaluwarsa karena sudah diterapkan", err)
	}
	if e.draftCount(t) != 0 {
		t.Error("tidak boleh ada draft")
	}
}

func TestService_Integration_InconsistentPartialSelectionFailsSafelyAndRollsBack(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newDiffEnv(t)
	preview, _ := e.svc.Preview(ctx, e.editor, e.targetID, e.sourceID)

	var fkOnly []schemadiff.Change
	for _, c := range preview.Changes {
		if c.Kind == schemadiff.KindAddConstraint && strings.HasSuffix(c.Object, "visits_pet_id_fkey") {
			fkOnly = append(fkOnly, c)
		}
	}
	if len(fkOnly) != 1 {
		t.Fatalf("add_constraint visits_pet_id_fkey = %d, mau 1; semua: %v", len(fkOnly), kindsOf(preview.Changes))
	}

	draft, err := e.svc.CreateDraft(ctx, e.editor, e.targetID, e.sourceID, ids(fkOnly))
	if err != nil {
		t.Fatal(err)
	}
	done, err := e.migrations.Execute(ctx, e.editor, e.targetID, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != domain.MigrationFailed || done.ErrorMessage == "" {
		t.Errorf("FK tanpa tabelnya harus gagal dan tercatat: %+v", done)
	}
	if left := nonDestructive(preview.Changes); len(left) == 0 {
		t.Fatal("seharusnya masih ada perubahan")
	}
	again, _ := e.svc.Preview(ctx, e.editor, e.targetID, e.sourceID)
	if len(again.Changes) != len(preview.Changes) {
		t.Errorf("target berubah padahal eksekusi gagal: %d perubahan sebelum, %d sesudah", len(preview.Changes), len(again.Changes))
	}
}

func TestService_Integration_DraftIsNotExecutedAutomaticallyAndPassesMigrationValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newDiffEnv(t)
	preview, _ := e.svc.Preview(ctx, e.editor, e.targetID, e.sourceID)

	draft, err := e.svc.CreateDraft(ctx, e.editor, e.targetID, e.sourceID, ids(preview.Changes))
	if err != nil {
		t.Fatal(err)
	}
	if msg := migration.CheckSQL(draft.SQL); msg != "" {
		t.Errorf("SQL hasil diff harus lolos validasi migration: %s", msg)
	}
	var status string
	if err := e.app.QueryRow(ctx, `SELECT status FROM db_migrations WHERE version = $1`, draft.Version).Scan(&status); err != nil || status != "draft" {
		t.Errorf("status = %q, err = %v, mau draft", status, err)
	}
}
