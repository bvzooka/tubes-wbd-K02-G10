package admin

import "dbplatform/internal/domain"

// Error aturan admin berupa ValidationError agar API otomatis menjawab 422 beserta pesannya.
var (
	ErrSelfDemote = &domain.ValidationError{
		Message: msgSelfDemote,
		Fields:  map[string]string{"is_platform_admin": msgSelfDemote},
	}
	ErrSelfDeactivate = &domain.ValidationError{Message: msgSelfDeactivate}
	ErrLastAdmin      = &domain.ValidationError{
		Message: msgLastAdmin,
		Fields:  map[string]string{"is_platform_admin": msgLastAdmin},
	}
	ErrGroupNotEmpty    = &domain.ValidationError{Message: msgGroupNotEmpty}
	ErrLeaderNotMember  = &domain.ValidationError{Message: "Leader harus anggota group."}
	ErrRemoveLeader     = &domain.ValidationError{Message: "Leader tidak dapat dikeluarkan dari group. Ganti leader dulu."}
	ErrDeactivateLeader = &domain.ValidationError{Message: "User ini masih menjadi leader group. Ganti leader dulu."}
)
