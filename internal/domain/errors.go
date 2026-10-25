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
)

// Bawa pesan per kolom form
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string { return "validation failed" }
func (e *ValidationError) Unwrap() error { return ErrValidation }
