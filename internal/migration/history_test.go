package migration

import (
	"testing"

	"dbplatform/internal/domain"
)

// state kosong di kolom want artinya status migration tidak boleh berubah
func TestSettle_Outcomes(t *testing.T) {
	resumed := int64(7)
	apply := domain.Execution{Kind: domain.KindApply}
	rollback := domain.Execution{Kind: domain.KindRollback}
	resume := domain.Execution{Kind: domain.KindForward, ResumesExecutionID: &resumed}

	tests := []struct {
		name string
		e    domain.Execution
		a    Attempt
		want settlement
	}{
		{"apply sukses", apply, Attempt{Outcome: domain.OutcomeSuccess}, settlement{"success", domain.ExecSuccess, domain.StateApplied}},
		{"rollback sukses", rollback, Attempt{Outcome: domain.OutcomeSuccess}, settlement{"success", domain.ExecSuccess, domain.StateRolledBack}},
		{"apply gagal", apply, Attempt{Outcome: domain.OutcomeFailed}, settlement{"failed", domain.ExecFailed, domain.StateFailed}},
		{"gagal tapi dicoba lagi", apply, Attempt{Outcome: domain.OutcomeFailed, WillRetry: true}, settlement{"failed", domain.ExecQueued, ""}},
		{"rollback gagal", rollback, Attempt{Outcome: domain.OutcomeFailed}, settlement{"failed", domain.ExecFailed, ""}},
		{"resume gagal", resume, Attempt{Outcome: domain.OutcomeFailed}, settlement{"failed", domain.ExecFailed, ""}},
		{"batch partial", apply, Attempt{Outcome: domain.OutcomePartial}, settlement{"partial", domain.ExecPartial, domain.StatePartial}},
		{"pembatalan setengah", rollback, Attempt{Outcome: domain.OutcomePartiallyReverted}, settlement{"partial", domain.ExecPartiallyReverted, domain.StatePartiallyReverted}},
		{"hasil tidak pasti", apply, Attempt{Outcome: domain.OutcomeUnknown}, settlement{"outcome_unknown", domain.ExecOutcomeUnknown, domain.StateUnknown}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := settle(tt.e, tt.a); got != tt.want {
				t.Errorf("settle = %+v, mau %+v", got, tt.want)
			}
		})
	}
}
