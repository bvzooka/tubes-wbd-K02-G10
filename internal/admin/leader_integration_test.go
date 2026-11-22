package admin_test

import (
	"context"
	"errors"
	"testing"

	"dbplatform/internal/admin"
	"dbplatform/internal/domain"
	"dbplatform/internal/testutil"
)

func TestGroupRepo_Integration_Leader(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newGroupEnv(t)
	budi := testutil.SeedUser(t, e.pool, "budi", false)
	ani := testutil.SeedUser(t, e.pool, "ani", false)
	g := testutil.SeedGroup(t, e.pool, "Tim", budi.ID)

	if err := e.repo.SetLeader(ctx, g, ani.ID); !errors.Is(err, admin.ErrLeaderNotMember) {
		t.Fatalf("bukan anggota: err = %v", err)
	}
	if err := e.repo.SetLeader(ctx, 9999, budi.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("group tidak ada: err = %v", err)
	}
	if err := e.repo.SetLeader(ctx, g, budi.ID); err != nil {
		t.Fatal(err)
	}

	got, err := e.repo.Get(ctx, g)
	if err != nil || got.LeaderID == nil || *got.LeaderID != budi.ID {
		t.Fatalf("leader tidak tersimpan: %+v %v", got, err)
	}
	rows, _ := e.repo.List(ctx)
	if len(rows) != 1 || rows[0].LeaderName != "budi" {
		t.Fatalf("List harus memuat nama leader: %+v", rows)
	}

	if err := e.repo.RemoveMember(ctx, g, budi.ID); !errors.Is(err, admin.ErrRemoveLeader) {
		t.Fatalf("mengeluarkan leader harus ditolak: %v", err)
	}
	if err := e.repo.RemoveMember(ctx, g, ani.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("bukan anggota: %v", err)
	}
}

func TestUserRepo_Integration_DeactivateLeaderRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	pool := testutil.NewAppDB(t)
	repo := admin.NewUserRepo(pool)
	budi := testutil.SeedUser(t, pool, "budi", false)
	g := testutil.SeedGroup(t, pool, "Tim", budi.ID)
	if err := admin.NewGroupRepo(pool).SetLeader(ctx, g, budi.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.SetActive(ctx, budi.ID, false); !errors.Is(err, admin.ErrDeactivateLeader) {
		t.Fatalf("err = %v", err)
	}
	if _, err := repo.SetActive(ctx, budi.ID, true); err != nil {
		t.Fatalf("mengaktifkan leader tetap boleh: %v", err)
	}
}
