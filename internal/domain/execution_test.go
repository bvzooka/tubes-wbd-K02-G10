package domain

import "testing"

func TestApprovalSummary_Outcome(t *testing.T) {
	tests := []struct {
		name string
		sum  ApprovalSummary
		want ExecutionStatus
	}{
		{"semua setuju", ApprovalSummary{Approved: 2}, ExecApproved},
		{"masih ada pending", ApprovalSummary{Approved: 1, Pending: 1}, ExecPendingReview},
		{"satu menolak", ApprovalSummary{Approved: 1, Pending: 1, Rejected: 1}, ExecRejected},
		{"tanpa baris", ApprovalSummary{}, ExecPendingReview},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sum.Outcome(); got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}
