package migration

import (
	"fmt"

	"dbplatform/internal/domain"
)

type versionState struct {
	Version int
	State   domain.MigrationState
}

type operation struct {
	Kind         domain.ExecutionKind
	Resume       bool
	Target       versionState
	Others       []versionState
	Irreversible bool
	HasDown      bool
}

func checkOperation(op operation) error {
	// database yang hasilnya belum beres dikunci dulu sebelum aturan lain dicek
	if version, blocked := blockedBy(op); blocked {
		return refuse(domain.ErrDatabaseBlocked, "Database terblok karena versi %d belum dipulihkan.", version)
	}
	if op.Resume {
		return checkResume(op)
	}
	switch op.Kind {
	case domain.KindApply:
		return checkApply(op)
	case domain.KindForward:
		return checkForward(op)
	case domain.KindRollback:
		return checkRollback(op)
	}
	return refuse(domain.ErrValidation, "Jenis permintaan %q tidak dikenal.", op.Kind)
}

// blockedBy mencari migration yang membuat seluruh database terblok
func blockedBy(op operation) (int, bool) {
	// unknown tidak boleh diapa-apakan karena belum tahu perubahannya masuk atau tidak
	if op.Target.State == domain.StateUnknown {
		return op.Target.Version, true
	}
	for _, other := range op.Others {
		// target sendiri dilewati supaya migration partial masih bisa di-resume atau dirollback
		if other.Version != op.Target.Version && other.State.BlocksDatabase() {
			return other.Version, true
		}
	}
	return 0, false
}

// checkApply untuk menjalankan migration yang belum pernah masuk ke database
func checkApply(op operation) error {
	if op.Target.State != domain.StateReady {
		return refuse(domain.ErrConflict, "Versi %d tidak bisa di-apply karena statusnya %s.", op.Target.Version, op.Target.State)
	}
	return checkOrder(op)
}

// checkForward untuk mencoba lagi migration yang gagal atau sudah dirollback
func checkForward(op operation) error {
	if op.Target.State != domain.StateFailed && op.Target.State != domain.StateRolledBack {
		return refuse(domain.ErrConflict, "Forward hanya untuk migration yang gagal atau sudah di-rollback, versi %d statusnya %s.", op.Target.Version, op.Target.State)
	}
	return checkOrder(op)
}

// checkOrder menjaga migration masuk berurutan dari versi kecil ke besar
func checkOrder(op operation) error {
	for _, other := range op.Others {
		if other.Version > op.Target.Version && other.State == domain.StateApplied {
			return refuse(domain.ErrMigrationOutOfOrder, "Versi %d yang lebih tinggi sudah aktif.", other.Version)
		}
		if other.Version < op.Target.Version && other.State.BlocksHigher() {
			return refuse(domain.ErrMigrationOutOfOrder, "Versi %d harus diselesaikan lebih dulu.", other.Version)
		}
	}
	return nil
}

// checkResume hanya untuk batch yang sebagian sudah tercommit
func checkResume(op operation) error {
	if op.Kind != domain.KindForward || op.Target.State != domain.StatePartial {
		return refuse(domain.ErrConflict, "Resume hanya untuk forward pada migration yang statusnya partial.")
	}
	return nil
}

// checkRollback membatalkan migration, harus dari versi aktif paling tinggi dulu
func checkRollback(op operation) error {
	switch op.Target.State {
	case domain.StateApplied:
		for _, other := range op.Others {
			if other.Version > op.Target.Version && other.State == domain.StateApplied {
				return refuse(domain.ErrMigrationOutOfOrder, "Rollback harus berurutan, versi %d masih aktif.", other.Version)
			}
		}
	// batch yang setengah jalan boleh dibatalkan atau dilanjutkan pembatalannya
	case domain.StatePartial, domain.StatePartiallyReverted:
	default:
		return refuse(domain.ErrConflict, "Versi %d tidak bisa di-rollback karena statusnya %s.", op.Target.Version, op.Target.State)
	}
	if op.Irreversible || !op.HasDown {
		return refuse(domain.ErrNotReversible, "Versi %d tidak punya cara rollback.", op.Target.Version)
	}
	return nil
}

// checkSupersede membuang migration yang tidak mau dicoba lagi supaya tidak menahan versi di atasnya
func checkSupersede(target versionState) error {
	if target.State != domain.StateFailed && target.State != domain.StateRolledBack {
		return refuse(domain.ErrConflict, "Supersede hanya untuk migration yang gagal atau sudah di-rollback, versi %d statusnya %s.", target.Version, target.State)
	}
	return nil
}

// refuse menggabungkan error domain untuk status HTTP dengan pesan yang dibaca user
func refuse(cause error, format string, args ...any) error {
	return &domain.MessageError{Err: cause, Message: fmt.Sprintf(format, args...)}
}
