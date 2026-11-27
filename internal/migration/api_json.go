package migration

import (
	"time"

	"dbplatform/internal/domain"
)

// bentuk JSON dipisah dari tipe domain supaya perubahan di dalam tidak langsung mengubah API

type definitionJSON struct {
	ID                 int64                  `json:"id"`
	ConnectionID       int64                  `json:"connection_id"`
	Version            int                    `json:"version"`
	Revision           int                    `json:"revision"`
	Name               string                 `json:"name"`
	SQLUp              string                 `json:"sql_up"`
	SQLDown            string                 `json:"sql_down"`
	Irreversible       bool                   `json:"irreversible"`
	IrreversibleReason string                 `json:"irreversible_reason"`
	Mode               domain.MigrationMode   `json:"mode"`
	Batch              *domain.BatchConfig    `json:"batch"`
	State              domain.MigrationState  `json:"state"`
	OpenStatus         domain.ExecutionStatus `json:"open_status"`
	Checksum           string                 `json:"checksum"`
	CreatedBy          string                 `json:"created_by"`
	CreatedAt          time.Time              `json:"created_at"`
	UpdatedAt          time.Time              `json:"updated_at"`
}

type executionJSON struct {
	ID                   int64                  `json:"id"`
	ConnectionID         int64                  `json:"connection_id"`
	Version              int                    `json:"version"`
	Revision             int                    `json:"revision"`
	Kind                 domain.ExecutionKind   `json:"kind"`
	Mode                 domain.MigrationMode   `json:"mode"`
	Batch                *domain.BatchConfig    `json:"batch"`
	SQL                  string                 `json:"sql"`
	Status               domain.ExecutionStatus `json:"status"`
	RequestedBy          string                 `json:"requested_by"`
	RequestedAt          time.Time              `json:"requested_at"`
	QueuedAt             *time.Time             `json:"queued_at"`
	StartedAt            *time.Time             `json:"started_at"`
	FinishedAt           *time.Time             `json:"finished_at"`
	FailureCode          string                 `json:"failure_code"`
	FailureMessage       string                 `json:"failure_message"`
	BatchesCommitted     int                    `json:"batches_committed"`
	RowsCommitted        int64                  `json:"rows_committed"`
	RollsBackExecutionID *int64                 `json:"rolls_back_execution_id"`
	ResumesExecutionID   *int64                 `json:"resumes_execution_id"`
}

type approvalJSON struct {
	GroupID   int64                 `json:"group_id"`
	GroupName string                `json:"group_name"`
	LeaderID  int64                 `json:"leader_id"`
	Leader    string                `json:"leader_name"`
	Status    domain.ApprovalStatus `json:"status"`
	Auto      bool                  `json:"auto"`
	Comment   string                `json:"comment"`
	DecidedAt *time.Time            `json:"decided_at"`
}

type historyJSON struct {
	ID                   int64                `json:"id"`
	ExecutionID          int64                `json:"execution_id"`
	Attempt              int                  `json:"attempt"`
	MigrationID          int64                `json:"migration_id"`
	Version              int                  `json:"version"`
	Revision             int                  `json:"revision"`
	Kind                 domain.ExecutionKind `json:"kind"`
	ExecutedBy           string               `json:"executed_by"`
	Status               string               `json:"status"`
	Committed            bool                 `json:"committed"`
	SQL                  string               `json:"sql"`
	Resume               bool                 `json:"resume"`
	RollsBackExecutionID *int64               `json:"rolls_back_execution_id"`
	BatchesCommitted     int                  `json:"batches_committed"`
	RowsCommitted        int64                `json:"rows_committed"`
	FailureCode          string               `json:"failure_code"`
	FailureMessage       string               `json:"failure_message"`
	CorrelationID        string               `json:"correlation_id"`
	StartedAt            time.Time            `json:"started_at"`
	FinishedAt           time.Time            `json:"finished_at"`
}

func toDefinitionJSON(d Definition) definitionJSON {
	return definitionJSON{
		ID: d.ID, ConnectionID: d.ConnectionID, Version: d.Version, Revision: d.Revision, Name: d.Name,
		SQLUp: d.UpSQL, SQLDown: d.DownSQL, Irreversible: d.Irreversible, IrreversibleReason: d.IrreversibleReason,
		Mode: d.Mode, Batch: d.Batch, State: d.State, OpenStatus: d.OpenStatus, Checksum: d.Checksum,
		CreatedBy: d.CreatedByName,
		CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
}

func toExecutionJSON(e domain.Execution) executionJSON {
	return executionJSON{
		ID: e.ID, ConnectionID: e.ConnectionID, Version: e.Version, Revision: e.Revision, Kind: e.Kind,
		Mode: e.Mode, Batch: e.Batch, SQL: e.SQLSnapshot, Status: e.Status, RequestedBy: e.RequestedByName,
		RequestedAt: e.RequestedAt, QueuedAt: e.QueuedAt, StartedAt: e.StartedAt, FinishedAt: e.FinishedAt,
		FailureCode: e.FailureCode, FailureMessage: e.FailureMessage, BatchesCommitted: e.BatchesCommitted,
		RowsCommitted: e.RowsCommitted, RollsBackExecutionID: e.RollsBackExecutionID,
		ResumesExecutionID: e.ResumesExecutionID,
	}
}

func toApprovalJSON(a domain.Approval) approvalJSON {
	return approvalJSON{
		GroupID: a.GroupID, GroupName: a.GroupName, LeaderID: a.LeaderID, Leader: a.LeaderName,
		Status: a.Status, Auto: a.Auto, Comment: a.Comment, DecidedAt: a.DecidedAt,
	}
}

func toHistoryJSON(h domain.HistoryEntry) historyJSON {
	return historyJSON{
		ID: h.ID, ExecutionID: h.ExecutionID, Attempt: h.Attempt, MigrationID: h.MigrationID,
		Version: h.Version, Revision: h.Revision, Kind: h.Kind, ExecutedBy: h.ExecutedByName, Status: h.Status,
		Committed: h.Committed, SQL: h.SQLExecuted, Resume: h.Resume, RollsBackExecutionID: h.RollsBackExecutionID,
		BatchesCommitted: h.BatchesCommitted, RowsCommitted: h.RowsCommitted, FailureCode: h.FailureCode,
		FailureMessage: h.FailureMessage, CorrelationID: h.CorrelationID,
		StartedAt: h.StartedAt, FinishedAt: h.FinishedAt,
	}
}

// mapSlice selalu mengembalikan slice, jadi daftar kosong menjadi [] bukan null di JSON
func mapSlice[T, U any](in []T, convert func(T) U) []U {
	out := make([]U, 0, len(in))
	for _, v := range in {
		out = append(out, convert(v))
	}
	return out
}
