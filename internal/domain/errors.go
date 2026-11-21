package domain

import "errors"

// Nanti ditranslate HandleError jadi page 403/404/502
var (
	ErrUnauthenticated     = errors.New("unauthenticated")
	ErrForbidden           = errors.New("forbidden")
	ErrNotFound            = errors.New("not found")
	ErrConflict            = errors.New("conflict")
	ErrValidation          = errors.New("validation failed")
	ErrUnsupported         = errors.New("unsupported operation")
	ErrConnectionFailed    = errors.New("database connection failed")
	ErrConnectionInactive  = errors.New("connection inactive")
	ErrMigrationOutOfOrder = errors.New("migration out of order")
	ErrMigrationNotDraft   = errors.New("migration is not a draft")

	ErrApprovalRequired = errors.New("approval required")
	ErrNotReversible    = errors.New("migration not reversible")
	ErrDatabaseBusy     = errors.New("database busy")
	ErrDatabaseBlocked  = errors.New("database blocked")
	ErrNoLeader         = errors.New("group has no leader")
	ErrRateLimited      = errors.New("too many requests")
)

// ValidationError membawa pesan per kolom. Cause membuat errors.Is tetap kenal
// penyebab spesifik, misalnya ErrNoLeader.
type ValidationError struct {
	Fields  map[string]string
	Message string
	Cause   error
}

func (e *ValidationError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "validation failed"
}

func (e *ValidationError) Unwrap() []error {
	if e.Cause != nil {
		return []error{ErrValidation, e.Cause}
	}
	return []error{ErrValidation}
}

// MessageError menempelkan pesan untuk pengguna pada error domain, misalnya
// ErrUnauthenticated dengan "Username atau password salah."
type MessageError struct {
	Err     error
	Message string
}

func (e *MessageError) Error() string { return e.Message }
func (e *MessageError) Unwrap() error { return e.Err }
