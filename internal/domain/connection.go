package domain

import "time"

type DBMS string

const (
	DBMSPostgres DBMS = "postgres"
	DBMSMySQL    DBMS = "mysql"
	DBMSMariaDB  DBMS = "mariadb"
)

// Connection tidak pernah memuat password.
type Connection struct {
	ID           int64
	Name         string
	DBMS         DBMS
	Host         string
	Port         int
	DatabaseName string
	Username     string
	SSLMode      string
	IsActive     bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ConnectionInput dengan Password kosong pada update berarti password tidak diubah.
type ConnectionInput struct {
	Name         string
	DBMS         DBMS
	Host         string
	Port         int
	DatabaseName string
	Username     string
	Password     string
	SSLMode      string
}
