package admin

import (
	"context"
	"errors"
	"fmt"

	"dbplatform/internal/contract"
	"dbplatform/internal/domain"
)

// userStore adalah penyimpanan user yang dibutuhkan service, implementasinya UserRepo
type userStore interface {
	Create(ctx context.Context, username, passwordHash string, isAdmin bool) (domain.User, error)
	List(ctx context.Context) ([]domain.User, error)
	Get(ctx context.Context, id int64) (domain.User, error)
	Update(ctx context.Context, id int64, passwordHash string, isAdmin bool) (domain.User, bool, error)
	SetActive(ctx context.Context, id int64, active bool) (domain.User, error)
}

// UserInput adalah isi form buat user baru
type UserInput struct {
	Username        string
	Password        string
	IsPlatformAdmin bool
}

// UserUpdate adalah isi form ubah user, password kosong berarti tidak diganti
type UserUpdate struct {
	Password        string
	IsPlatformAdmin bool
}

// Service berisi aturan pengelolaan user, hasher dan sessions untuk menyentuh password dan session
type Service struct {
	users    userStore
	hasher   contract.PasswordHasher
	sessions contract.SessionAdmin
}

// NewService dibuat dengan penyimpanan user, hasher password, dan pengelola session
func NewService(users userStore, hasher contract.PasswordHasher, sessions contract.SessionAdmin) *Service {
	return &Service{users: users, hasher: hasher, sessions: sessions}
}

// requireAdmin dicek di service dan bukan hanya di middleware, supaya aturannya berlaku dari pemanggil mana pun
func requireAdmin(actor domain.Principal) error {
	if !actor.IsPlatformAdmin {
		return domain.ErrForbidden
	}
	return nil
}

// CreateUser memvalidasi semua field sekaligus supaya form bisa menampilkan semua kesalahan dalam satu kali
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

// ListUsers, GetUser, dan ActivateUser hanya meneruskan ke repo setelah cek admin
func (s *Service) ListUsers(ctx context.Context, actor domain.Principal) ([]domain.User, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	return s.users.List(ctx)
}

func (s *Service) GetUser(ctx context.Context, actor domain.Principal, id int64) (domain.User, error) {
	if err := requireAdmin(actor); err != nil {
		return domain.User{}, err
	}
	return s.users.Get(ctx, id)
}

// UpdateUser mengubah password dan peran admin, dengan efek ke session user itu
func (s *Service) UpdateUser(ctx context.Context, actor domain.Principal, id int64, in UserUpdate) (domain.User, error) {
	if err := requireAdmin(actor); err != nil {
		return domain.User{}, err
	}
	if in.Password != "" {
		if msg := validatePassword(in.Password); msg != "" {
			return domain.User{}, &domain.ValidationError{Fields: map[string]string{"password": msg}}
		}
	}

	// Admin gaboleh cabut admin dirinya sendiri
	if actor.UserID == id && !in.IsPlatformAdmin {
		return domain.User{}, ErrSelfDemote
	}

	var hash string
	if in.Password != "" {
		h, err := s.hasher.Hash(in.Password)
		if err != nil {
			return domain.User{}, fmt.Errorf("hash password: %w", err)
		}
		hash = h
	}

	u, wasAdmin, err := s.users.Update(ctx, id, hash, in.IsPlatformAdmin)
	if err != nil {
		return domain.User{}, err
	}

	// Ganti password maka semua session user itu dicabut
	if in.Password != "" {
		if err := s.sessions.RevokeAllForUser(ctx, id); err != nil {
			return domain.User{}, fmt.Errorf("revoke sessions: %w", err)
		}
	}

	// Hak naik, token lama diganti baru
	if !wasAdmin && u.IsPlatformAdmin {
		if err := s.sessions.MarkRotationNeeded(ctx, id); err != nil {
			return domain.User{}, fmt.Errorf("mark rotation: %w", err)
		}
	}
	return u, nil
}

// DeactivateUser menonaktifkan user lalu mencabut semua session-nya supaya langsung keluar
func (s *Service) DeactivateUser(ctx context.Context, actor domain.Principal, id int64) (domain.User, error) {
	if err := requireAdmin(actor); err != nil {
		return domain.User{}, err
	}

	// Gaboleh nonaktifin diri sendiri
	if actor.UserID == id {
		return domain.User{}, ErrSelfDeactivate
	}
	u, err := s.users.SetActive(ctx, id, false)
	if err != nil {
		return domain.User{}, err
	}
	if err := s.sessions.RevokeAllForUser(ctx, id); err != nil {
		return domain.User{}, fmt.Errorf("revoke sessions: %w", err)
	}
	return u, nil
}

func (s *Service) ActivateUser(ctx context.Context, actor domain.Principal, id int64) (domain.User, error) {
	if err := requireAdmin(actor); err != nil {
		return domain.User{}, err
	}
	return s.users.SetActive(ctx, id, true)
}
