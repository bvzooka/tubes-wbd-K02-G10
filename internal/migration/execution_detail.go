package migration

import (
	"context"
	"errors"

	"dbplatform/internal/domain"
)

const maxDetailAttempts = 100

type ExecutionDetail struct {
	Execution domain.Execution
	Approvals []domain.Approval
	Attempts  []domain.HistoryEntry
	JobID     string
	CanEdit   bool
}

type JobStatus struct {
	Job       domain.Job
	Execution domain.Execution
}

// CanEdit dipakai untuk menampilkan atau menyembunyikan tombol, pengamanan sebenarnya tetap di tiap aksi
func (s *ExecutionService) CanEdit(ctx context.Context, actor domain.Principal, connID int64) (bool, error) {
	return s.deps.Authz.Can(ctx, actor.UserID, connID, domain.AccessViewEdit)
}

func (s *ExecutionService) Detail(ctx context.Context, actor domain.Principal, executionID int64) (ExecutionDetail, error) {
	e, err := s.viewable(ctx, actor, executionID)
	if err != nil {
		return ExecutionDetail{}, err
	}
	d := ExecutionDetail{Execution: e}
	if d.Approvals, err = s.deps.Approvals.List(ctx, s.deps.DB, e.ID); err != nil {
		return ExecutionDetail{}, err
	}
	filter := HistoryFilter{ExecutionID: e.ID}
	if d.Attempts, err = s.deps.Store.ListHistory(ctx, e.ConnectionID, filter, maxDetailAttempts, 0); err != nil {
		return ExecutionDetail{}, err
	}
	d.JobID, err = s.deps.Store.JobID(ctx, s.deps.DB, e.ID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return ExecutionDetail{}, err
	}
	d.CanEdit, err = s.CanEdit(ctx, actor, e.ConnectionID)
	return d, err
}

// ForVersion mengembalikan semua permintaan milik satu migration, yang terbaru di atas
func (s *ExecutionService) ForVersion(
	ctx context.Context, actor domain.Principal, connID int64, version int,
) ([]domain.Execution, error) {
	if err := requireAccess(ctx, s.deps.Authz, actor, connID, domain.AccessViewOnly); err != nil {
		return nil, err
	}
	return s.deps.Store.ListExecutions(ctx, connID, version)
}

// Job dipakai halaman monitor yang bertanya berulang sampai job selesai
func (s *ExecutionService) Job(ctx context.Context, actor domain.Principal, jobID string) (JobStatus, error) {
	job, err := s.deps.Queue.Get(ctx, jobID)
	if err != nil {
		return JobStatus{}, err
	}
	e, err := s.viewable(ctx, actor, job.ExecutionID)
	if err != nil {
		return JobStatus{}, err
	}
	return JobStatus{Job: job, Execution: e}, nil
}

// viewable sama dengan editable tapi cukup akses View Only
func (s *ExecutionService) viewable(ctx context.Context, actor domain.Principal, executionID int64) (domain.Execution, error) {
	e, err := s.deps.Store.GetExecution(ctx, executionID)
	if err != nil {
		return domain.Execution{}, err
	}
	if err := requireAccess(ctx, s.deps.Authz, actor, e.ConnectionID, domain.AccessViewOnly); err != nil {
		return domain.Execution{}, err
	}
	return e, nil
}
