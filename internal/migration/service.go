package migration

import (
	"context"
	"fmt"

	"dbplatform/internal/contract"
	"dbplatform/internal/domain"
)

type store interface {
	Create(ctx context.Context, connID int64, name, sql, checksum string, createdBy int64) (domain.Migration, error)
	Get(ctx context.Context, connID int64, version int) (domain.Migration, error)
	List(ctx context.Context, connID int64) ([]domain.Migration, error)
	UpdateDraft(ctx context.Context, connID int64, version int, name, sql, checksum string) (domain.Migration, error)
	Cancel(ctx context.Context, connID int64, version int) error
}

type Service struct {
	store store
	authz contract.Authorizer
}

func NewService(store store, authz contract.Authorizer) *Service {
	return &Service{store: store, authz: authz}
}

func (s *Service) require(ctx context.Context, actor domain.Principal, connID int64, min domain.AccessLevel) error {
	ok, err := s.authz.Can(ctx, actor.UserID, connID, min)
	if err != nil {
		return fmt.Errorf("check access: %w", err)
	}
	if !ok {
		return domain.ErrForbidden
	}
	return nil
}

func (s *Service) List(ctx context.Context, actor domain.Principal, connID int64) ([]domain.Migration, error) {
	if err := s.require(ctx, actor, connID, domain.AccessViewOnly); err != nil {
		return nil, err
	}
	return s.store.List(ctx, connID)
}

func (s *Service) Get(ctx context.Context, actor domain.Principal, connID int64, version int) (domain.Migration, error) {
	if err := s.require(ctx, actor, connID, domain.AccessViewOnly); err != nil {
		return domain.Migration{}, err
	}
	return s.store.Get(ctx, connID, version)
}

func (s *Service) Create(ctx context.Context, actor domain.Principal, connID int64, in Input) (domain.Migration, error) {
	if err := s.require(ctx, actor, connID, domain.AccessViewEdit); err != nil {
		return domain.Migration{}, err
	}
	clean, fields := Validate(in)
	if len(fields) > 0 {
		return domain.Migration{}, &domain.ValidationError{Fields: fields}
	}
	return s.store.Create(ctx, connID, clean.Name, clean.SQL, Checksum(clean.SQL), actor.UserID)
}

func (s *Service) Update(ctx context.Context, actor domain.Principal, connID int64, version int, in Input) (domain.Migration, error) {
	if err := s.require(ctx, actor, connID, domain.AccessViewEdit); err != nil {
		return domain.Migration{}, err
	}
	clean, fields := Validate(in)
	if len(fields) > 0 {
		return domain.Migration{}, &domain.ValidationError{Fields: fields}
	}
	return s.store.UpdateDraft(ctx, connID, version, clean.Name, clean.SQL, Checksum(clean.SQL))
}

func (s *Service) Cancel(ctx context.Context, actor domain.Principal, connID int64, version int) error {
	if err := s.require(ctx, actor, connID, domain.AccessViewEdit); err != nil {
		return err
	}
	return s.store.Cancel(ctx, connID, version)
}
