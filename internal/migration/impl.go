package migration

var (
	_ store            = (*Repo)(nil)
	_ migrationService = (*Service)(nil)
)
