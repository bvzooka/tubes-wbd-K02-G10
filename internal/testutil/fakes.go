package testutil

import (
	"context"
	"sync"

	"dbplatform/internal/contract"
	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
)

// FakeDriverProvider memenuhi contract.DriverProvider dengan driver yang didaftarkan manual
type FakeDriverProvider struct {
	mu      sync.Mutex
	drivers map[int64]dbdriver.Driver
}

var _ contract.DriverProvider = (*FakeDriverProvider)(nil)

func NewFakeDriverProvider() *FakeDriverProvider {
	return &FakeDriverProvider{drivers: map[int64]dbdriver.Driver{}}
}

// Set mendaftarkan driver untuk satu koneksi. Mengembalikan receiver agar bisa dirantai
func (p *FakeDriverProvider) Set(connectionID int64, d dbdriver.Driver) *FakeDriverProvider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.drivers[connectionID] = d
	return p
}

func (p *FakeDriverProvider) Get(_ context.Context, connectionID int64) (dbdriver.Driver, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	d, ok := p.drivers[connectionID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return d, nil
}

// SpySessionAdmin memenuhi contract.SessionAdmin dan mencatat pemanggilan
type SpySessionAdmin struct {
	mu       sync.Mutex
	revoked  []int64
	rotation []int64
}

var _ contract.SessionAdmin = (*SpySessionAdmin)(nil)

func (s *SpySessionAdmin) RevokeAllForUser(_ context.Context, userID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked = append(s.revoked, userID)
	return nil
}

func (s *SpySessionAdmin) MarkRotationNeeded(_ context.Context, userIDs ...int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rotation = append(s.rotation, userIDs...)
	return nil
}

// Revoked mengembalikan salinan id user yang pernah dicabut sessionnya
func (s *SpySessionAdmin) Revoked() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int64(nil), s.revoked...)
}

// RotationMarked mengembalikan salinan id user yang pernah ditandai perlu rotasi
func (s *SpySessionAdmin) RotationMarked() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int64(nil), s.rotation...)
}
