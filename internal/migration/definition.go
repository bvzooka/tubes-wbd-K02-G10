package migration

import (
	"context"
	"fmt"
	"log/slog"

	"dbplatform/internal/contract"
	"dbplatform/internal/domain"
)

type definitionStore interface {
	CreateDefinition(ctx context.Context, connID int64, in DraftInput, createdBy int64) (Definition, error)
	GetDefinition(ctx context.Context, connID int64, version int) (Definition, error)
	ListDefinitions(ctx context.Context, connID int64) ([]Definition, error)
	UpdateDefinition(ctx context.Context, connID int64, version, revision int, in DraftInput) (Definition, error)
	MoveDefinition(ctx context.Context, d Definition, to domain.MigrationState) error
}

// DefinitionService mengurus isi migration dari draft sampai beku atau dibuang, belum sampai dijalankan
type DefinitionService struct {
	store  definitionStore
	authz  contract.Authorizer
	logger *slog.Logger
}

func NewDefinitionService(store definitionStore, authz contract.Authorizer, logger *slog.Logger) *DefinitionService {
	return &DefinitionService{store: store, authz: authz, logger: logger}
}

func (s *DefinitionService) List(ctx context.Context, actor domain.Principal, connID int64) ([]Definition, error) {
	if err := requireAccess(ctx, s.authz, actor, connID, domain.AccessViewOnly); err != nil {
		return nil, err
	}
	return s.store.ListDefinitions(ctx, connID)
}

func (s *DefinitionService) Get(ctx context.Context, actor domain.Principal, connID int64, version int) (Definition, error) {
	if err := requireAccess(ctx, s.authz, actor, connID, domain.AccessViewOnly); err != nil {
		return Definition{}, err
	}
	return s.store.GetDefinition(ctx, connID, version)
}

func (s *DefinitionService) Create(ctx context.Context, actor domain.Principal, connID int64, in DraftInput) (Definition, error) {
	if err := requireAccess(ctx, s.authz, actor, connID, domain.AccessViewEdit); err != nil {
		return Definition{}, err
	}
	clean, fields := cleanDraft(in)
	if len(fields) > 0 {
		return Definition{}, &domain.ValidationError{Fields: fields}
	}
	d, err := s.store.CreateDefinition(ctx, connID, clean, actor.UserID)
	if err != nil {
		return Definition{}, err
	}
	s.record(ctx, "migration.created", actor, d)
	return d, nil
}

// Update memerlukan revisi yang dibaca user supaya perubahan orang lain tidak tertimpa
func (s *DefinitionService) Update(ctx context.Context, actor domain.Principal, connID int64, version, revision int, in DraftInput) (Definition, error) {
	if err := requireAccess(ctx, s.authz, actor, connID, domain.AccessViewEdit); err != nil {
		return Definition{}, err
	}
	clean, fields := cleanDraft(in)
	if len(fields) > 0 {
		return Definition{}, &domain.ValidationError{Fields: fields}
	}
	d, err := s.store.UpdateDefinition(ctx, connID, version, revision, clean)
	if err != nil {
		return Definition{}, err
	}
	s.record(ctx, "migration.updated", actor, d)
	return d, nil
}

// Freeze mengunci SQL supaya yang disetujui leader sama persis dengan yang dijalankan
func (s *DefinitionService) Freeze(ctx context.Context, actor domain.Principal, connID int64, version, revision int) (Definition, error) {
	return s.transition(ctx, actor, connID, version, domain.StateReady, "migration.frozen", func(d Definition) error {
		if d.State != domain.StateDraft {
			return refuse(domain.ErrConflict, "Hanya draft yang bisa dibekukan.")
		}
		// user harus membekukan isi yang dia lihat, bukan isi yang baru saja diubah orang lain
		if d.Revision != revision {
			return refuse(domain.ErrConflict, "Versi %d sudah berubah, muat ulang halamannya.", d.Version)
		}
		// divalidasi ulang karena aturan saat simpan lebih longgar
		clean, fields := cleanDraft(d.DraftInput)
		if len(fields) == 0 {
			fields = checkReady(clean)
		}
		if len(fields) > 0 {
			return &domain.ValidationError{Fields: fields}
		}
		return nil
	})
}

// Reopen membuka lagi migration beku yang belum pernah dijalankan, misalnya setelah ditolak
func (s *DefinitionService) Reopen(ctx context.Context, actor domain.Principal, connID int64, version int) (Definition, error) {
	return s.transition(ctx, actor, connID, version, domain.StateDraft, "migration.reopened", func(d Definition) error {
		if d.State != domain.StateReady {
			return refuse(domain.ErrConflict, "Hanya migration yang belum pernah dijalankan yang bisa dibuka lagi.")
		}
		return nil
	})
}

func (s *DefinitionService) Cancel(ctx context.Context, actor domain.Principal, connID int64, version int) (Definition, error) {
	return s.transition(ctx, actor, connID, version, domain.StateCancelled, "migration.cancelled", func(d Definition) error {
		if d.State != domain.StateDraft {
			return refuse(domain.ErrConflict, "Hanya draft yang bisa dibatalkan.")
		}
		return nil
	})
}

func (s *DefinitionService) Supersede(ctx context.Context, actor domain.Principal, connID int64, version int) (Definition, error) {
	return s.transition(ctx, actor, connID, version, domain.StateSuperseded, "migration.superseded", func(d Definition) error {
		return checkSupersede(versionState{Version: d.Version, State: d.State})
	})
}

// transition adalah alur yang sama untuk semua ganti status: cek akses, cek syarat, pindahkan, catat
func (s *DefinitionService) transition(
	ctx context.Context, actor domain.Principal, connID int64, version int,
	to domain.MigrationState, event string, allowed func(Definition) error,
) (Definition, error) {
	if err := requireAccess(ctx, s.authz, actor, connID, domain.AccessViewEdit); err != nil {
		return Definition{}, err
	}
	d, err := s.store.GetDefinition(ctx, connID, version)
	if err != nil {
		return Definition{}, err
	}
	if err := allowed(d); err != nil {
		return Definition{}, err
	}
	if err := s.store.MoveDefinition(ctx, d, to); err != nil {
		return Definition{}, err
	}
	moved, err := s.store.GetDefinition(ctx, connID, version)
	if err != nil {
		return Definition{}, err
	}
	s.record(ctx, event, actor, moved)
	return moved, nil
}

// record mencatat aktivitas tanpa isi SQL karena SQL bisa memuat data sensitif
func (s *DefinitionService) record(ctx context.Context, event string, actor domain.Principal, d Definition) {
	s.logger.InfoContext(ctx, "migration definition changed",
		"event", event, "actor", actor.Username, "connection_id", d.ConnectionID,
		"migration_id", d.ID, "version", d.Version, "revision", d.Revision, "status", d.State)
}

func requireAccess(ctx context.Context, authz contract.Authorizer, actor domain.Principal, connID int64, min domain.AccessLevel) error {
	ok, err := authz.Can(ctx, actor.UserID, connID, min)
	if err != nil {
		return fmt.Errorf("check access: %w", err)
	}
	if !ok {
		return domain.ErrForbidden
	}
	return nil
}
