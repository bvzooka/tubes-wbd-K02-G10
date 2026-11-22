// Package approval mengelola persetujuan leader group atas permintaan eksekusi migration.
package approval

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/appdb"
	"dbplatform/internal/clock"
	"dbplatform/internal/contract"
	"dbplatform/internal/domain"
)

var (
	errAlreadyDecided = &domain.MessageError{Err: domain.ErrConflict, Message: "Group ini sudah memberi keputusan."}
	errNotPending     = &domain.MessageError{Err: domain.ErrConflict, Message: "Permintaan ini sudah tidak menunggu persetujuan."}
	errNotLeader      = &domain.MessageError{Err: domain.ErrForbidden, Message: "Hanya leader group ini yang dapat memberi keputusan."}
)

type Service struct {
	pool   *pgxpool.Pool
	clock  clock.Clock
	logger *slog.Logger
}

func NewService(pool *pgxpool.Pool, clk clock.Clock, logger *slog.Logger) *Service {
	return &Service{pool: pool, clock: clk, logger: logger}
}

var _ contract.Approvals = (*Service)(nil)

// Create menyimpan snapshot group yang kini punya akses beserta leader-nya. Pemohon
// yang menjadi leader suatu group otomatis menyetujui untuk group itu.
func (s *Service) Create(ctx context.Context, tx appdb.DBTX, executionID, connectionID, requesterID int64) ([]domain.Approval, error) {
	groups, err := groupsWithAccess(ctx, tx, connectionID)
	if err != nil {
		return nil, err
	}
	if missing := groupsWithoutLeader(groups); len(missing) > 0 {
		return nil, &domain.ValidationError{
			Message: "Group berikut belum punya leader: " + strings.Join(missing, ", ") + ". Minta admin menetapkan leader dulu.",
			Cause:   domain.ErrNoLeader,
		}
	}

	now := s.clock.Now()
	approvals := make([]domain.Approval, 0, len(groups))
	for _, g := range groups {
		a := domain.Approval{
			ExecutionID: executionID,
			GroupID:     g.ID,
			GroupName:   g.Name,
			LeaderID:    *g.LeaderID,
			LeaderName:  g.LeaderName,
			Status:      domain.ApprovalPending,
		}
		if a.LeaderID == requesterID {
			a.Status, a.Auto, a.DecidedBy, a.DecidedAt = domain.ApprovalApproved, true, &requesterID, &now
		}
		if err := insertApproval(ctx, tx, a); err != nil {
			return nil, err
		}
		approvals = append(approvals, a)
	}
	return approvals, nil
}

func (s *Service) Decide(ctx context.Context, tx appdb.DBTX, actor domain.Principal, executionID, groupID int64, approve bool, comment string) error {
	comment = strings.TrimSpace(comment)
	if !approve && comment == "" {
		return &domain.ValidationError{Fields: map[string]string{"comment": "Alasan penolakan wajib diisi."}}
	}

	a, err := lockApproval(ctx, tx, executionID, groupID)
	if err != nil {
		return err
	}
	// Platform Admin tidak punya hak khusus di sini: hanya leader snapshot yang boleh.
	if a.LeaderID != actor.UserID {
		return errNotLeader
	}
	if a.Status != domain.ApprovalPending {
		return errAlreadyDecided
	}

	now := s.clock.Now()
	a.Status, a.DecidedBy, a.DecidedAt, a.Comment = domain.ApprovalRejected, &actor.UserID, &now, comment
	if approve {
		a.Status = domain.ApprovalApproved
	}
	return saveDecision(ctx, tx, a)
}

func (s *Service) Summary(ctx context.Context, q appdb.DBTX, executionID int64) (domain.ApprovalSummary, error) {
	return summary(ctx, q, executionID)
}

func (s *Service) RequiredNow(ctx context.Context, q appdb.DBTX, connectionID int64) ([]domain.GroupRef, error) {
	groups, err := groupsWithAccess(ctx, q, connectionID)
	if err != nil {
		return nil, err
	}
	refs := make([]domain.GroupRef, len(groups))
	for i, g := range groups {
		refs[i] = g.GroupRef
	}
	return refs, nil
}

func (s *Service) List(ctx context.Context, q appdb.DBTX, executionID int64) ([]domain.Approval, error) {
	return listApprovals(ctx, q, executionID)
}

func (s *Service) Inbox(ctx context.Context, userID int64) ([]domain.PendingApproval, error) {
	return inbox(ctx, s.pool, userID)
}

// Review adalah alur lengkap tombol Setuju/Tolak: catat keputusan, lalu ubah status
// eksekusi menjadi approved (semua setuju) atau rejected (ada yang menolak), semuanya
// dalam satu transaction.
func (s *Service) Review(ctx context.Context, actor domain.Principal, executionID, groupID int64, approve bool, comment string) (domain.ExecutionStatus, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin review: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() // no-op setelah commit

	status, err := lockExecution(ctx, tx, executionID)
	if err != nil {
		return "", err
	}
	if status != domain.ExecPendingReview {
		return "", errNotPending
	}
	if err := s.Decide(ctx, tx, actor, executionID, groupID, approve, comment); err != nil {
		return "", err
	}
	sum, err := summary(ctx, tx, executionID)
	if err != nil {
		return "", err
	}
	next := sum.Outcome()
	if next != status {
		if err := setExecutionStatus(ctx, tx, executionID, next, s.clock.Now()); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit review: %w", err)
	}

	s.logger.InfoContext(ctx, "approval decided",
		"event", "approval.decided", "actor", actor.Username, "execution_id", executionID,
		"group_id", groupID, "approve", approve, "status", next)
	return next, nil
}

func groupsWithoutLeader(groups []groupWithLeader) []string {
	var names []string
	for _, g := range groups {
		if g.LeaderID == nil {
			names = append(names, g.Name)
		}
	}
	return names
}
