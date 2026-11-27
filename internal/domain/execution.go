package domain

import "time"

type MigrationState string

const (
	StateDraft             MigrationState = "draft"
	StateReady             MigrationState = "ready"
	StateApplied           MigrationState = "applied"
	StateFailed            MigrationState = "failed"
	StateRolledBack        MigrationState = "rolled_back"
	StatePartial           MigrationState = "partial"
	StatePartiallyReverted MigrationState = "partially_reverted"
	StateUnknown           MigrationState = "unknown"
	StateCancelled         MigrationState = "cancelled"
	StateSuperseded        MigrationState = "superseded"
)

// BlocksHigher: state yang menahan migration berversi lebih tinggi.
func (s MigrationState) BlocksHigher() bool {
	switch s {
	case StateDraft, StateReady, StateRolledBack, StatePartial, StatePartiallyReverted, StateUnknown:
		return true
	}
	return false
}

// BlocksDatabase: state yang menahan seluruh database sampai dipulihkan.
func (s MigrationState) BlocksDatabase() bool {
	return s == StatePartial || s == StatePartiallyReverted || s == StateUnknown
}

type MigrationMode string

const (
	ModeNormal MigrationMode = "normal"
	ModeBatch  MigrationMode = "batch"
)

type BatchConfig struct {
	Schema    string `json:"schema"`
	Table     string `json:"table"`
	KeyColumn string `json:"key_column"`
	BatchSize int    `json:"batch_size"`
}

type ExecutionKind string

const (
	KindApply    ExecutionKind = "apply"
	KindRollback ExecutionKind = "rollback"
	KindForward  ExecutionKind = "forward"
)

type ExecutionStatus string

const (
	ExecPendingReview     ExecutionStatus = "pending_review"
	ExecRejected          ExecutionStatus = "rejected"
	ExecApproved          ExecutionStatus = "approved"
	ExecCancelled         ExecutionStatus = "cancelled"
	ExecQueued            ExecutionStatus = "queued"
	ExecRunning           ExecutionStatus = "running"
	ExecSuccess           ExecutionStatus = "success"
	ExecFailed            ExecutionStatus = "failed"
	ExecPartial           ExecutionStatus = "partial"
	ExecPartiallyReverted ExecutionStatus = "partially_reverted"
	ExecOutcomeUnknown    ExecutionStatus = "outcome_unknown"
)

type Execution struct {
	ID                              int64
	MigrationID                     int64
	ConnectionID                    int64
	Version                         int
	Revision                        int
	Kind                            ExecutionKind
	Mode                            MigrationMode
	Batch                           *BatchConfig
	SQLSnapshot                     string
	Checksum                        string
	RollsBackExecutionID            *int64
	ResumesExecutionID              *int64
	RootExecutionID                 *int64 // nil berarti dirinya sendiri
	Status                          ExecutionStatus
	RequestedBy                     int64
	RequestedByName                 string
	RequestedAt                     time.Time
	QueuedAt, StartedAt, FinishedAt *time.Time
	FailureCode                     string
	FailureMessage                  string
	ScopeMax, CursorValue           *int64
	BatchesCommitted                int
	RowsCommitted                   int64
	CorrelationID                   string
	Legacy                          bool
}

type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "pending"
	ApprovalApproved ApprovalStatus = "approved"
	ApprovalRejected ApprovalStatus = "rejected"
)

type Approval struct {
	ExecutionID int64
	GroupID     int64
	GroupName   string
	LeaderID    int64
	LeaderName  string
	Status      ApprovalStatus
	DecidedBy   *int64
	DecidedAt   *time.Time
	Comment     string
	Auto        bool
}

type ApprovalSummary struct{ Pending, Approved, Rejected int }

// Outcome: satu penolakan cukup untuk menolak, semua group harus setuju agar disetujui.
func (s ApprovalSummary) Outcome() ExecutionStatus {
	switch {
	case s.Rejected > 0:
		return ExecRejected
	case s.Pending == 0 && s.Approved > 0:
		return ExecApproved
	default:
		return ExecPendingReview
	}
}

type GroupRef struct {
	ID   int64
	Name string
}

type PendingApproval struct {
	Execution Execution
	Approval  Approval
	Database  string
}

type Job struct {
	ID            string // UUID
	ExecutionID   int64
	ConnectionID  int64
	State         string // queued | running | done
	Attempt       int
	MaxAttempts   int
	RunAfter      time.Time
	Fence         int64
	Traceparent   string
	CorrelationID string
	LastError     string
}

const (
	JobQueued  = "queued"
	JobRunning = "running"
	JobDone    = "done"
)

type EnqueueInput struct {
	ExecutionID   int64
	ConnectionID  int64
	Traceparent   string
	CorrelationID string
	MaxAttempts   int
}

type RunOutcome string

const (
	OutcomeSuccess           RunOutcome = "success"
	OutcomeFailed            RunOutcome = "failed"
	OutcomePartial           RunOutcome = "partial"
	OutcomePartiallyReverted RunOutcome = "partially_reverted"
	OutcomeUnknown           RunOutcome = "unknown"
	OutcomeRequeue           RunOutcome = "requeue" // belum dijalankan (mis. target sibuk)
)

// RunResult: runner tidak menjadwalkan retry sendiri, worker yang memutuskan.
type RunResult struct {
	Outcome    RunOutcome
	Retryable  bool          // hanya benar jika bukti menyatakan belum ter-commit
	RetryAfter time.Duration // 0 berarti pakai backoff worker
	Err        error
}

type HistoryEntry struct {
	ID, ExecutionID, MigrationID, ConnectionID int64
	Attempt, Version, Revision                 int
	Kind                                       ExecutionKind
	ExecutedBy                                 int64
	ExecutedByName                             string
	Status                                     string // success | failed | partial | outcome_unknown
	Committed                                  bool
	SQLExecuted                                string
	RollsBackExecutionID                       *int64
	BatchesCommitted                           int
	RowsCommitted                              int64
	FailureCode, FailureMessage                string
	CorrelationID, TraceID                     string
	StartedAt, FinishedAt                      time.Time

	// true kalau percobaan ini melanjutkan batch yang setengah jalan
	Resume bool
}
