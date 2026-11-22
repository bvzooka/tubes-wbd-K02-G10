// Package connmgr mengelola koneksi ke database target: simpan, ubah, uji, dan sediakan driver-nya
package connmgr

import (
	"context"
	"time"

	"dbplatform/internal/domain"
)

// batas waktu uji koneksi supaya host yang tidak merespons tidak menahan request
const testTimeout = 10 * time.Second

// Service berisi aturan pengelolaan koneksi, semuanya hanya boleh dilakukan Platform Admin
type Service struct {
	repo    *Repo
	factory DriverFactory
}

// NewService dibuat dengan repo koneksi dan pembuat driver untuk uji koneksi
func NewService(repo *Repo, factory DriverFactory) *Service {
	return &Service{repo: repo, factory: factory}
}

// Create memvalidasi input lalu menyimpan koneksi baru atas nama admin p
func (s *Service) Create(ctx context.Context, p domain.Principal, in domain.ConnectionInput) (domain.Connection, error) {
	if err := requireAdmin(p); err != nil {
		return domain.Connection{}, err
	}
	if err := ValidateInput(in, false); err != nil {
		return domain.Connection{}, err
	}
	return s.repo.Insert(ctx, NormalizeInput(in), p.UserID)
}

// Update memvalidasi input lalu mengubah koneksi id, password kosong berarti tidak diubah
func (s *Service) Update(ctx context.Context, p domain.Principal, id int64, in domain.ConnectionInput) (domain.Connection, error) {
	if err := requireAdmin(p); err != nil {
		return domain.Connection{}, err
	}
	if err := ValidateInput(in, true); err != nil {
		return domain.Connection{}, err
	}
	return s.repo.Update(ctx, id, NormalizeInput(in))
}

// setActive mengaktifkan atau menonaktifkan koneksi id
func (s *Service) SetActive(ctx context.Context, p domain.Principal, id int64, active bool) error {
	if err := requireAdmin(p); err != nil {
		return err
	}
	return s.repo.SetActive(ctx, id, active)
}

// list mengembalikan semua koneksi tanpa password
func (s *Service) List(ctx context.Context, p domain.Principal) ([]domain.Connection, error) {
	if err := requireAdmin(p); err != nil {
		return nil, err
	}
	return s.repo.List(ctx)
}

// mengembalikan satu koneksi tanpa password, ErrNotFound bila tidak ada
func (s *Service) Get(ctx context.Context, p domain.Principal, id int64) (domain.Connection, error) {
	if err := requireAdmin(p); err != nil {
		return domain.Connection{}, err
	}
	return s.repo.Get(ctx, id)
}

// TestConnection mencoba terhubung memakai input form, password kosong saat edit berarti memakai password tersimpan
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

// requireAdmin dicek di service dan bukan hanya di middleware, supaya aturannya tetap berlaku dari pemanggil mana pun
func requireAdmin(p domain.Principal) error {
	if !p.IsPlatformAdmin {
		return domain.ErrForbidden
	}
	return nil
}
