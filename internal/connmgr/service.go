package connmgr

import (
	"context"
	"time"

	"dbplatform/internal/domain"
)

const testTimeout = 10 * time.Second

type Service struct {
	repo    *Repo
	factory DriverFactory
}

func NewService(repo *Repo, factory DriverFactory) *Service {
	return &Service{repo: repo, factory: factory}
}

func requireAdmin(p domain.Principal) error {
	if !p.IsPlatformAdmin {
		return domain.ErrForbidden
	}
	return nil
}

func (s *Service) Create(ctx context.Context, p domain.Principal, in domain.ConnectionInput) (domain.Connection, error) {
	if err := requireAdmin(p); err != nil {
		return domain.Connection{}, err
	}
	if err := ValidateInput(in, false); err != nil {
		return domain.Connection{}, err
	}
	return s.repo.Insert(ctx, NormalizeInput(in), p.UserID)
}

func (s *Service) Update(ctx context.Context, p domain.Principal, id int64, in domain.ConnectionInput) (domain.Connection, error) {
	if err := requireAdmin(p); err != nil {
		return domain.Connection{}, err
	}
	if err := ValidateInput(in, true); err != nil {
		return domain.Connection{}, err
	}
	return s.repo.Update(ctx, id, NormalizeInput(in))
}

func (s *Service) SetActive(ctx context.Context, p domain.Principal, id int64, active bool) error {
	if err := requireAdmin(p); err != nil {
		return err
	}
	return s.repo.SetActive(ctx, id, active)
}

func (s *Service) List(ctx context.Context, p domain.Principal) ([]domain.Connection, error) {
	if err := requireAdmin(p); err != nil {
		return nil, err
	}
	return s.repo.List(ctx)
}

func (s *Service) Get(ctx context.Context, p domain.Principal, id int64) (domain.Connection, error) {
	if err := requireAdmin(p); err != nil {
		return domain.Connection{}, err
	}
	return s.repo.Get(ctx, id)
}


func (s *Service) TestConnection(ctx context.Context, p domain.Principal, id int64, in domain.ConnectionInput) error {
	if err := requireAdmin(p); err != nil {
		return err
	}
	if err := ValidateInput(in, id > 0); err != nil {
		return err
	}
	in = NormalizeInput(in)
	password := in.Password
	if password == "" {
		_, stored, err := s.repo.Load(ctx, id)
		if err != nil {
			return err
		}
		password = stored
	}
	conn := domain.Connection{
		DBMS: in.DBMS, Host: in.Host, Port: in.Port,
		DatabaseName: in.DatabaseName, Username: in.Username, SSLMode: in.SSLMode,
	}
	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	drv, err := s.factory(ctx, conn, password)
	if err != nil {
		return err
	}
	defer drv.Close()
	return drv.Ping(ctx)
}
