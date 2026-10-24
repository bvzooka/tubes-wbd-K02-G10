package migration

import (
	"fmt"

	"dbplatform/internal/domain"
)

type OutOfOrderError struct {
	Blocking int
}

func (e *OutOfOrderError) Error() string {
	return fmt.Sprintf("migration version %d must be executed first", e.Blocking)
}

func (e *OutOfOrderError) Unwrap() error { return domain.ErrMigrationOutOfOrder }
