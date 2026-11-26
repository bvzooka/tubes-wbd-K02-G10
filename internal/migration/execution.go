package migration

import (
	"context"
	"log/slog"
	"time"

	"dbplatform/internal/appdb"
	"dbplatform/internal/clock"
	"dbplatform/internal/contract"
	"dbplatform/internal/domain"
	"dbplatform/internal/telemetry"
)

type executionStore interface {
	InConnectionTx(ctx context.Context, connID int64, fn func(q appdb.DBTX) error) error
	DefinitionIn(ctx context.Context, q appdb.DBTX, connID int64, version int) (Definition, error)
	VersionStates(ctx context.Context, q appdb.DBTX, connID int64) ([]versionState, error)
	HasOpenExecution(ctx context.Context, q appdb.DBTX, migrationID int64) (bool, error)
	InsertExecution(ctx context.Context, q appdb.DBTX, e domain.Execution) (domain.Execution, error)
	GetExecution(ctx context.Context, id int64) (domain.Execution, error)
	ExecutionIn(ctx context.Context, q appdb.DBTX, id int64) (domain.Execution, error)
	LatestForwardExecution(ctx context.Context, q appdb.DBTX, migrationID int64, statuses ...domain.ExecutionStatus) (domain.Execution, error)
	SetExecutionStatus(ctx context.Context, q appdb.DBTX, id int64, from, to domain.ExecutionStatus, at time.Time) error
	JobID(ctx context.Context, q appdb.DBTX, executionID int64) (string, error)
	ListHistory(ctx context.Context, connID int64, f HistoryFilter, limit, offset int) ([]domain.HistoryEntry, error)
	ListExecutions(ctx context.Context, connID int64, version int) ([]domain.Execution, error)
}

// ExecutionDeps dikumpulkan jadi struct supaya urutan argumen tidak tertukar saat perakitan
type ExecutionDeps struct {
	Store       executionStore
	DB          appdb.DBTX // untuk pembacaan di luar transaction
	Authz       contract.Authorizer
	Approvals   contract.Approvals
	Queue       contract.JobQueue
	Clock       clock.Clock
	Logger      *slog.Logger
	MaxAttempts int
}

// ExecutionService mengurus permintaan eksekusi sampai masuk antrean, yang menjalankan SQL adalah runner
type ExecutionService struct{ deps ExecutionDeps }

func NewExecutionService(deps ExecutionDeps) *ExecutionService {
	return &ExecutionService{deps: deps}
}

// Request mengajukan apply, forward, resume, atau rollback untuk disetujui para leader
func (s *ExecutionService) Request(
	ctx context.Context, actor domain.Principal, connID int64, version int, kind domain.ExecutionKind, resume bool,
) (domain.Execution, error) {
	if err := requireAccess(ctx, s.deps.Authz, actor, connID, domain.AccessViewEdit); err != nil {
		return domain.Execution{}, err
	}
	var created domain.Execution
	err := s.deps.Store.InConnectionTx(ctx, connID, func(q appdb.DBTX) error {
		d, err := s.checkRequest(ctx, q, connID, version, kind, resume)
		if err != nil {
			return err
		}
		e := domain.Execution{
			MigrationID: d.ID, ConnectionID: connID, Version: d.Version, Revision: d.Revision,
			Kind: kind, Mode: d.Mode, Batch: d.Batch, SQLSnapshot: d.UpSQL, Checksum: d.Checksum,
			RequestedBy: actor.UserID, RequestedAt: s.deps.Clock.Now(), CorrelationID: telemetry.CorrelationID(ctx),
		}
		if kind == domain.KindRollback || resume {
			if err := s.linkPrevious(ctx, q, d, &e); err != nil {
				return err
			}
		}
		created, err = s.submit(ctx, q, e)
		return err
	})
	if err != nil {
		return domain.Execution{}, err
	}
	s.record(ctx, "migration.requested", actor, created)
	return created, nil
}

// checkRequest mengumpulkan bahan dari database lalu menyerahkan keputusannya ke checkOperation
func (s *ExecutionService) checkRequest(
	ctx context.Context, q appdb.DBTX, connID int64, version int, kind domain.ExecutionKind, resume bool,
) (Definition, error) {
	d, err := s.deps.Store.DefinitionIn(ctx, q, connID, version)
	if err != nil {
		return Definition{}, err
	}
	open, err := s.deps.Store.HasOpenExecution(ctx, q, d.ID)
	if err != nil {
		return Definition{}, err
	}
	if open {
		return Definition{}, refuse(domain.ErrConflict, "Versi %d masih punya permintaan yang belum selesai.", d.Version)
	}
	others, err := s.deps.Store.VersionStates(ctx, q, connID)
	if err != nil {
		return Definition{}, err
	}
	err = checkOperation(operation{
		Kind: kind, Resume: resume,
		Target: versionState{Version: d.Version, State: d.State}, Others: others,
		Irreversible: d.Irreversible, HasDown: d.DownSQL != "",
	})
	return d, err
}

// linkPrevious menghubungkan rollback atau resume dengan eksekusi yang dibatalkan atau dilanjutkan
func (s *ExecutionService) linkPrevious(ctx context.Context, q appdb.DBTX, d Definition, e *domain.Execution) error {
	prev, err := s.deps.Store.LatestForwardExecution(ctx, q, d.ID, domain.ExecSuccess, domain.ExecPartial)
	if err != nil {
		return err
	}
	// root menyatukan satu rangkaian batch supaya checkpoint-nya bisa dicari bersama
	root := prev.ID
	if prev.RootExecutionID != nil {
		root = *prev.RootExecutionID
	}
	e.RootExecutionID = &root
	if e.Kind == domain.KindRollback {
		e.RollsBackExecutionID, e.SQLSnapshot = &prev.ID, d.DownSQL
		return nil
	}
	e.ResumesExecutionID = &prev.ID
	return nil
}

// submit menyimpan permintaan dan daftar persetujuannya, kalau salah satu gagal dua-duanya batal
func (s *ExecutionService) submit(ctx context.Context, q appdb.DBTX, e domain.Execution) (domain.Execution, error) {
	saved, err := s.deps.Store.InsertExecution(ctx, q, e)
	if err != nil {
		return domain.Execution{}, err
	}
	if _, err := s.deps.Approvals.Create(ctx, q, saved.ID, saved.ConnectionID, saved.RequestedBy); err != nil {
		return domain.Execution{}, err
	}
	summary, err := s.deps.Approvals.Summary(ctx, q, saved.ID)
	if err != nil {
		return domain.Execution{}, err
	}
	// pengaju yang leader satu-satunya group tidak perlu menunggu siapa pun
	if summary.Outcome() == domain.ExecApproved {
		err := s.deps.Store.SetExecutionStatus(ctx, q, saved.ID, domain.ExecPendingReview, domain.ExecApproved, saved.RequestedAt)
		if err != nil {
			return domain.Execution{}, err
		}
	}
	return s.deps.Store.ExecutionIn(ctx, q, saved.ID)
}

// Cancel membatalkan permintaan yang belum masuk antrean
func (s *ExecutionService) Cancel(ctx context.Context, actor domain.Principal, executionID int64) error {
	e, err := s.editable(ctx, actor, executionID)
	if err != nil {
		return err
	}
	err = s.deps.Store.InConnectionTx(ctx, e.ConnectionID, func(q appdb.DBTX) error {
		// dibaca ulang di dalam kunci karena statusnya bisa baru saja berubah
		e, err = s.deps.Store.ExecutionIn(ctx, q, executionID)
		if err != nil {
			return err
		}
		if e.Status != domain.ExecPendingReview && e.Status != domain.ExecApproved {
			return refuse(domain.ErrConflict, "Permintaan berstatus %s tidak bisa dibatalkan.", e.Status)
		}
		return s.deps.Store.SetExecutionStatus(ctx, q, e.ID, e.Status, domain.ExecCancelled, s.deps.Clock.Now())
	})
	if err != nil {
		return err
	}
	e.Status = domain.ExecCancelled
	s.record(ctx, "migration.request_cancelled", actor, e)
	return nil
}

// Run memasukkan permintaan yang sudah disetujui ke antrean dan langsung mengembalikan job ID
func (s *ExecutionService) Run(ctx context.Context, actor domain.Principal, executionID int64) (string, error) {
	e, err := s.editable(ctx, actor, executionID)
	if err != nil {
		return "", err
	}
	var jobID string
	err = s.deps.Store.InConnectionTx(ctx, e.ConnectionID, func(q appdb.DBTX) error {
		e, err = s.deps.Store.ExecutionIn(ctx, q, executionID)
		if err != nil {
			return err
		}
		switch e.Status {
		case domain.ExecQueued, domain.ExecRunning:
			// tombol ditekan dua kali cukup mendapat tiket yang sama
			jobID, err = s.deps.Store.JobID(ctx, q, e.ID)
			return err
		case domain.ExecPendingReview:
			return refuse(domain.ErrApprovalRequired, "Permintaan ini belum disetujui semua leader.")
		case domain.ExecApproved:
			jobID, err = s.enqueue(ctx, q, e)
			return err
		}
		return refuse(domain.ErrConflict, "Permintaan berstatus %s tidak bisa dijalankan.", e.Status)
	})
	if err != nil {
		return "", err
	}
	s.record(ctx, "migration.queued", actor, e)
	return jobID, nil
}

// enqueue mengubah status dan membuat job dalam transaction yang sama, jadi tidak ada status queued tanpa job
func (s *ExecutionService) enqueue(ctx context.Context, q appdb.DBTX, e domain.Execution) (string, error) {
	err := s.deps.Store.SetExecutionStatus(ctx, q, e.ID, domain.ExecApproved, domain.ExecQueued, s.deps.Clock.Now())
	if err != nil {
		return "", err
	}
	// job memakai correlation_id milik pengajuan. biar pengajuan, run, kerja worker, dan riwayat attempt bisa dicari dengan satu ID
	job, err := s.deps.Queue.Enqueue(ctx, q, domain.EnqueueInput{
		ExecutionID: e.ID, ConnectionID: e.ConnectionID, MaxAttempts: s.deps.MaxAttempts,
		CorrelationID: e.CorrelationID,
	})
	if err != nil {
		return "", err
	}
	return job.ID, nil
}

// editable memeriksa akses ke database milik permintaan itu, supaya ID permintaan saja tidak cukup
func (s *ExecutionService) editable(ctx context.Context, actor domain.Principal, executionID int64) (domain.Execution, error) {
	e, err := s.deps.Store.GetExecution(ctx, executionID)
	if err != nil {
		return domain.Execution{}, err
	}
	if err := requireAccess(ctx, s.deps.Authz, actor, e.ConnectionID, domain.AccessViewEdit); err != nil {
		return domain.Execution{}, err
	}
	return e, nil
}

func (s *ExecutionService) record(ctx context.Context, event string, actor domain.Principal, e domain.Execution) {
	s.deps.Logger.InfoContext(ctx, "migration execution changed",
		"event", event, "actor", actor.Username, "connection_id", e.ConnectionID,
		"migration_id", e.MigrationID, "execution_id", e.ID, "version", e.Version, "kind", e.Kind, "status", e.Status)
}
