package admin

import (
	"context"
	"errors"
	"testing"

	"dbplatform/internal/domain"
	"dbplatform/internal/testutil"
)

type failingSessions struct{ testutil.SpySessionAdmin }

func (f *failingSessions) RevokeAllForUser(context.Context, int64) error {
	return errors.New("tabel sessions tidak terjangkau")
}

func newManageService(store *fakeStore) (*Service, *testutil.SpySessionAdmin) {
	spy := &testutil.SpySessionAdmin{}
	return NewService(store, fakeHasher{}, spy), spy
}

func TestService_GetUser(t *testing.T) {
	store := &fakeStore{got: domain.User{ID: 3, Username: "budi"}}
	svc, _ := newManageService(store)

	u, err := svc.GetUser(context.Background(), adminActor, 3)
	if err != nil || u.Username != "budi" {
		t.Fatalf("user = %+v, err = %v", u, err)
	}
	if _, err := svc.GetUser(context.Background(), plainActor, 3); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("non-admin err = %v, mau ErrForbidden", err)
	}

	store.getErr = domain.ErrNotFound
	if _, err := svc.GetUser(context.Background(), adminActor, 99); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, mau ErrNotFound", err)
	}
}

func TestService_UpdateUser_NonAdminForbidden(t *testing.T) {
	store := &fakeStore{}
	svc, spy := newManageService(store)

	_, err := svc.UpdateUser(context.Background(), plainActor, 5, UserUpdate{Password: "rahasia-panjang-1"})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("err = %v, mau ErrForbidden", err)
	}
	if len(store.updated) != 0 || len(spy.Revoked()) != 0 {
		t.Error("tidak boleh ada perubahan untuk non-admin")
	}
}

func TestService_UpdateUser_ShortPasswordIsValidationError(t *testing.T) {
	store := &fakeStore{}
	svc, spy := newManageService(store)

	_, err := svc.UpdateUser(context.Background(), adminActor, 5, UserUpdate{Password: "pendek"})
	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Fields["password"] == "" {
		t.Fatalf("err = %v, mau error validasi pada password", err)
	}
	if len(store.updated) != 0 || len(spy.Revoked()) != 0 {
		t.Error("tidak boleh ada perubahan saat input tidak valid")
	}
}

func TestService_UpdateUser_CannotDemoteSelf(t *testing.T) {
	store := &fakeStore{}
	svc, _ := newManageService(store)

	_, err := svc.UpdateUser(context.Background(), adminActor, adminActor.UserID, UserUpdate{IsPlatformAdmin: false})
	if !errors.Is(err, ErrSelfDemote) {
		t.Fatalf("err = %v, mau ErrSelfDemote", err)
	}
	if len(store.updated) != 0 {
		t.Error("repo tidak boleh dipanggil")
	}

	if _, err := svc.UpdateUser(context.Background(), adminActor, adminActor.UserID, UserUpdate{IsPlatformAdmin: true}); err != nil {
		t.Errorf("mempertahankan peran admin diri sendiri harus boleh: %v", err)
	}
}

func TestService_UpdateUser_PasswordChangeRevokesSessions(t *testing.T) {
	store := &fakeStore{wasAdmin: false}
	svc, spy := newManageService(store)

	_, err := svc.UpdateUser(context.Background(), adminActor, 5, UserUpdate{Password: "rahasia-baru-123"})
	if err != nil {
		t.Fatal(err)
	}
	got := store.updated[0]
	if got.id != 5 || got.hash != "hashed:rahasia-baru-123" || got.isAdmin {
		t.Errorf("argumen repo = %+v", got)
	}
	if r := spy.Revoked(); len(r) != 1 || r[0] != 5 {
		t.Errorf("revoked = %v, mau [5]", r)
	}
	if len(spy.RotationMarked()) != 0 {
		t.Error("tidak naik hak akses, tidak perlu rotasi")
	}
}

func TestService_UpdateUser_EmptyPasswordKeepsHashAndSessions(t *testing.T) {
	store := &fakeStore{wasAdmin: true}
	svc, spy := newManageService(store)

	if _, err := svc.UpdateUser(context.Background(), adminActor, 5, UserUpdate{IsPlatformAdmin: true}); err != nil {
		t.Fatal(err)
	}
	if store.updated[0].hash != "" {
		t.Errorf("password kosong harus diteruskan sebagai hash kosong, dapat %q", store.updated[0].hash)
	}
	if len(spy.Revoked()) != 0 || len(spy.RotationMarked()) != 0 {
		t.Error("tanpa ganti password dan tanpa naik hak akses, session tidak boleh disentuh")
	}
}

func TestService_UpdateUser_PromotionMarksRotation(t *testing.T) {
	store := &fakeStore{wasAdmin: false}
	svc, spy := newManageService(store)

	if _, err := svc.UpdateUser(context.Background(), adminActor, 5, UserUpdate{IsPlatformAdmin: true}); err != nil {
		t.Fatal(err)
	}
	if r := spy.RotationMarked(); len(r) != 1 || r[0] != 5 {
		t.Errorf("rotation = %v, mau [5]", r)
	}
	if len(spy.Revoked()) != 0 {
		t.Error("promosi tanpa ganti password tidak boleh mencabut session")
	}
}

func TestService_UpdateUser_DemotionDoesNotMarkRotation(t *testing.T) {
	store := &fakeStore{wasAdmin: true}
	svc, spy := newManageService(store)

	if _, err := svc.UpdateUser(context.Background(), adminActor, 5, UserUpdate{IsPlatformAdmin: false}); err != nil {
		t.Fatal(err)
	}
	if len(spy.RotationMarked()) != 0 {
		t.Error("pencabutan peran tidak memerlukan rotasi")
	}
}

func TestService_UpdateUser_RepoErrorsPropagateWithoutSessionChanges(t *testing.T) {
	for _, repoErr := range []error{ErrLastAdmin, domain.ErrNotFound} {
		store := &fakeStore{updateErr: repoErr}
		svc, spy := newManageService(store)

		_, err := svc.UpdateUser(context.Background(), adminActor, 5, UserUpdate{Password: "rahasia-baru-123"})
		if !errors.Is(err, repoErr) {
			t.Errorf("err = %v, mau %v", err, repoErr)
		}
		if len(spy.Revoked()) != 0 {
			t.Errorf("session tidak boleh dicabut saat update gagal (%v)", repoErr)
		}
	}
}

func TestService_UpdateUser_RevokeFailureIsReported(t *testing.T) {
	svc := NewService(&fakeStore{}, fakeHasher{}, &failingSessions{})
	_, err := svc.UpdateUser(context.Background(), adminActor, 5, UserUpdate{Password: "rahasia-baru-123"})
	if err == nil {
		t.Fatal("kegagalan mencabut session harus dilaporkan, bukan disembunyikan")
	}
}

func TestService_DeactivateUser(t *testing.T) {
	store := &fakeStore{}
	svc, spy := newManageService(store)

	u, err := svc.DeactivateUser(context.Background(), adminActor, 5)
	if err != nil || u.IsActive {
		t.Fatalf("user = %+v, err = %v", u, err)
	}
	if len(store.activeCalls) != 1 || store.activeCalls[0] != (activeCall{5, false}) {
		t.Errorf("SetActive = %+v", store.activeCalls)
	}
	if r := spy.Revoked(); len(r) != 1 || r[0] != 5 {
		t.Errorf("revoked = %v, mau [5]", r)
	}
}

func TestService_DeactivateUser_Guards(t *testing.T) {
	store := &fakeStore{}
	svc, spy := newManageService(store)

	if _, err := svc.DeactivateUser(context.Background(), plainActor, 5); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("non-admin err = %v", err)
	}
	if _, err := svc.DeactivateUser(context.Background(), adminActor, adminActor.UserID); !errors.Is(err, ErrSelfDeactivate) {
		t.Errorf("diri sendiri err = %v", err)
	}
	if len(store.activeCalls) != 0 || len(spy.Revoked()) != 0 {
		t.Error("tidak boleh ada perubahan saat ditolak")
	}

	store.setActiveErr = ErrLastAdmin
	if _, err := svc.DeactivateUser(context.Background(), adminActor, 5); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("err = %v, mau ErrLastAdmin", err)
	}
	if len(spy.Revoked()) != 0 {
		t.Error("session tidak boleh dicabut bila penonaktifan ditolak")
	}
}

func TestService_ActivateUser(t *testing.T) {
	store := &fakeStore{}
	svc, spy := newManageService(store)

	u, err := svc.ActivateUser(context.Background(), adminActor, 5)
	if err != nil || !u.IsActive {
		t.Fatalf("user = %+v, err = %v", u, err)
	}
	if len(store.activeCalls) != 1 || store.activeCalls[0] != (activeCall{5, true}) {
		t.Errorf("SetActive = %+v", store.activeCalls)
	}
	if len(spy.Revoked()) != 0 {
		t.Error("mengaktifkan tidak mencabut session")
	}
	if _, err := svc.ActivateUser(context.Background(), plainActor, 5); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("non-admin err = %v", err)
	}
}
