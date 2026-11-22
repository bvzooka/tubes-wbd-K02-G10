package domain

import "time"

// MigrationStatus adalah tahap hidup sebuah migration
type MigrationStatus string

const (
	MigrationDraft     MigrationStatus = "draft"
	MigrationRunning   MigrationStatus = "running"
	MigrationSuccess   MigrationStatus = "success"
	MigrationFailed    MigrationStatus = "failed"
	MigrationCancelled MigrationStatus = "cancelled"
)

// Migration adalah satu perubahan skema dengan nomor versi, SQL-nya, dan hasil eksekusinya
type Migration struct {
	ID            int64
	ConnectionID  int64
	Version       int
	Name          string
	SQL           string
	Checksum      string
	Status        MigrationStatus
	ErrorMessage  string
	Warning       string
	CreatedBy     int64
	CreatedByName string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	// pointer karena kosong selama migration masih draft
	ExecutedBy     *int64
	ExecutedByName string
	ExecutedAt     *time.Time
	FinishedAt     *time.Time
}
