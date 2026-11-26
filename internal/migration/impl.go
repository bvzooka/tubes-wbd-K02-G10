package migration

import "dbplatform/internal/contract"

var (
	_ definitionStore = (*Repo)(nil)
	_ executionStore  = (*Repo)(nil)
	_ runnerStore     = (*Repo)(nil)

	_ contract.ExecutionRunner = (*Runner)(nil)
)
