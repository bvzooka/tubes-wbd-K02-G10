package approval

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"dbplatform/internal/appdb"
	"dbplatform/internal/domain"
)

type groupWithLeader struct {
	domain.GroupRef
	LeaderID   *int64
	LeaderName string
}

// groupsWithAccess mengembalikan group yang SEKARANG punya akses (view maupun edit).
func groupsWithAccess(ctx context.Context, q appdb.DBTX, connectionID int64) ([]groupWithLeader, error) {
	rows, err := q.Query(ctx, `
		SELECT g.id, g.name, g.leader_id, COALESCE(u.username, '')
		FROM user_groups g
		JOIN group_database_access a ON a.group_id = g.id AND a.connection_id = $1
		LEFT JOIN users u ON u.id = g.leader_id
		ORDER BY g.id`, connectionID)
	if err != nil {
		return nil, fmt.Errorf("groups with access: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (groupWithLeader, error) {
		var g groupWithLeader
		err := row.Scan(&g.ID, &g.Name, &g.LeaderID, &g.LeaderName)
		return g, err
	})
}

func insertApproval(ctx context.Context, q appdb.DBTX, a domain.Approval) error {
	_, err := q.Exec(ctx, `
		INSERT INTO execution_approvals
			(execution_id, group_id, group_name, leader_id, status, decided_by, decided_at, auto)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		a.ExecutionID, a.GroupID, a.GroupName, a.LeaderID, a.Status, a.DecidedBy, a.DecidedAt, a.Auto)
	if err != nil {
		return fmt.Errorf("insert approval: %w", err)
	}
	return nil
}

func lockApproval(ctx context.Context, q appdb.DBTX, executionID, groupID int64) (domain.Approval, error) {
	a := domain.Approval{ExecutionID: executionID, GroupID: groupID}
	err := q.QueryRow(ctx, `
		SELECT leader_id, status FROM execution_approvals
		WHERE execution_id = $1 AND group_id = $2
		FOR UPDATE`, executionID, groupID).Scan(&a.LeaderID, &a.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, domain.ErrNotFound
	}
	if err != nil {
		return a, fmt.Errorf("lock approval: %w", err)
	}
	return a, nil
}

func saveDecision(ctx context.Context, q appdb.DBTX, a domain.Approval) error {
	_, err := q.Exec(ctx, `
		UPDATE execution_approvals
		SET status = $3, decided_by = $4, decided_at = $5, comment = $6
		WHERE execution_id = $1 AND group_id = $2`,
		a.ExecutionID, a.GroupID, a.Status, a.DecidedBy, a.DecidedAt, a.Comment)
	if err != nil {
		return fmt.Errorf("save decision: %w", err)
	}
	return nil
}

func summary(ctx context.Context, q appdb.DBTX, executionID int64) (domain.ApprovalSummary, error) {
	var s domain.ApprovalSummary
	err := q.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'pending'),
		       count(*) FILTER (WHERE status = 'approved'),
		       count(*) FILTER (WHERE status = 'rejected')
		FROM execution_approvals WHERE execution_id = $1`, executionID).
		Scan(&s.Pending, &s.Approved, &s.Rejected)
	if err != nil {
		return s, fmt.Errorf("approval summary: %w", err)
	}
	return s, nil
}

func listApprovals(ctx context.Context, q appdb.DBTX, executionID int64) ([]domain.Approval, error) {
	rows, err := q.Query(ctx, `
		SELECT a.execution_id, a.group_id, a.group_name, a.leader_id, u.username,
		       a.status, a.decided_by, a.decided_at, a.comment, a.auto
		FROM execution_approvals a
		JOIN users u ON u.id = a.leader_id
		WHERE a.execution_id = $1
		ORDER BY a.group_id`, executionID)
	if err != nil {
		return nil, fmt.Errorf("list approvals: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Approval, error) {
		var a domain.Approval
		err := row.Scan(&a.ExecutionID, &a.GroupID, &a.GroupName, &a.LeaderID, &a.LeaderName,
			&a.Status, &a.DecidedBy, &a.DecidedAt, &a.Comment, &a.Auto)
		return a, err
	})
}

func inbox(ctx context.Context, q appdb.DBTX, leaderID int64) ([]domain.PendingApproval, error) {
	rows, err := q.Query(ctx, `
		SELECT e.id, e.migration_id, e.connection_id, e.version, e.revision, e.kind, e.mode,
		       e.sql_snapshot, e.status, e.requested_by, req.username, e.requested_at,
		       a.group_id, a.group_name, c.name
		FROM execution_approvals a
		JOIN migration_executions e ON e.id = a.execution_id
		JOIN connections c ON c.id = e.connection_id
		JOIN users req ON req.id = e.requested_by
		WHERE a.leader_id = $1 AND a.status = 'pending' AND e.status = 'pending_review'
		ORDER BY e.requested_at, e.id`, leaderID)
	if err != nil {
		return nil, fmt.Errorf("approval inbox: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.PendingApproval, error) {
		var p domain.PendingApproval
		e := &p.Execution
		err := row.Scan(&e.ID, &e.MigrationID, &e.ConnectionID, &e.Version, &e.Revision, &e.Kind, &e.Mode,
			&e.SQLSnapshot, &e.Status, &e.RequestedBy, &e.RequestedByName, &e.RequestedAt,
			&p.Approval.GroupID, &p.Approval.GroupName, &p.Database)
		p.Approval.ExecutionID, p.Approval.LeaderID, p.Approval.Status = e.ID, leaderID, domain.ApprovalPending
		return p, err
	})
}

// lockExecution mengunci baris eksekusi agar dua leader yang memutuskan bersamaan
// tidak sama-sama melihat dirinya sebagai keputusan terakhir.
func lockExecution(ctx context.Context, q appdb.DBTX, executionID int64) (domain.ExecutionStatus, error) {
	var status domain.ExecutionStatus
	err := q.QueryRow(ctx, `SELECT status FROM migration_executions WHERE id = $1 FOR UPDATE`, executionID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("lock execution: %w", err)
	}
	return status, nil
}

func setExecutionStatus(ctx context.Context, q appdb.DBTX, executionID int64, status domain.ExecutionStatus, now time.Time) error {
	_, err := q.Exec(ctx, `
		UPDATE migration_executions
		SET status = $2, approved_at = CASE WHEN $2 = 'approved' THEN $3::timestamptz ELSE approved_at END
		WHERE id = $1`, executionID, status, now)
	if err != nil {
		return fmt.Errorf("update execution status: %w", err)
	}
	return nil
}
