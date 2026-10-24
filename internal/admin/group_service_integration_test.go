package admin_test

import (
	"context"
	"errors"
	"testing"

	"dbplatform/internal/access"
	"dbplatform/internal/admin"
	"dbplatform/internal/domain"
	"dbplatform/internal/testutil"
)

func TestGroupService_Integration_GrantsBecomeEffectiveAccess(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	pool := testutil.NewAppDB(t)
	spy := &testutil.SpySessionAdmin{}
	svc := admin.NewGroupService(admin.NewGroupRepo(pool), spy)
	authz := access.New(pool)

	adminUser := testutil.SeedUser(t, pool, "root", true)
	actor := domain.Principal{UserID: adminUser.ID, Username: "root", IsPlatformAdmin: true}
	budi := testutil.SeedUser(t, pool, "budi", false)
	conn := testutil.SeedConnection(t, pool, "sample", true)

	level := func() domain.AccessLevel {
		t.Helper()
		l, err := authz.EffectiveLevel(ctx, budi.ID, conn)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}

	readers, err := svc.CreateGroup(ctx, actor, admin.GroupInput{Name: "Pembaca"})
	if err != nil {
		t.Fatal(err)
	}
	editors, err := svc.CreateGroup(ctx, actor, admin.GroupInput{Name: "Penyunting"})
	if err != nil {
		t.Fatal(err)
	}

	if level() != domain.AccessNone {
		t.Fatalf("sebelum apa pun, level = %q, mau none", level())
	}

	if err := svc.GrantAccess(ctx, actor, readers.ID, conn, domain.AccessViewOnly); err != nil {
		t.Fatal(err)
	}
	if level() != domain.AccessNone {
		t.Errorf("group diberi akses tapi budi belum anggota, level = %q, mau none", level())
	}

	if err := svc.AddMember(ctx, actor, readers.ID, budi.ID); err != nil {
		t.Fatal(err)
	}
	if level() != domain.AccessViewOnly {
		t.Errorf("level = %q, mau view_only", level())
	}
	if r := spy.RotationMarked(); len(r) != 1 || r[0] != budi.ID {
		t.Errorf("menambah anggota harus menandai rotasi budi, dapat %v", r)
	}

	if err := svc.AddMember(ctx, actor, editors.ID, budi.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.GrantAccess(ctx, actor, editors.ID, conn, domain.AccessViewEdit); err != nil {
		t.Fatal(err)
	}
	if level() != domain.AccessViewEdit {
		t.Errorf("dua group, level tertinggi menang: level = %q, mau view_edit", level())
	}

	before := len(spy.RotationMarked())
	if err := svc.GrantAccess(ctx, actor, editors.ID, conn, domain.AccessViewOnly); err != nil {
		t.Fatal(err)
	}
	if len(spy.RotationMarked()) != before {
		t.Error("menurunkan level tidak boleh menandai rotasi")
	}
	if level() != domain.AccessViewOnly {
		t.Errorf("setelah diturunkan, level = %q, mau view_only", level())
	}

	if err := svc.GrantAccess(ctx, actor, editors.ID, conn, domain.AccessViewEdit); err != nil {
		t.Fatal(err)
	}
	if len(spy.RotationMarked()) != before+1 {
		t.Errorf("menaikkan level harus menandai rotasi anggota: %v", spy.RotationMarked())
	}

	if err := svc.RevokeAccess(ctx, actor, editors.ID, conn); err != nil {
		t.Fatal(err)
	}
	if level() != domain.AccessViewOnly {
		t.Errorf("akses editors dicabut, level = %q, mau view_only", level())
	}

	if err := svc.RemoveMember(ctx, actor, readers.ID, budi.ID); err != nil {
		t.Fatal(err)
	}
	if level() != domain.AccessNone {
		t.Errorf("budi keluar dari semua group yang berakses, level = %q, mau none", level())
	}
}

func TestGroupService_Integration_InactiveConnectionAndUserGetNoAccess(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	pool := testutil.NewAppDB(t)
	svc := admin.NewGroupService(admin.NewGroupRepo(pool), &testutil.SpySessionAdmin{})
	authz := access.New(pool)

	adminUser := testutil.SeedUser(t, pool, "root", true)
	actor := domain.Principal{UserID: adminUser.ID, IsPlatformAdmin: true}
	budi := testutil.SeedUser(t, pool, "budi", false)
	conn := testutil.SeedConnection(t, pool, "sample", true)
	g, _ := svc.CreateGroup(ctx, actor, admin.GroupInput{Name: "Tim"})

	if err := svc.AddMember(ctx, actor, g.ID, budi.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.GrantAccess(ctx, actor, g.ID, conn, domain.AccessViewEdit); err != nil {
		t.Fatal(err)
	}

	testutil.SetUserActive(t, pool, budi.ID, false)
	if l, _ := authz.EffectiveLevel(ctx, budi.ID, conn); l != domain.AccessNone {
		t.Errorf("user nonaktif, level = %q, mau none", l)
	}
	testutil.SetUserActive(t, pool, budi.ID, true)

	inactive := testutil.SeedConnection(t, pool, "mati", false)
	err := svc.GrantAccess(ctx, actor, g.ID, inactive, domain.AccessViewOnly)
	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Fields["connection_id"] == "" {
		t.Errorf("koneksi nonaktif tidak boleh diberi akses: err = %v", err)
	}
}

func TestGroupService_Integration_DeleteFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	pool := testutil.NewAppDB(t)
	svc := admin.NewGroupService(admin.NewGroupRepo(pool), &testutil.SpySessionAdmin{})

	adminUser := testutil.SeedUser(t, pool, "root", true)
	actor := domain.Principal{UserID: adminUser.ID, IsPlatformAdmin: true}
	budi := testutil.SeedUser(t, pool, "budi", false)
	conn := testutil.SeedConnection(t, pool, "sample", true)
	g, _ := svc.CreateGroup(ctx, actor, admin.GroupInput{Name: "Tim"})
	_ = svc.AddMember(ctx, actor, g.ID, budi.ID)
	_ = svc.GrantAccess(ctx, actor, g.ID, conn, domain.AccessViewOnly)

	d, err := svc.GroupDetail(ctx, actor, g.ID)
	if err != nil || d.CanDelete || len(d.Members) != 1 || len(d.Accesses) != 1 {
		t.Fatalf("detail = %+v, err = %v", d, err)
	}
	if len(d.AvailableUsers) != 1 || d.AvailableUsers[0].Username != "root" {
		t.Errorf("user tersedia = %+v, mau hanya root (budi sudah anggota)", d.AvailableUsers)
	}
	if len(d.AvailableConnections) != 1 {
		t.Errorf("koneksi tersedia = %+v", d.AvailableConnections)
	}

	if err := svc.DeleteGroup(ctx, actor, g.ID); !errors.Is(err, admin.ErrGroupNotEmpty) {
		t.Fatalf("err = %v, mau ErrGroupNotEmpty", err)
	}
	_ = svc.RemoveMember(ctx, actor, g.ID, budi.ID)
	_ = svc.RevokeAccess(ctx, actor, g.ID, conn)

	d, _ = svc.GroupDetail(ctx, actor, g.ID)
	if !d.CanDelete {
		t.Fatal("group kosong harus bisa dihapus")
	}
	if err := svc.DeleteGroup(ctx, actor, g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GroupDetail(ctx, actor, g.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("setelah dihapus: err = %v, mau ErrNotFound", err)
	}
}

func TestGroupService_Integration_DuplicateNameIsFieldError(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	pool := testutil.NewAppDB(t)
	svc := admin.NewGroupService(admin.NewGroupRepo(pool), &testutil.SpySessionAdmin{})
	actor := domain.Principal{UserID: 1, IsPlatformAdmin: true}

	if _, err := svc.CreateGroup(ctx, actor, admin.GroupInput{Name: "Tim Data"}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.CreateGroup(ctx, actor, admin.GroupInput{Name: "  TIM DATA "})
	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Fields["name"] == "" {
		t.Fatalf("err = %v, mau error validasi pada name", err)
	}
}
