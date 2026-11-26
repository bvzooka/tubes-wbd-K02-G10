package migration

import (
	"errors"
	"testing"

	"dbplatform/internal/domain"
)

// vs mempersingkat penulisan daftar versi di tabel kasus
func vs(version int, state domain.MigrationState) versionState {
	return versionState{Version: version, State: state}
}

// nil di kolom want artinya permintaan harus diterima
func TestCheckOperation_Rules(t *testing.T) {
	apply := domain.KindApply
	forward := domain.KindForward
	rollback := domain.KindRollback

	tests := []struct {
		name string
		op   operation
		want error
	}{
		{"apply versi pertama", operation{Kind: apply, Target: vs(1, domain.StateReady)}, nil},
		{"apply setelah versi bawah aktif", operation{Kind: apply, Target: vs(2, domain.StateReady), Others: []versionState{vs(1, domain.StateApplied)}}, nil},
		{"apply draft ditolak", operation{Kind: apply, Target: vs(1, domain.StateDraft)}, domain.ErrConflict},
		{"apply tertahan draft di bawahnya", operation{Kind: apply, Target: vs(2, domain.StateReady), Others: []versionState{vs(1, domain.StateDraft)}}, domain.ErrMigrationOutOfOrder},
		{"apply tertahan rolled back di bawahnya", operation{Kind: apply, Target: vs(2, domain.StateReady), Others: []versionState{vs(1, domain.StateRolledBack)}}, domain.ErrMigrationOutOfOrder},
		{"apply tidak tertahan failed di bawahnya", operation{Kind: apply, Target: vs(2, domain.StateReady), Others: []versionState{vs(1, domain.StateFailed)}}, nil},
		{"apply kalah dari versi atas yang aktif", operation{Kind: apply, Target: vs(1, domain.StateReady), Others: []versionState{vs(2, domain.StateApplied)}}, domain.ErrMigrationOutOfOrder},
		{"apply saat database terblok", operation{Kind: apply, Target: vs(2, domain.StateReady), Others: []versionState{vs(1, domain.StatePartial)}}, domain.ErrDatabaseBlocked},

		{"forward migration gagal", operation{Kind: forward, Target: vs(1, domain.StateFailed)}, nil},
		{"forward setelah rollback", operation{Kind: forward, Target: vs(1, domain.StateRolledBack)}, nil},
		{"forward migration aktif ditolak", operation{Kind: forward, Target: vs(1, domain.StateApplied)}, domain.ErrConflict},
		{"resume partial", operation{Kind: forward, Resume: true, Target: vs(1, domain.StatePartial)}, nil},
		{"resume selain partial ditolak", operation{Kind: forward, Resume: true, Target: vs(1, domain.StateFailed)}, domain.ErrConflict},

		{"rollback versi teratas", operation{Kind: rollback, HasDown: true, Target: vs(2, domain.StateApplied), Others: []versionState{vs(1, domain.StateApplied)}}, nil},
		{"rollback bukan versi teratas", operation{Kind: rollback, HasDown: true, Target: vs(1, domain.StateApplied), Others: []versionState{vs(2, domain.StateApplied)}}, domain.ErrMigrationOutOfOrder},
		{"rollback irreversible", operation{Kind: rollback, HasDown: true, Irreversible: true, Target: vs(1, domain.StateApplied)}, domain.ErrNotReversible},
		{"rollback tanpa sql down", operation{Kind: rollback, Target: vs(1, domain.StateApplied)}, domain.ErrNotReversible},
		{"rollback partial", operation{Kind: rollback, HasDown: true, Target: vs(1, domain.StatePartial)}, nil},
		{"rollback yang belum jalan", operation{Kind: rollback, HasDown: true, Target: vs(1, domain.StateReady)}, domain.ErrConflict},

		{"unknown memblok dirinya sendiri", operation{Kind: rollback, HasDown: true, Target: vs(1, domain.StateUnknown)}, domain.ErrDatabaseBlocked},
		{"jenis tidak dikenal", operation{Kind: "hapus", Target: vs(1, domain.StateReady)}, domain.ErrValidation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkOperation(tt.op)
			if !errors.Is(err, tt.want) {
				t.Errorf("error = %v, mau %v", err, tt.want)
			}
		})
	}
}

func TestCheckSupersede_Rules(t *testing.T) {
	for state, ok := range map[domain.MigrationState]bool{
		domain.StateFailed:     true,
		domain.StateRolledBack: true,
		domain.StateApplied:    false,
		domain.StateDraft:      false,
	} {
		if err := checkSupersede(vs(1, state)); (err == nil) != ok {
			t.Errorf("%s: error = %v", state, err)
		}
	}
}
