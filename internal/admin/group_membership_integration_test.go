package admin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/admin"
	"dbplatform/internal/domain"
	"dbplatform/internal/testutil"
)

type groupEnv struct {
	pool *pgxpool.Pool
	repo *admin.GroupRepo
}

func newGroupEnv(t *testing.T) groupEnv {
	t.Helper()
	pool := testutil.NewAppDB(t)
	return groupEnv{pool: pool, repo: admin.NewGroupRepo(pool)}
}

func TestGroupRepo_Integration_AddAndListMembers(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newGroupEnv(t)
	g := testutil.SeedGroup(t, e.pool, "Tim")
	budi := testutil.SeedUser(t, e.pool, "budi", false)
	ani := testutil.SeedUser(t, e.pool, "ani", false)
	testutil.SetUserActive(t, e.pool, ani.ID, false)

	for _, id := range []int64{budi.ID, ani.ID} {
		added, err := e.repo.AddMember(ctx, g, id)
		if err != nil || !added {
			t.Fatalf("AddMember(%d) = %v, %v", id, added, err)
		}
	}
	again, err := e.repo.AddMember(ctx, g, budi.ID)
	if err != nil || again {
		t.Errorf("menambah anggota yang sudah ada: added = %v, err = %v, mau false dan nil", again, err)
	}

	members, err := e.repo.ListMembers(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 || members[0].Username != "ani" || members[1].Username != "budi" {
		t.Fatalf("anggota harus berurut menurut username: %+v", members)
	}
	if members[0].IsActive || !members[1].IsActive {
		t.Errorf("status aktif tidak sesuai: %+v", members)
	}

	ids, err := e.repo.MemberIDs(ctx, g)
	if err != nil || len(ids) != 2 {
		t.Errorf("MemberIDs = %v, %v", ids, err)
	}
}

func TestGroupRepo_Integration_AddMemberUnknownReferences(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newGroupEnv(t)
	g := testutil.SeedGroup(t, e.pool, "Tim")
	u := testutil.SeedUser(t, e.pool, "budi", false)

	if _, err := e.repo.AddMember(ctx, 424242, u.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("group tak ada: err = %v, mau ErrNotFound", err)
	}
	if _, err := e.repo.AddMember(ctx, g, 424242); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("user tak ada: err = %v, mau ErrNotFound", err)
	}
}

func TestGroupRepo_Integration_RemoveMember(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newGroupEnv(t)
	u := testutil.SeedUser(t, e.pool, "budi", false)
	g := testutil.SeedGroup(t, e.pool, "Tim", u.ID)

	if err := e.repo.RemoveMember(ctx, g, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.repo.RemoveMember(ctx, g, u.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("mengeluarkan dua kali: err = %v, mau ErrNotFound", err)
	}
	if members, _ := e.repo.ListMembers(ctx, g); len(members) != 0 {
		t.Errorf("anggota tersisa: %+v", members)
	}
}

func TestGroupRepo_Integration_AvailableUsersExcludesMembersAndInactive(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newGroupEnv(t)
	member := testutil.SeedUser(t, e.pool, "anggota", false)
	free := testutil.SeedUser(t, e.pool, "bebas", false)
	gone := testutil.SeedUser(t, e.pool, "nonaktif", false)
	testutil.SetUserActive(t, e.pool, gone.ID, false)
	g := testutil.SeedGroup(t, e.pool, "Tim", member.ID)

	list, err := e.repo.AvailableUsers(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != free.ID || list[0].Username != "bebas" {
		t.Errorf("AvailableUsers = %+v, mau hanya 'bebas'", list)
	}
}

func TestGroupRepo_Integration_GrantAccessIsUpsertAndReportsPrevious(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newGroupEnv(t)
	g := testutil.SeedGroup(t, e.pool, "Tim")
	conn := testutil.SeedConnection(t, e.pool, "sample", true)
	granter := testutil.SeedUser(t, e.pool, "admin", true)

	prev, err := e.repo.GrantAccess(ctx, g, conn, domain.AccessViewOnly, granter.ID)
	if err != nil || prev != domain.AccessNone {
		t.Fatalf("pemberian pertama: prev = %q, err = %v", prev, err)
	}
	prev, err = e.repo.GrantAccess(ctx, g, conn, domain.AccessViewEdit, granter.ID)
	if err != nil || prev != domain.AccessViewOnly {
		t.Fatalf("pemberian kedua: prev = %q, err = %v, mau view_only", prev, err)
	}

	var rows int
	var level string
	var by *int64
	err = e.pool.QueryRow(ctx, `
		SELECT count(*), max(access_level), (array_agg(granted_by))[1]
		FROM group_database_access WHERE group_id = $1`, g).Scan(&rows, &level, &by)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 1 || level != "view_edit" {
		t.Errorf("upsert tidak boleh menggandakan baris: rows = %d, level = %s", rows, level)
	}
	if by == nil || *by != granter.ID {
		t.Errorf("granted_by = %v, mau %d", by, granter.ID)
	}
}

func TestGroupRepo_Integration_GrantAccessWithoutGranterStoresNull(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newGroupEnv(t)
	g := testutil.SeedGroup(t, e.pool, "Tim")
	conn := testutil.SeedConnection(t, e.pool, "sample", true)

	if _, err := e.repo.GrantAccess(ctx, g, conn, domain.AccessViewOnly, 0); err != nil {
		t.Fatal(err)
	}
	var by *int64
	if err := e.pool.QueryRow(ctx, `SELECT granted_by FROM group_database_access WHERE group_id = $1`, g).Scan(&by); err != nil {
		t.Fatal(err)
	}
	if by != nil {
		t.Errorf("granted_by = %v, mau NULL", *by)
	}
}

func TestGroupRepo_Integration_GrantAccessRejections(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newGroupEnv(t)
	g := testutil.SeedGroup(t, e.pool, "Tim")
	active := testutil.SeedConnection(t, e.pool, "aktif", true)
	inactive := testutil.SeedConnection(t, e.pool, "nonaktif", false)

	if _, err := e.repo.GrantAccess(ctx, g, inactive, domain.AccessViewOnly, 0); !errors.Is(err, domain.ErrConnectionInactive) {
		t.Errorf("koneksi nonaktif: err = %v, mau ErrConnectionInactive", err)
	}
	if _, err := e.repo.GrantAccess(ctx, g, 424242, domain.AccessViewOnly, 0); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("koneksi tak ada: err = %v, mau ErrNotFound", err)
	}
	if _, err := e.repo.GrantAccess(ctx, 424242, active, domain.AccessViewOnly, 0); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("group tak ada: err = %v, mau ErrNotFound", err)
	}
	if _, err := e.repo.GrantAccess(ctx, g, active, domain.AccessLevel("admin"), 0); err == nil {
		t.Error("level tak dikenal harus ditolak database")
	}
	if list, _ := e.repo.ListAccess(ctx, g); len(list) != 0 {
		t.Errorf("tidak boleh ada akses tersimpan setelah penolakan: %+v", list)
	}
}

func TestGroupRepo_Integration_ListAndRevokeAccess(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newGroupEnv(t)
	g := testutil.SeedGroup(t, e.pool, "Tim")
	zeta := testutil.SeedConnection(t, e.pool, "Zeta", true)
	alfa := testutil.SeedConnection(t, e.pool, "alfa", true)
	testutil.Grant(t, e.pool, g, zeta, domain.AccessViewEdit)
	testutil.Grant(t, e.pool, g, alfa, domain.AccessViewOnly)

	list, err := e.repo.ListAccess(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ConnectionName != "alfa" || list[0].Level != domain.AccessViewOnly ||
		list[1].ConnectionName != "Zeta" || list[1].Level != domain.AccessViewEdit {
		t.Fatalf("ListAccess = %+v", list)
	}

	if err := e.repo.RevokeAccess(ctx, g, alfa); err != nil {
		t.Fatal(err)
	}
	if err := e.repo.RevokeAccess(ctx, g, alfa); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("mencabut dua kali: err = %v, mau ErrNotFound", err)
	}
	if list, _ := e.repo.ListAccess(ctx, g); len(list) != 1 || list[0].ConnectionID != zeta {
		t.Errorf("sisa akses = %+v", list)
	}
}

func TestGroupRepo_Integration_AvailableConnectionsOnlyActive(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newGroupEnv(t)
	testutil.SeedConnection(t, e.pool, "Zeta", true)
	testutil.SeedConnection(t, e.pool, "alfa", true)
	testutil.SeedConnection(t, e.pool, "mati", false)

	list, err := e.repo.AvailableConnections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "alfa" || list[1].Name != "Zeta" {
		t.Errorf("AvailableConnections = %+v, mau alfa lalu Zeta saja", list)
	}
}
