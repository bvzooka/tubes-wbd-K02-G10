package admin_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/admin"
	"dbplatform/internal/domain"
	"dbplatform/internal/testutil"
)

type manageEnv struct {
	pool *pgxpool.Pool
	svc  *admin.Service
	spy  *testutil.SpySessionAdmin
	repo *admin.UserRepo
}

func newManageEnv(t *testing.T) manageEnv {
	t.Helper()
	pool := testutil.NewAppDB(t)
	repo := admin.NewUserRepo(pool)
	spy := &testutil.SpySessionAdmin{}
	return manageEnv{pool: pool, svc: admin.NewService(repo, bcryptHasher{}, spy), spy: spy, repo: repo}
}

func (e manageEnv) addUser(t *testing.T, username string, isAdmin bool) domain.User {
	t.Helper()
	u, err := e.repo.Create(context.Background(), username, "hash-awal", isAdmin)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (e manageEnv) hash(t *testing.T, id int64) string {
	t.Helper()
	var h string
	if err := e.pool.QueryRow(context.Background(), `SELECT password_hash FROM users WHERE id = $1`, id).Scan(&h); err != nil {
		t.Fatal(err)
	}
	return h
}

func actorFor(id int64) domain.Principal {
	return domain.Principal{UserID: id, Username: "aktor", IsPlatformAdmin: true}
}

func TestService_Integration_UpdatePasswordChangesHashAndRevokes(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newManageEnv(t)
	u := e.addUser(t, "budi", false)

	if _, err := e.svc.UpdateUser(ctx, actorFor(9999), u.ID, admin.UserUpdate{Password: "rahasia-baru-123"}); err != nil {
		t.Fatal(err)
	}
	h := e.hash(t, u.ID)
	if h == "hash-awal" || !(bcryptHasher{}).Verify(h, "rahasia-baru-123") {
		t.Errorf("hash baru harus cocok dengan password baru, dapat %q", h)
	}
	if r := e.spy.Revoked(); len(r) != 1 || r[0] != u.ID {
		t.Errorf("revoked = %v, mau [%d]", r, u.ID)
	}
}

func TestService_Integration_UpdateWithoutPasswordKeepsHash(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	e := newManageEnv(t)
	u := e.addUser(t, "budi", false)

	if _, err := e.svc.UpdateUser(context.Background(), actorFor(9999), u.ID, admin.UserUpdate{IsPlatformAdmin: true}); err != nil {
		t.Fatal(err)
	}
	if h := e.hash(t, u.ID); h != "hash-awal" {
		t.Errorf("hash berubah padahal password kosong: %q", h)
	}
	if r := e.spy.RotationMarked(); len(r) != 1 || r[0] != u.ID {
		t.Errorf("promosi harus menandai rotasi, dapat %v", r)
	}
	got, err := e.repo.Get(context.Background(), u.ID)
	if err != nil || !got.IsPlatformAdmin {
		t.Errorf("user = %+v, err = %v", got, err)
	}
}

func TestService_Integration_UnknownUserIsNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newManageEnv(t)

	if _, err := e.svc.UpdateUser(ctx, actorFor(9999), 424242, admin.UserUpdate{}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("update err = %v", err)
	}
	if _, err := e.svc.DeactivateUser(ctx, actorFor(9999), 424242); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("deactivate err = %v", err)
	}
	if _, err := e.svc.ActivateUser(ctx, actorFor(9999), 424242); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("activate err = %v", err)
	}
	if _, err := e.svc.GetUser(ctx, actorFor(9999), 424242); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("get err = %v", err)
	}
}

func TestService_Integration_DeactivateAndActivate(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newManageEnv(t)
	u := e.addUser(t, "budi", false)

	got, err := e.svc.DeactivateUser(ctx, actorFor(9999), u.ID)
	if err != nil || got.IsActive {
		t.Fatalf("user = %+v, err = %v", got, err)
	}
	if r := e.spy.Revoked(); len(r) != 1 || r[0] != u.ID {
		t.Errorf("revoked = %v", r)
	}

	got, err = e.svc.ActivateUser(ctx, actorFor(9999), u.ID)
	if err != nil || !got.IsActive {
		t.Fatalf("user = %+v, err = %v", got, err)
	}
}

func TestService_Integration_LastAdminIsProtected(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newManageEnv(t)
	only := e.addUser(t, "satu-satunya", true)

	if _, err := e.svc.UpdateUser(ctx, actorFor(9999), only.ID, admin.UserUpdate{IsPlatformAdmin: false}); !errors.Is(err, admin.ErrLastAdmin) {
		t.Errorf("mencabut peran admin terakhir: err = %v, mau ErrLastAdmin", err)
	}
	if _, err := e.svc.DeactivateUser(ctx, actorFor(9999), only.ID); !errors.Is(err, admin.ErrLastAdmin) {
		t.Errorf("menonaktifkan admin terakhir: err = %v, mau ErrLastAdmin", err)
	}
	if len(e.spy.Revoked()) != 0 {
		t.Error("session tidak boleh dicabut saat penolakan")
	}
	got, _ := e.repo.Get(ctx, only.ID)
	if !got.IsPlatformAdmin || !got.IsActive {
		t.Errorf("admin terakhir harus tetap admin dan aktif: %+v", got)
	}
}

func TestService_Integration_SecondAdminAllowsDemotion(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newManageEnv(t)
	a1 := e.addUser(t, "admin-a", true)
	a2 := e.addUser(t, "admin-b", true)

	if _, err := e.svc.UpdateUser(ctx, actorFor(a1.ID), a2.ID, admin.UserUpdate{IsPlatformAdmin: false}); err != nil {
		t.Fatalf("masih ada admin lain, harus boleh: %v", err)
	}
	if _, err := e.svc.DeactivateUser(ctx, actorFor(a2.ID), a1.ID); !errors.Is(err, admin.ErrLastAdmin) {
		t.Errorf("a1 kini admin terakhir: err = %v, mau ErrLastAdmin", err)
	}
}

func TestService_Integration_InactiveAdminDoesNotCountAsOtherAdmin(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newManageEnv(t)
	active := e.addUser(t, "admin-aktif", true)
	dormant := e.addUser(t, "admin-tidur", true)
	if _, err := e.repo.SetActive(ctx, dormant.ID, false); err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.DeactivateUser(ctx, actorFor(9999), active.ID); !errors.Is(err, admin.ErrLastAdmin) {
		t.Errorf("admin nonaktif tidak dihitung: err = %v, mau ErrLastAdmin", err)
	}
}

func TestService_Integration_TwoAdminsDeactivatingEachOtherLeavesOne(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newManageEnv(t)
	a1 := e.addUser(t, "admin-a", true)
	a2 := e.addUser(t, "admin-b", true)
	pairs := [][2]int64{{a1.ID, a2.ID}, {a2.ID, a1.ID}}

	for round := 0; round < 40; round++ {
		for _, id := range []int64{a1.ID, a2.ID} {
			if _, err := e.repo.SetActive(ctx, id, true); err != nil {
				t.Fatal(err)
			}
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, p := range pairs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, errs[i] = e.svc.DeactivateUser(ctx, actorFor(p[0]), p[1])
			}()
		}
		close(start)
		wg.Wait()

		var ok, blocked int
		for _, err := range errs {
			switch {
			case err == nil:
				ok++
			case errors.Is(err, admin.ErrLastAdmin):
				blocked++
			default:
				t.Fatalf("putaran %d: error tak terduga: %v", round, err)
			}
		}
		if ok != 1 || blocked != 1 {
			t.Fatalf("putaran %d: berhasil = %d, ditolak = %d; mau tepat 1 dan 1", round, ok, blocked)
		}

		var active int
		if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE is_platform_admin AND is_active`).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if active != 1 {
			t.Fatalf("putaran %d: admin aktif tersisa = %d, mau 1", round, active)
		}
	}
}
