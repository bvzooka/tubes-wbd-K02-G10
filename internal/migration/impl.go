package migration

var (
	_ store            = (*Repo)(nil)
	_ definitionStore  = (*Repo)(nil)
	_ executionStore   = (*Repo)(nil)
	_ migrationService = (*Service)(nil)
)
