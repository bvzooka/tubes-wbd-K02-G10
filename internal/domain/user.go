package domain

import "time"

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

type Group struct {
	ID          int64
	Name        string
	Description string
	CreatedAt   time.Time
}

// AccessibleDatabase adalah satu database yang boleh diakses seorang user beserta level tertingginya.
type AccessibleDatabase struct {
	ConnectionID int64
	Name         string
	DBMS         DBMS
	Level        AccessLevel
}
