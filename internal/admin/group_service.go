package admin

import (
	"context"
	"errors"
	"fmt"

	"dbplatform/internal/contract"
	"dbplatform/internal/domain"
)

// groupStore adalah penyimpanan group yang dibutuhkan service, implementasinya GroupRepo
type groupStore interface {
	Create(ctx context.Context, name, description string) (domain.Group, error)
	List(ctx context.Context) ([]GroupRow, error)
	Get(ctx context.Context, id int64) (domain.Group, error)
	Update(ctx context.Context, id int64, name, description string) (domain.Group, error)
	Delete(ctx context.Context, id int64) error
	ListMembers(ctx context.Context, groupID int64) ([]GroupMember, error)
	MemberIDs(ctx context.Context, groupID int64) ([]int64, error)
	AddMember(ctx context.Context, groupID, userID int64) (bool, error)
	RemoveMember(ctx context.Context, groupID, userID int64) error
	SetLeader(ctx context.Context, groupID, userID int64) error
	ListAccess(ctx context.Context, groupID int64) ([]GroupAccess, error)
	GrantAccess(ctx context.Context, groupID, connID int64, level domain.AccessLevel, grantedBy int64) (domain.AccessLevel, error)
	RevokeAccess(ctx context.Context, groupID, connID int64) error
	AvailableUsers(ctx context.Context, groupID int64) ([]UserOption, error)
	AvailableConnections(ctx context.Context) ([]ConnOption, error)
}

// GroupInput adalah isi form buat atau ubah group
type GroupInput struct {
	Name        string
	Description string
}

// GroupDetail adalah semua yang ditampilkan di halaman satu group: anggota, akses, dan pilihan dropdown
type GroupDetail struct {
	Group                domain.Group
	Members              []GroupMember
	Accesses             []GroupAccess
	AvailableUsers       []UserOption
	AvailableConnections []ConnOption
	CanDelete            bool
}

// GroupService berisi aturan pengelolaan group, sessions dipakai untuk memaksa rotasi saat hak akses berubah
type GroupService struct {
	groups   groupStore
	sessions contract.SessionAdmin
}

// NewGroupService dibuat dengan penyimpanan group dan pengelola session
func NewGroupService(groups groupStore, sessions contract.SessionAdmin) *GroupService {
	return &GroupService{groups: groups, sessions: sessions}
}

// validateGroupInput memvalidasi nama dan deskripsi sekaligus, hasilnya sudah dirapikan
func validateGroupInput(in GroupInput) (GroupInput, map[string]string) {
	fields := map[string]string{}
	name, msg := validateGroupName(in.Name)
	if msg != "" {
		fields["name"] = msg
	}
	desc, msg := validateDescription(in.Description)
	if msg != "" {
		fields["description"] = msg
	}
	return GroupInput{Name: name, Description: desc}, fields
}

func (s *GroupService) CreateGroup(ctx context.Context, actor domain.Principal, in GroupInput) (domain.Group, error) {
	if err := requireAdmin(actor); err != nil {
		return domain.Group{}, err
	}
	clean, fields := validateGroupInput(in)
	if len(fields) > 0 {
		return domain.Group{}, &domain.ValidationError{Fields: fields}
	}
	g, err := s.groups.Create(ctx, clean.Name, clean.Description)
	if errors.Is(err, domain.ErrConflict) {
		return domain.Group{}, &domain.ValidationError{Fields: map[string]string{"name": msgGroupNameTaken}}
	}
	if err != nil {
		return domain.Group{}, fmt.Errorf("create group: %w", err)
	}
	return g, nil
}

func (s *GroupService) ListGroups(ctx context.Context, actor domain.Principal) ([]GroupRow, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	rows, err := s.groups.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	return rows, nil
}

// GroupDetail mengumpulkan semua data halaman group dalam satu panggilan
func (s *GroupService) GroupDetail(ctx context.Context, actor domain.Principal, id int64) (GroupDetail, error) {
	if err := requireAdmin(actor); err != nil {
		return GroupDetail{}, err
	}
	g, err := s.groups.Get(ctx, id)
	if err != nil {
		return GroupDetail{}, fmt.Errorf("get group: %w", err)
	}
	members, err := s.groups.ListMembers(ctx, id)
	if err != nil {
		return GroupDetail{}, fmt.Errorf("list members: %w", err)
	}
	access, err := s.groups.ListAccess(ctx, id)
	if err != nil {
		return GroupDetail{}, fmt.Errorf("list access: %w", err)
	}
	users, err := s.groups.AvailableUsers(ctx, id)
	if err != nil {
		return GroupDetail{}, fmt.Errorf("list available users: %w", err)
	}
	conns, err := s.groups.AvailableConnections(ctx)
	if err != nil {
		return GroupDetail{}, fmt.Errorf("list available connections: %w", err)
	}
	return GroupDetail{
		Group:                g,
		Members:              members,
		Accesses:             access,
		AvailableUsers:       users,
		AvailableConnections: conns,
		// Cuman boleh hapus kalau gaada anggota dan akses
		CanDelete: len(members) == 0 && len(access) == 0,
	}, nil
}

func (s *GroupService) UpdateGroup(ctx context.Context, actor domain.Principal, id int64, in GroupInput) (domain.Group, error) {
	if err := requireAdmin(actor); err != nil {
		return domain.Group{}, err
	}
	clean, fields := validateGroupInput(in)
	if len(fields) > 0 {
		return domain.Group{}, &domain.ValidationError{Fields: fields}
	}
	g, err := s.groups.Update(ctx, id, clean.Name, clean.Description)
	if errors.Is(err, domain.ErrConflict) {
		return domain.Group{}, &domain.ValidationError{Fields: map[string]string{"name": msgGroupNameTaken}}
	}
	if err != nil {
		return domain.Group{}, fmt.Errorf("update group: %w", err)
	}
	return g, nil
}

func (s *GroupService) DeleteGroup(ctx context.Context, actor domain.Principal, id int64) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	if err := s.groups.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete group: %w", err)
	}
	return nil
}

// AddMember menambah anggota, user yang tidak ada jadi pesan validasi bukan error server
func (s *GroupService) AddMember(ctx context.Context, actor domain.Principal, groupID, userID int64) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	if _, err := s.groups.Get(ctx, groupID); err != nil {
		return fmt.Errorf("get group: %w", err)
	}
	added, err := s.groups.AddMember(ctx, groupID, userID)
	if errors.Is(err, domain.ErrNotFound) {
		return &domain.ValidationError{Fields: map[string]string{"user_id": msgUserMissing}}
	}
	if err != nil {
		return fmt.Errorf("add member: %w", err)
	}

	// Rotasi cuma kalau anggotanya bener-bener baru
	if added {
		if err := s.sessions.MarkRotationNeeded(ctx, userID); err != nil {
			return fmt.Errorf("mark rotation: %w", err)
		}
	}
	return nil
}

func (s *GroupService) RemoveMember(ctx context.Context, actor domain.Principal, groupID, userID int64) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	if err := s.groups.RemoveMember(ctx, groupID, userID); err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	return nil
}

// GrantAccess memberi atau mengubah level akses group ke satu database
func (s *GroupService) GrantAccess(ctx context.Context, actor domain.Principal, groupID, connID int64, level domain.AccessLevel) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	if !level.Valid() {
		return &domain.ValidationError{Fields: map[string]string{"access_level": msgAccessLevelInvalid}}
	}
	if _, err := s.groups.Get(ctx, groupID); err != nil {
		return fmt.Errorf("get group: %w", err)
	}

	previous, err := s.groups.GrantAccess(ctx, groupID, connID, level, actor.UserID)
	switch {
	case errors.Is(err, domain.ErrConnectionInactive):
		return &domain.ValidationError{Fields: map[string]string{"connection_id": msgConnectionInactive}}
	case errors.Is(err, domain.ErrNotFound):
		return &domain.ValidationError{Fields: map[string]string{"connection_id": msgConnectionMissing}}
	case err != nil:
		return fmt.Errorf("grant access: %w", err)
	}

	// Rotasi cuman kalau level naik
	if level.Rank() > previous.Rank() {
		ids, err := s.groups.MemberIDs(ctx, groupID)
		if err != nil {
			return fmt.Errorf("list member ids: %w", err)
		}
		if len(ids) > 0 {
			if err := s.sessions.MarkRotationNeeded(ctx, ids...); err != nil {
				return fmt.Errorf("mark rotation: %w", err)
			}
		}
	}
	return nil
}

func (s *GroupService) RevokeAccess(ctx context.Context, actor domain.Principal, groupID, connID int64) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	if err := s.groups.RevokeAccess(ctx, groupID, connID); err != nil {
		return fmt.Errorf("revoke access: %w", err)
	}
	return nil
}

func (s *GroupService) SetLeader(ctx context.Context, actor domain.Principal, groupID, userID int64) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	if err := s.groups.SetLeader(ctx, groupID, userID); err != nil {
		return err
	}
	// Leader mendapat hak baru (menyetujui eksekusi), sama seperti kenaikan akses.
	if err := s.sessions.MarkRotationNeeded(ctx, userID); err != nil {
		return fmt.Errorf("mark rotation: %w", err)
	}
	return nil
}
