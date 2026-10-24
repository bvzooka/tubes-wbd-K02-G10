package admin

import (
	"context"
	"errors"
	"fmt"

	"dbplatform/internal/contract"
	"dbplatform/internal/domain"
)

type userStore interface {
	Create(ctx context.Context, username, passwordHash string, isAdmin bool) (domain.User, error)
	List(ctx context.Context) ([]domain.User, error)
}

type UserInput struct {
	Username        string
	Password        string
	IsPlatformAdmin bool
}

type Service struct {
	users    userStore
	hasher   contract.PasswordHasher
	sessions contract.SessionAdmin
}

func NewService(users userStore, hasher contract.PasswordHasher, sessions contract.SessionAdmin) *Service {
	return &Service{users: users, hasher: hasher, sessions: sessions}
}

func requireAdmin(actor domain.Principal) error {
	if !actor.IsPlatformAdmin {
		return domain.ErrForbidden
	}
	return nil
}

func (s *Service) CreateUser(ctx context.Context, actor domain.Principal, in UserInput) (domain.User, error) {
	if err := requireAdmin(actor); err != nil {
		return domain.User{}, err
	}

	username, msg := validateUsername(in.Username)
	fields := map[string]string{}
	if msg != "" {
		fields["username"] = msg
	}
	if msg := validatePassword(in.Password); msg != "" {
		fields["password"] = msg
	}
	if len(fields) > 0 {
		return domain.User{}, &domain.ValidationError{Fields: fields}
	}

	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return domain.User{}, fmt.Errorf("hash password: %w", err)
	}
	u, err := s.users.Create(ctx, username, hash, in.IsPlatformAdmin)
	if errors.Is(err, domain.ErrConflict) {
		return domain.User{}, &domain.ValidationError{Fields: map[string]string{"username": msgUsernameTaken}}
	}
	if err != nil {
		return domain.User{}, err
	}
	return u, nil
}

func (s *Service) ListUsers(ctx context.Context, actor domain.Principal) ([]domain.User, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	return s.users.List(ctx)
}
