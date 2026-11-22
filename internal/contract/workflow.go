package contract

import (
	"context"

	"dbplatform/internal/appdb"
	"dbplatform/internal/domain"
)

type Approvals interface {
	// Create dipanggil di dalam tx yang membuat execution.
	Create(ctx context.Context, tx appdb.DBTX, executionID, connectionID, requesterID int64) ([]domain.Approval, error)
	Decide(ctx context.Context, tx appdb.DBTX, actor domain.Principal, executionID, groupID int64, approve bool, comment string) error
	Summary(ctx context.Context, q appdb.DBTX, executionID int64) (domain.ApprovalSummary, error)
	RequiredNow(ctx context.Context, q appdb.DBTX, connectionID int64) ([]domain.GroupRef, error)
	List(ctx context.Context, q appdb.DBTX, executionID int64) ([]domain.Approval, error)
	Inbox(ctx context.Context, userID int64) ([]domain.PendingApproval, error)
}

type JobQueue interface {
	// Enqueue dipanggil di dalam tx yang mengubah status execution ke queued.
	Enqueue(ctx context.Context, tx appdb.DBTX, in domain.EnqueueInput) (domain.Job, error)
	Get(ctx context.Context, jobID string) (domain.Job, error)
}

type Lease interface {
	// Context dibatalkan kalau lease hilang atau worker berhenti.
	Context() context.Context
	Fence() int64
	Lost() bool
}

type ExecutionRunner interface {
	Run(ctx context.Context, job domain.Job, lease Lease) domain.RunResult
	Reconcile(ctx context.Context, job domain.Job) domain.RunResult
}
