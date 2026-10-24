package admin_test

import (
	"context"
	"errors"
	"testing"

	"dbplatform/internal/admin"
	"dbplatform/internal/domain"
	"dbplatform/internal/testutil"
)

func TestGroupRepo_Integration_CreateGetAndList(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	repo := admin.NewGroupRepo(testutil.NewAppDB(t))

	g, err := repo.Create(ctx, "Tim Data", "analis")
	if err != nil {
		t.Fatal(err)
	}
	if g.ID == 0 || g.CreatedAt.IsZero() || g.Name != "Tim Data" || g.Description != "analis" {
		t.Errorf("group = %+v", g)
	}

	got, err := repo.Get(ctx, g.ID)
	if err != nil || got.Name != "Tim Data" || got.Description != "analis" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if _, err := repo.Get(ctx, 424242); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get tak ada: err = %v", err)
	}
}

func TestGroupRepo_Integration_ListOrderAndCounts(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	pool := testutil.NewAppDB(t)
	repo := admin.NewGroupRepo(pool)

	u1 := testutil.SeedUser(t, pool, "ani", false)
	u2 := testutil.SeedUser(t, pool, "budi", false)
	conn := testutil.SeedConnection(t, pool, "sample", true)
	zeta := testutil.SeedGroup(t, pool, "Zeta", u1.ID, u2.ID)
	testutil.SeedGroup(t, pool, "alfa")
	testutil.Grant(t, pool, zeta, conn, domain.AccessViewOnly)

	groups, err := repo.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0].Name != "alfa" || groups[1].Name != "Zeta" {
		t.Fatalf("urutan harus tak peduli huruf besar-kecil: %+v", groups)
	}
	if groups[0].MemberCount != 0 || groups[0].AccessCount != 0 {
		t.Errorf("alfa: %+v", groups[0])
	}
	if groups[1].MemberCount != 2 || groups[1].AccessCount != 1 {
		t.Errorf("Zeta: anggota = %d, akses = %d, mau 2 dan 1", groups[1].MemberCount, groups[1].AccessCount)
	}
}

func TestGroupRepo_Integration_DuplicateNameIsCaseInsensitive(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	repo := admin.NewGroupRepo(testutil.NewAppDB(t))

	if _, err := repo.Create(ctx, "Tim Data", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(ctx, "tim data", ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, mau ErrConflict", err)
	}
}

func TestGroupRepo_Integration_Update(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	repo := admin.NewGroupRepo(testutil.NewAppDB(t))
	a, _ := repo.Create(ctx, "Alfa", "")
	b, _ := repo.Create(ctx, "Beta", "")

	got, err := repo.Update(ctx, a.ID, "Alfa Baru", "deskripsi baru")
	if err != nil || got.Name != "Alfa Baru" || got.Description != "deskripsi baru" || got.CreatedAt.IsZero() {
		t.Fatalf("Update = %+v, %v", got, err)
	}
	if _, err := repo.Update(ctx, a.ID, "Alfa Baru", "hanya deskripsi berubah"); err != nil {
		t.Errorf("memakai nama sendiri harus boleh: %v", err)
	}
	if _, err := repo.Update(ctx, b.ID, "ALFA BARU", ""); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("nama milik group lain: err = %v, mau ErrConflict", err)
	}
	if _, err := repo.Update(ctx, 424242, "Apa saja", ""); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("group tak ada: err = %v, mau ErrNotFound", err)
	}
}

func TestGroupRepo_Integration_Delete(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	pool := testutil.NewAppDB(t)
	repo := admin.NewGroupRepo(pool)

	empty, _ := repo.Create(ctx, "Kosong", "")
	if err := repo.Delete(ctx, empty.ID); err != nil {
		t.Fatalf("group kosong harus bisa dihapus: %v", err)
	}
	if _, err := repo.Get(ctx, empty.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("setelah dihapus: err = %v, mau ErrNotFound", err)
	}
	if err := repo.Delete(ctx, empty.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("hapus dua kali: err = %v, mau ErrNotFound", err)
	}

	user := testutil.SeedUser(t, pool, "budi", false)
	withMember := testutil.SeedGroup(t, pool, "Berisi anggota", user.ID)
	if err := repo.Delete(ctx, withMember); !errors.Is(err, admin.ErrGroupNotEmpty) {
		t.Errorf("punya anggota: err = %v, mau ErrGroupNotEmpty", err)
	}

	conn := testutil.SeedConnection(t, pool, "sample", true)
	withGrant := testutil.SeedGroup(t, pool, "Berisi akses")
	testutil.Grant(t, pool, withGrant, conn, domain.AccessViewOnly)
	if err := repo.Delete(ctx, withGrant); !errors.Is(err, admin.ErrGroupNotEmpty) {
		t.Errorf("punya akses: err = %v, mau ErrGroupNotEmpty", err)
	}

	for _, id := range []int64{withMember, withGrant} {
		if _, err := repo.Get(ctx, id); err != nil {
			t.Errorf("group %d tidak boleh hilang saat penghapusan ditolak: %v", id, err)
		}
	}
}
