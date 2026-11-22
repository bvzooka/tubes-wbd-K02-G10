package schemadiff

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"dbplatform/internal/contract"
	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
	"dbplatform/internal/migration"
)

// batas nama migration dan pesan error form schema diff
const (
	maxNameChars = 120

	msgSameDatabase  = "Pilih database sumber yang berbeda dari database tujuan."
	msgNothingChosen = "Pilih minimal satu perubahan."
	msgStaleChoice   = "Perubahan yang dipilih sudah tidak berlaku karena schema berubah. Hitung ulang perbedaannya."
)

// Pakai service migration, ga nyentuh database platform sendiri
type migrationCreator interface {
	Create(ctx context.Context, actor domain.Principal, connID int64, in migration.Input) (domain.Migration, error)
}

// Service menghitung perbedaan schema dan membuat draft migration dari perubahan yang dipilih user
type Service struct {
	authz      contract.Authorizer
	drivers    contract.DriverProvider
	migrations migrationCreator
}

// NewService dibuat dengan authorizer, penyedia driver, dan pembuat migration
func NewService(authz contract.Authorizer, drivers contract.DriverProvider, migrations migrationCreator) *Service {
	return &Service{authz: authz, drivers: drivers, migrations: migrations}
}

// Result adalah hasil perbandingan: database tujuan dan sumber beserta daftar perubahannya
type Result struct {
	TargetID   int64
	TargetName string
	SourceID   int64
	SourceName string
	Changes    []Change
}

// can memeriksa akses user ke satu database, ErrForbidden kalau levelnya kurang
func (s *Service) can(ctx context.Context, actor domain.Principal, connID int64, min domain.AccessLevel) error {
	ok, err := s.authz.Can(ctx, actor.UserID, connID, min)
	if err != nil {
		return fmt.Errorf("check access: %w", err)
	}
	if !ok {
		return domain.ErrForbidden
	}
	return nil
}

// PostgreSQL aja, bukan tujuan, dan harus bisa dibaca user
func (s *Service) Sources(ctx context.Context, actor domain.Principal, targetID int64) ([]domain.AccessibleDatabase, error) {
	if err := s.can(ctx, actor, targetID, domain.AccessViewEdit); err != nil {
		return nil, err
	}
	all, err := s.authz.ListAccessible(ctx, actor.UserID)
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}
	var out []domain.AccessibleDatabase
	for _, d := range all {
		if d.ConnectionID != targetID && d.DBMS == domain.DBMSPostgres && d.Level.AtLeast(domain.AccessViewOnly) {
			out = append(out, d)
		}
	}
	return out, nil
}

// Butuh view_edit di tujuan, view_only di sumber
func (s *Service) Preview(ctx context.Context, actor domain.Principal, targetID, sourceID int64) (*Result, error) {
	if err := s.can(ctx, actor, targetID, domain.AccessViewEdit); err != nil {
		return nil, err
	}
	if sourceID == targetID {
		return nil, &domain.ValidationError{Fields: map[string]string{"source_id": msgSameDatabase}}
	}
	if err := s.can(ctx, actor, sourceID, domain.AccessViewOnly); err != nil {
		return nil, err
	}

	names, err := s.names(ctx, actor, targetID, sourceID)
	if err != nil {
		return nil, err
	}
	targetSchema, err := s.introspect(ctx, targetID)
	if err != nil {
		return nil, err
	}
	sourceSchema, err := s.introspect(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	return &Result{
		TargetID: targetID, TargetName: names[targetID],
		SourceID: sourceID, SourceName: names[sourceID],
		Changes: Diff(targetSchema, sourceSchema),
	}, nil
}

// CreateDraft membuat migration draft di database tujuan dari perubahan yang dipilih
func (s *Service) CreateDraft(ctx context.Context, actor domain.Principal, targetID, sourceID int64, selected []string) (domain.Migration, error) {
	// Diff dihitung ulang di server, SQL ga pernah ambil dari browser
	result, err := s.Preview(ctx, actor, targetID, sourceID)
	if err != nil {
		return domain.Migration{}, err
	}

	ids := make(map[string]bool, len(selected))
	for _, id := range selected {
		if id = strings.TrimSpace(id); id != "" {
			ids[id] = true
		}
	}
	if len(ids) == 0 {
		return domain.Migration{}, &domain.ValidationError{Fields: map[string]string{"changes": msgNothingChosen}}
	}
	known := make(map[string]bool, len(result.Changes))
	for _, c := range result.Changes {
		known[c.ID] = true
	}
	for id := range ids {
		// Kalau ada ID asing, schema udah berubah sejak preview
		if !known[id] {
			return domain.Migration{}, &domain.ValidationError{Fields: map[string]string{"changes": msgStaleChoice}}
		}
	}

	name := truncate("Schema diff dari "+result.SourceName, maxNameChars)
	return s.migrations.Create(ctx, actor, targetID, migration.Input{Name: name, SQL: SQL(Select(result.Changes, ids))})
}

// introspect membaca schema satu database, hanya PostgreSQL karena SQL yang dibuat memakai sintaks PostgreSQL
func (s *Service) introspect(ctx context.Context, connID int64) (*domain.Schema, error) {
	d, err := s.drivers.Get(ctx, connID)
	if err != nil {
		return nil, err
	}
	if d.Dialect() != domain.DBMSPostgres {
		return nil, fmt.Errorf("%w: schema diff hanya mendukung PostgreSQL", domain.ErrUnsupported)
	}
	return introspectWith(ctx, d)
}

// introspectWith membungkus error pembacaan schema dengan konteksnya
func introspectWith(ctx context.Context, d dbdriver.Driver) (*domain.Schema, error) {
	schema, err := d.Introspect(ctx)
	if err != nil {
		return nil, fmt.Errorf("introspect: %w", err)
	}
	return schema, nil
}

// names mencari nama database untuk judul hasil, id yang tidak ditemukan diberi nama cadangan
func (s *Service) names(ctx context.Context, actor domain.Principal, ids ...int64) (map[int64]string, error) {
	all, err := s.authz.ListAccessible(ctx, actor.UserID)
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}
	out := make(map[int64]string, len(ids))
	for _, id := range ids {
		out[id] = fmt.Sprintf("database %d", id)
	}
	for _, d := range all {
		if _, wanted := out[d.ConnectionID]; wanted {
			out[d.ConnectionID] = d.Name
		}
	}
	return out, nil
}

// truncate memotong berdasarkan karakter, bukan byte, supaya tidak memutus karakter di tengah
func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}
