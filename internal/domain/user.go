package domain

import "time"

// User adalah akun di platform
type User struct {
	ID              int64
	Username        string
	IsPlatformAdmin bool
	IsActive        bool
	CreatedAt       time.Time
}

// Principal adalah identitas yang sedang melakukan request (hasil autentikasi).
type Principal struct {
	UserID          int64
	Username        string
	IsPlatformAdmin bool
}

// Group mengelompokkan user untuk pemberian akses database
type Group struct {
	ID          int64
	Name        string
	Description string
	CreatedAt   time.Time
}

// AccessibleDatabase adalah database yang boleh dibuka user beserta level aksesnya
type AccessibleDatabase struct {
	ConnectionID int64
	Name         string
	DBMS         DBMS
	Level        AccessLevel
}
