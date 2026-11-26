package migration

import (
	"context"
	"time"

	"dbplatform/internal/domain"
)

const historyPageSize = 20

const (
	historySuccess = "success"
	historyFailed  = "failed"
	historyPartial = "partial"
	historyUnknown = "outcome_unknown"
)

type Attempt struct {
	Number         int
	Outcome        domain.RunOutcome
	WillRetry      bool
	Committed      bool
	SQLExecuted    string // kosong kalau gagal sebelum SQL sempat dikirim
	FailureCode    string
	FailureMessage string
	Batches        int
	Rows           int64
	TraceID        string
	StartedAt      time.Time
	FinishedAt     time.Time
}

// settlement adalah akibat sebuah attempt ke tiga tempat sekaligus
type settlement struct {
	history   string
	execution domain.ExecutionStatus
	state     domain.MigrationState // kosong berarti status migration tidak berubah
}

// settle fungsi murni yang menerjemahkan hasil attempt jadi status riwayat, permintaan, dan migration
func settle(e domain.Execution, a Attempt) settlement {
	switch a.Outcome {
	case domain.OutcomeSuccess:
		if e.Kind == domain.KindRollback {
			return settlement{historySuccess, domain.ExecSuccess, domain.StateRolledBack}
		}
		return settlement{historySuccess, domain.ExecSuccess, domain.StateApplied}
	case domain.OutcomePartial:
		return settlement{historyPartial, domain.ExecPartial, domain.StatePartial}
	case domain.OutcomePartiallyReverted:
		return settlement{historyPartial, domain.ExecPartiallyReverted, domain.StatePartiallyReverted}
	case domain.OutcomeUnknown:
		return settlement{historyUnknown, domain.ExecOutcomeUnknown, domain.StateUnknown}
	}
	// sisanya gagal tanpa perubahan yang tercommit
	if a.WillRetry {
		return settlement{history: historyFailed, execution: domain.ExecQueued}
	}
	// rollback atau resume yang gagal tidak menggeser posisi, perubahan lamanya masih ada di database target
	if e.Kind == domain.KindRollback || e.ResumesExecutionID != nil {
		return settlement{history: historyFailed, execution: domain.ExecFailed}
	}
	return settlement{historyFailed, domain.ExecFailed, domain.StateFailed}
}

// HistoryFilter dipakai halaman riwayat, nilai kosong berarti tidak disaring
type HistoryFilter struct {
	ExecutionID  int64
	Status       string
	HideRollback bool
	Page         int
}

// History mengembalikan satu halaman riwayat dan apakah masih ada halaman berikutnya
func (s *ExecutionService) History(
	ctx context.Context, actor domain.Principal, connID int64, f HistoryFilter,
) ([]domain.HistoryEntry, bool, error) {
	if err := requireAccess(ctx, s.deps.Authz, actor, connID, domain.AccessViewOnly); err != nil {
		return nil, false, err
	}
	page := max(f.Page, 1)
	// diambil satu baris lebih untuk tahu ada halaman berikutnya tanpa menghitung semuanya
	entries, err := s.deps.Store.ListHistory(ctx, connID, f, historyPageSize+1, (page-1)*historyPageSize)
	if err != nil {
		return nil, false, err
	}
	if len(entries) > historyPageSize {
		return entries[:historyPageSize], true, nil
	}
	return entries, false, nil
}
