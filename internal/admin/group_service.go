package admin

import (
	"context"
	"errors"
	"fmt"

	"dbplatform/internal/contract"
	"dbplatform/internal/domain"
)

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
	ListAccess(ctx context.Context, groupID int64) ([]GroupAccess, error)
	GrantAccess(ctx context.Context, groupID, connID int64, level domain.AccessLevel, grantedBy int64) (domain.AccessLevel, error)
	RevokeAccess(ctx context.Context, groupID, connID int64) error
	AvailableUsers(ctx context.Context, groupID int64) ([]UserOption, error)
	AvailableConnections(ctx context.Context) ([]ConnOption, error)
}

type GroupInput struct {
	Name        string
	Description string
}

type GroupDetail struct {
	Group                domain.Group
	Members              []GroupMember
	Accesses             []GroupAccess
	AvailableUsers       []UserOption
	AvailableConnections []ConnOption
	CanDelete            bool
}

type GroupService struct {
	groups   groupStore
	sessions contract.SessionAdmin
}

func NewGroupService(groups groupStore, sessions contract.SessionAdmin) *GroupService {
	return &GroupService{groups: groups, sessions: sessions}
}

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
	return g, err
}

func (s *GroupService) ListGroups(ctx context.Context, actor domain.Principal) ([]GroupRow, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	return s.groups.List(ctx)
}

func (s *GroupService) GroupDetail(ctx context.Context, actor domain.Principal, id int64) (GroupDetail, error) {
	if err := requireAdmin(actor); err != nil {
		return GroupDetail{}, err
	}
	g, err := s.groups.Get(ctx, id)
	if err != nil {
		return GroupDetail{}, err
	}
	members, err := s.groups.ListMembers(ctx, id)
	if err != nil {
		return GroupDetail{}, err
	}
	access, err := s.groups.ListAccess(ctx, id)
	if err != nil {
		return GroupDetail{}, err
	}
	users, err := s.groups.AvailableUsers(ctx, id)
	if err != nil {
		return GroupDetail{}, err
	}
	conns, err := s.groups.AvailableConnections(ctx)
	if err != nil {
		return GroupDetail{}, err
	}
	return GroupDetail{
		Group:                g,
		Members:              members,
		Accesses:             access,
		AvailableUsers:       users,
		AvailableConnections: conns,
		CanDelete:            len(members) == 0 && len(access) == 0,
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
	return g, err
}

func (s *GroupService) DeleteGroup(ctx context.Context, actor domain.Principal, id int64) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	return s.groups.Delete(ctx, id)
}

func (s *GroupService) AddMember(ctx context.Context, actor domain.Principal, groupID, userID int64) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	if _, err := s.groups.Get(ctx, groupID); err != nil {
		return err
	}
	added, err := s.groups.AddMember(ctx, groupID, userID)
	if errors.Is(err, domain.ErrNotFound) {
		return &domain.ValidationError{Fields: map[string]string{"user_id": msgUserMissing}}
	}
	if err != nil {
		return err
	}
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
	return s.groups.RemoveMember(ctx, groupID, userID)
}

func (s *GroupService) GrantAccess(ctx context.Context, actor domain.Principal, groupID, connID int64, level domain.AccessLevel) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	if !level.Valid() {
		return &domain.ValidationError{Fields: map[string]string{"access_level": msgAccessLevelInvalid}}
	}
	if _, err := s.groups.Get(ctx, groupID); err != nil {
		return err
	}

	previous, err := s.groups.GrantAccess(ctx, groupID, connID, level, actor.UserID)
	switch {
	case errors.Is(err, domain.ErrConnectionInactive):
		return &domain.ValidationError{Fields: map[string]string{"connection_id": msgConnectionInactive}}
	case errors.Is(err, domain.ErrNotFound):
		return &domain.ValidationError{Fields: map[string]string{"connection_id": msgConnectionMissing}}
	case err != nil:
		return err
	}

	if level.Rank() > previous.Rank() {
		ids, err := s.groups.MemberIDs(ctx, groupID)
		if err != nil {
			return err
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
	return s.groups.RevokeAccess(ctx, groupID, connID)
}
