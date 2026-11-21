package admin

import (
	"context"
	"errors"
	"testing"

	"dbplatform/internal/domain"
)

func TestGroupService_SetLeaderMarksRotation(t *testing.T) {
	store := &fakeGroupStore{}
	svc, spy := newGroupService(store)

	if err := svc.SetLeader(context.Background(), adminActor, 7, 42); err != nil {
		t.Fatal(err)
	}
	if len(store.leaders) != 1 || store.leaders[0] != [2]int64{7, 42} {
		t.Fatalf("leaders = %v", store.leaders)
	}
	if got := spy.RotationMarked(); len(got) != 1 || got[0] != 42 {
		t.Fatalf("leader baru harus dirotasi sessionnya: %v", got)
	}
}

func TestGroupService_SetLeaderRejectsNonAdmin(t *testing.T) {
	store := &fakeGroupStore{}
	svc, _ := newGroupService(store)
	if err := svc.SetLeader(context.Background(), plainActor, 7, 42); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("err = %v", err)
	}
	if store.mutatingCall != 0 {
		t.Fatal("store tidak boleh dipanggil")
	}
}

func TestGroupService_SetLeaderNonMemberDoesNotRotate(t *testing.T) {
	store := &fakeGroupStore{leaderErr: ErrLeaderNotMember}
	svc, spy := newGroupService(store)
	if err := svc.SetLeader(context.Background(), adminActor, 7, 42); !errors.Is(err, ErrLeaderNotMember) {
		t.Fatalf("err = %v", err)
	}
	if len(spy.RotationMarked()) != 0 {
		t.Fatal("gagal set leader tidak boleh merotasi session")
	}
}
