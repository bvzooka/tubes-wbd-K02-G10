package migration

var (
	_ store            = (*Repo)(nil)
	_ definitionStore  = (*Repo)(nil)
	_ migrationService = (*Service)(nil)
)
