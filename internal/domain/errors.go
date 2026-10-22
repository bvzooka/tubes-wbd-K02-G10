package domain

import "errors"

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
)

// ValidationError membawa pesan per field. errors.Is(err, ErrValidation) bernilai true.
type ValidationError struct {
	Fields map[string]string // nama field form -> pesan (Bahasa Indonesia)
}

func (e *ValidationError) Error() string { return "validation failed" }
func (e *ValidationError) Unwrap() error { return ErrValidation }
