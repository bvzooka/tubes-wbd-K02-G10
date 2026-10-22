package domain

import "time"

type MigrationStatus string

const (
	MigrationDraft     MigrationStatus = "draft"
	MigrationRunning   MigrationStatus = "running"
	MigrationSuccess   MigrationStatus = "success"
	MigrationFailed    MigrationStatus = "failed"
	MigrationCancelled MigrationStatus = "cancelled"
)

type Migration struct {
	ID             int64
	ConnectionID   int64
	Version        int
	Name           string
	SQL            string
	Checksum       string
	Status         MigrationStatus
	ErrorMessage   string
	Warning        string
	CreatedBy      int64
	CreatedByName  string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ExecutedBy     *int64
	ExecutedByName string
	ExecutedAt     *time.Time
	FinishedAt     *time.Time
}
