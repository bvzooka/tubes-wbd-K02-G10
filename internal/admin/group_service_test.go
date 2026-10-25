package admin

import (
	"context"
	"errors"
	"strings"
	"testing"

	"dbplatform/internal/domain"
	"dbplatform/internal/testutil"
)

type grantCall struct {
	groupID, connID int64
	level           domain.AccessLevel
	by              int64
}

type fakeGroupStore struct {
	group        domain.Group
	getErr       error
	listed       []GroupRow
	members      []GroupMember
	memberIDs    []int64
	access       []GroupAccess
	users        []UserOption
	conns        []ConnOption
	created      []GroupInput
	createErr    error
	updated      []GroupInput
	updateErr    error
	deleted      []int64
	deleteErr    error
	added        [][2]int64
	addResult    bool
	addErr       error
	removed      [][2]int64
	removeErr    error
	grants       []grantCall
	grantPrev    domain.AccessLevel
	grantErr     error
	revoked      [][2]int64
	revokeErr    error
	mutatingCall int
}

func (f *fakeGroupStore) Create(_ context.Context, name, desc string) (domain.Group, error) {
	if f.createErr != nil {
		return domain.Group{}, f.createErr
	}
	f.mutatingCall++
	f.created = append(f.created, GroupInput{name, desc})
	return domain.Group{ID: 1, Name: name, Description: desc}, nil
}

func (f *fakeGroupStore) List(context.Context) ([]GroupRow, error) { return f.listed, nil }

func (f *fakeGroupStore) Get(context.Context, int64) (domain.Group, error) { return f.group, f.getErr }

func (f *fakeGroupStore) Update(_ context.Context, id int64, name, desc string) (domain.Group, error) {
	if f.updateErr != nil {
		return domain.Group{}, f.updateErr
	}
	f.mutatingCall++
	f.updated = append(f.updated, GroupInput{name, desc})
	return domain.Group{ID: id, Name: name, Description: desc}, nil
}

func (f *fakeGroupStore) Delete(_ context.Context, id int64) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.mutatingCall++
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeGroupStore) ListMembers(context.Context, int64) ([]GroupMember, error) {
	return f.members, nil
}

func (f *fakeGroupStore) MemberIDs(context.Context, int64) ([]int64, error) { return f.memberIDs, nil }

func (f *fakeGroupStore) AddMember(_ context.Context, groupID, userID int64) (bool, error) {
	if f.addErr != nil {
		return false, f.addErr
	}
	f.mutatingCall++
	f.added = append(f.added, [2]int64{groupID, userID})
	return f.addResult, nil
}

func (f *fakeGroupStore) RemoveMember(_ context.Context, groupID, userID int64) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	f.mutatingCall++
	f.removed = append(f.removed, [2]int64{groupID, userID})
	return nil
}

func (f *fakeGroupStore) ListAccess(context.Context, int64) ([]GroupAccess, error) {
	return f.access, nil
}

func (f *fakeGroupStore) GrantAccess(_ context.Context, groupID, connID int64, level domain.AccessLevel, by int64) (domain.AccessLevel, error) {
	if f.grantErr != nil {
		return domain.AccessNone, f.grantErr
	}
	f.mutatingCall++
	f.grants = append(f.grants, grantCall{groupID, connID, level, by})
	return f.grantPrev, nil
}

func (f *fakeGroupStore) RevokeAccess(_ context.Context, groupID, connID int64) error {
	if f.revokeErr != nil {
		return f.revokeErr
	}
	f.mutatingCall++
	f.revoked = append(f.revoked, [2]int64{groupID, connID})
	return nil
}

func (f *fakeGroupStore) AvailableUsers(context.Context, int64) ([]UserOption, error) {
	return f.users, nil
}

func (f *fakeGroupStore) AvailableConnections(context.Context) ([]ConnOption, error) {
	return f.conns, nil
}

type failingRotation struct{ testutil.SpySessionAdmin }

func (f *failingRotation) MarkRotationNeeded(context.Context, ...int64) error {
	return errors.New("tabel sessions tidak terjangkau")
}

func newGroupService(store *fakeGroupStore) (*GroupService, *testutil.SpySessionAdmin) {
	spy := &testutil.SpySessionAdmin{}
	return NewGroupService(store, spy), spy
}

func TestGroupService_EveryMethodRejectsNonAdmin(t *testing.T) {
	ctx := context.Background()
	calls := map[string]func(*GroupService) error{
		"CreateGroup": func(s *GroupService) error {
			_, err := s.CreateGroup(ctx, plainActor, GroupInput{Name: "Tim"})
			return err
		},
		"ListGroups": func(s *GroupService) error { _, err := s.ListGroups(ctx, plainActor); return err },
		"GroupDetail": func(s *GroupService) error {
			_, err := s.GroupDetail(ctx, plainActor, 1)
			return err
		},
		"UpdateGroup": func(s *GroupService) error {
			_, err := s.UpdateGroup(ctx, plainActor, 1, GroupInput{Name: "Tim"})
			return err
		},
		"DeleteGroup":  func(s *GroupService) error { return s.DeleteGroup(ctx, plainActor, 1) },
		"AddMember":    func(s *GroupService) error { return s.AddMember(ctx, plainActor, 1, 2) },
		"RemoveMember": func(s *GroupService) error { return s.RemoveMember(ctx, plainActor, 1, 2) },
		"GrantAccess": func(s *GroupService) error {
			return s.GrantAccess(ctx, plainActor, 1, 2, domain.AccessViewOnly)
		},
		"RevokeAccess": func(s *GroupService) error { return s.RevokeAccess(ctx, plainActor, 1, 2) },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			store := &fakeGroupStore{addResult: true}
			svc, spy := newGroupService(store)
			if err := call(svc); !errors.Is(err, domain.ErrForbidden) {
				t.Errorf("err = %v, mau ErrForbidden", err)
			}
			if store.mutatingCall != 0 || len(spy.RotationMarked()) != 0 {
				t.Error("tidak boleh ada perubahan untuk non-admin")
			}
		})
	}
}

func TestGroupService_CreateGroup(t *testing.T) {
	store := &fakeGroupStore{}
	svc, _ := newGroupService(store)

	g, err := svc.CreateGroup(context.Background(), adminActor, GroupInput{Name: "  Tim Data ", Description: " analis "})
	if err != nil {
		t.Fatal(err)
	}
	if g.Name != "Tim Data" || len(store.created) != 1 || store.created[0] != (GroupInput{"Tim Data", "analis"}) {
		t.Errorf("group = %+v, tersimpan = %+v", g, store.created)
	}
}

func TestGroupService_CreateAndUpdateValidation(t *testing.T) {
	bad := GroupInput{Name: "   ", Description: strings.Repeat("x", 501)}
	for name, call := range map[string]func(*GroupService) error{
		"create": func(s *GroupService) error {
			_, err := s.CreateGroup(context.Background(), adminActor, bad)
			return err
		},
		"update": func(s *GroupService) error {
			_, err := s.UpdateGroup(context.Background(), adminActor, 1, bad)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			store := &fakeGroupStore{}
			svc, _ := newGroupService(store)

			var verr *domain.ValidationError
			if err := call(svc); !errors.As(err, &verr) {
				t.Fatalf("err = %v, mau *ValidationError", err)
			}
			if verr.Fields["name"] == "" || verr.Fields["description"] == "" {
				t.Errorf("fields = %v, mau name dan description terisi", verr.Fields)
			}
			if store.mutatingCall != 0 {
				t.Error("store tidak boleh disentuh saat input tidak valid")
			}
		})
	}
}

func TestGroupService_DuplicateNameBecomesFieldError(t *testing.T) {
	for name, store := range map[string]*fakeGroupStore{
		"create": {createErr: domain.ErrConflict},
		"update": {updateErr: domain.ErrConflict},
	} {
		t.Run(name, func(t *testing.T) {
			svc, _ := newGroupService(store)
			var err error
			if name == "create" {
				_, err = svc.CreateGroup(context.Background(), adminActor, GroupInput{Name: "Tim"})
			} else {
				_, err = svc.UpdateGroup(context.Background(), adminActor, 1, GroupInput{Name: "Tim"})
			}
			var verr *domain.ValidationError
			if !errors.As(err, &verr) || verr.Fields["name"] != msgGroupNameTaken {
				t.Fatalf("err = %v, mau error validasi 'nama sudah dipakai'", err)
			}
		})
	}
}

func TestGroupService_UpdateGroup(t *testing.T) {
	store := &fakeGroupStore{}
	svc, _ := newGroupService(store)

	g, err := svc.UpdateGroup(context.Background(), adminActor, 7, GroupInput{Name: " Baru ", Description: ""})
	if err != nil || g.ID != 7 || g.Name != "Baru" {
		t.Fatalf("group = %+v, err = %v", g, err)
	}

	store.updateErr = domain.ErrNotFound
	if _, err := svc.UpdateGroup(context.Background(), adminActor, 99, GroupInput{Name: "Baru"}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, mau ErrNotFound", err)
	}
}

func TestGroupService_GroupDetailCanDelete(t *testing.T) {
	member := []GroupMember{{UserID: 1, Username: "budi"}}
	access := []GroupAccess{{ConnectionID: 2, ConnectionName: "sample", Level: domain.AccessViewOnly}}
	tests := []struct {
		name    string
		members []GroupMember
		access  []GroupAccess
		want    bool
	}{
		{"kosong", nil, nil, true},
		{"punya anggota", member, nil, false},
		{"punya akses", nil, access, false},
		{"punya keduanya", member, access, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeGroupStore{
				group:   domain.Group{ID: 5, Name: "Tim"},
				members: tt.members, access: tt.access,
				users: []UserOption{{ID: 9, Username: "ani"}}, conns: []ConnOption{{ID: 3, Name: "sample"}},
			}
			svc, _ := newGroupService(store)

			d, err := svc.GroupDetail(context.Background(), adminActor, 5)
			if err != nil {
				t.Fatal(err)
			}
			if d.CanDelete != tt.want {
				t.Errorf("CanDelete = %v, mau %v", d.CanDelete, tt.want)
			}
			if d.Group.Name != "Tim" || len(d.AvailableUsers) != 1 || len(d.AvailableConnections) != 1 {
				t.Errorf("detail tidak lengkap: %+v", d)
			}
		})
	}
}

func TestGroupService_GroupDetailNotFound(t *testing.T) {
	svc, _ := newGroupService(&fakeGroupStore{getErr: domain.ErrNotFound})
	if _, err := svc.GroupDetail(context.Background(), adminActor, 99); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, mau ErrNotFound", err)
	}
}

func TestGroupService_DeleteGroup(t *testing.T) {
	store := &fakeGroupStore{}
	svc, _ := newGroupService(store)
	if err := svc.DeleteGroup(context.Background(), adminActor, 4); err != nil || len(store.deleted) != 1 || store.deleted[0] != 4 {
		t.Fatalf("err = %v, deleted = %v", err, store.deleted)
	}
	for _, repoErr := range []error{ErrGroupNotEmpty, domain.ErrNotFound} {
		svc, _ := newGroupService(&fakeGroupStore{deleteErr: repoErr})
		if err := svc.DeleteGroup(context.Background(), adminActor, 4); !errors.Is(err, repoErr) {
			t.Errorf("err = %v, mau %v", err, repoErr)
		}
	}
}

func TestGroupService_AddMemberMarksRotationOnlyWhenNewlyAdded(t *testing.T) {
	store := &fakeGroupStore{addResult: true}
	svc, spy := newGroupService(store)

	if err := svc.AddMember(context.Background(), adminActor, 5, 8); err != nil {
		t.Fatal(err)
	}
	if r := spy.RotationMarked(); len(r) != 1 || r[0] != 8 {
		t.Errorf("rotation = %v, mau [8]", r)
	}

	store.addResult = false
	if err := svc.AddMember(context.Background(), adminActor, 5, 8); err != nil {
		t.Fatal(err)
	}
	if len(spy.RotationMarked()) != 1 {
		t.Error("anggota yang sudah ada tidak boleh memicu rotasi lagi")
	}
}

func TestGroupService_AddMemberRejections(t *testing.T) {
	svc, spy := newGroupService(&fakeGroupStore{getErr: domain.ErrNotFound})
	if err := svc.AddMember(context.Background(), adminActor, 99, 8); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("group tak ada: err = %v", err)
	}

	svc, spy = newGroupService(&fakeGroupStore{addErr: domain.ErrNotFound})
	var verr *domain.ValidationError
	if err := svc.AddMember(context.Background(), adminActor, 5, 424242); !errors.As(err, &verr) || verr.Fields["user_id"] == "" {
		t.Errorf("user tak ada: err = %v, mau error validasi user_id", err)
	}
	if len(spy.RotationMarked()) != 0 {
		t.Error("rotasi tidak boleh ditandai saat penambahan gagal")
	}
}

func TestGroupService_AddMemberRotationFailureIsReported(t *testing.T) {
	svc := NewGroupService(&fakeGroupStore{addResult: true}, &failingRotation{})
	if err := svc.AddMember(context.Background(), adminActor, 5, 8); err == nil {
		t.Fatal("kegagalan menandai rotasi harus dilaporkan")
	}
}

func TestGroupService_RemoveMemberDoesNotTouchSessions(t *testing.T) {
	store := &fakeGroupStore{}
	svc, spy := newGroupService(store)

	if err := svc.RemoveMember(context.Background(), adminActor, 5, 8); err != nil || len(store.removed) != 1 {
		t.Fatalf("err = %v, removed = %v", err, store.removed)
	}
	if len(spy.RotationMarked()) != 0 || len(spy.Revoked()) != 0 {
		t.Error("akses dihitung per request, session tidak perlu disentuh")
	}
	svc, _ = newGroupService(&fakeGroupStore{removeErr: domain.ErrNotFound})
	if err := svc.RemoveMember(context.Background(), adminActor, 5, 8); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, mau ErrNotFound", err)
	}
}

func TestGroupService_GrantAccessRotationRules(t *testing.T) {
	tests := []struct {
		name         string
		previous     domain.AccessLevel
		level        domain.AccessLevel
		members      []int64
		wantRotation []int64
	}{
		{"pemberian baru", domain.AccessNone, domain.AccessViewOnly, []int64{1, 2}, []int64{1, 2}},
		{"naik ke view_edit", domain.AccessViewOnly, domain.AccessViewEdit, []int64{1, 2}, []int64{1, 2}},
		{"level sama", domain.AccessViewEdit, domain.AccessViewEdit, []int64{1, 2}, nil},
		{"turun ke view_only", domain.AccessViewEdit, domain.AccessViewOnly, []int64{1, 2}, nil},
		{"naik tapi tanpa anggota", domain.AccessViewOnly, domain.AccessViewEdit, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeGroupStore{grantPrev: tt.previous, memberIDs: tt.members}
			svc, spy := newGroupService(store)

			if err := svc.GrantAccess(context.Background(), adminActor, 5, 3, tt.level); err != nil {
				t.Fatal(err)
			}
			if len(store.grants) != 1 || store.grants[0] != (grantCall{5, 3, tt.level, adminActor.UserID}) {
				t.Errorf("grants = %+v", store.grants)
			}
			got := spy.RotationMarked()
			if len(got) != len(tt.wantRotation) {
				t.Fatalf("rotation = %v, mau %v", got, tt.wantRotation)
			}
			for i := range got {
				if got[i] != tt.wantRotation[i] {
					t.Errorf("rotation = %v, mau %v", got, tt.wantRotation)
				}
			}
		})
	}
}

func TestGroupService_GrantAccessRejections(t *testing.T) {
	ctx := context.Background()

	store := &fakeGroupStore{}
	svc, _ := newGroupService(store)
	var verr *domain.ValidationError
	if err := svc.GrantAccess(ctx, adminActor, 5, 3, domain.AccessLevel("admin")); !errors.As(err, &verr) || verr.Fields["access_level"] == "" {
		t.Errorf("level salah: err = %v", err)
	}
	if err := svc.GrantAccess(ctx, adminActor, 5, 3, domain.AccessNone); !errors.As(err, &verr) {
		t.Errorf("level kosong: err = %v", err)
	}
	if store.mutatingCall != 0 {
		t.Error("store tidak boleh disentuh saat level tidak valid")
	}

	svc, _ = newGroupService(&fakeGroupStore{getErr: domain.ErrNotFound})
	if err := svc.GrantAccess(ctx, adminActor, 99, 3, domain.AccessViewOnly); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("group tak ada: err = %v", err)
	}

	svc, spy := newGroupService(&fakeGroupStore{grantErr: domain.ErrConnectionInactive, memberIDs: []int64{1}})
	if err := svc.GrantAccess(ctx, adminActor, 5, 3, domain.AccessViewOnly); !errors.As(err, &verr) || verr.Fields["connection_id"] != msgConnectionInactive {
		t.Errorf("koneksi nonaktif: err = %v", err)
	}

	svc, _ = newGroupService(&fakeGroupStore{grantErr: domain.ErrNotFound})
	if err := svc.GrantAccess(ctx, adminActor, 5, 424242, domain.AccessViewOnly); !errors.As(err, &verr) || verr.Fields["connection_id"] != msgConnectionMissing {
		t.Errorf("koneksi tak ada: err = %v", err)
	}
	if len(spy.RotationMarked()) != 0 {
		t.Error("rotasi tidak boleh ditandai saat pemberian gagal")
	}
}

func TestGroupService_GrantAccessRotationFailureIsReported(t *testing.T) {
	store := &fakeGroupStore{grantPrev: domain.AccessNone, memberIDs: []int64{1}}
	svc := NewGroupService(store, &failingRotation{})
	if err := svc.GrantAccess(context.Background(), adminActor, 5, 3, domain.AccessViewOnly); err == nil {
		t.Fatal("kegagalan menandai rotasi harus dilaporkan")
	}
}

func TestGroupService_RevokeAccess(t *testing.T) {
	store := &fakeGroupStore{}
	svc, spy := newGroupService(store)

	if err := svc.RevokeAccess(context.Background(), adminActor, 5, 3); err != nil || len(store.revoked) != 1 {
		t.Fatalf("err = %v, revoked = %v", err, store.revoked)
	}
	if len(spy.RotationMarked()) != 0 || len(spy.Revoked()) != 0 {
		t.Error("mencabut akses tidak perlu menyentuh session")
	}
	svc, _ = newGroupService(&fakeGroupStore{revokeErr: domain.ErrNotFound})
	if err := svc.RevokeAccess(context.Background(), adminActor, 5, 3); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, mau ErrNotFound", err)
	}
}
