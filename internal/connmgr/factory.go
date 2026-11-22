package connmgr

import (
	"context"
	"fmt"

	"dbplatform/internal/dbdriver"
	"dbplatform/internal/dbdriver/mysql"
	"dbplatform/internal/dbdriver/postgres"
	"dbplatform/internal/domain"
)

// DefaultFactory memilih driver sesuai jenis DBMS koneksi, DBMS yang belum didukung ditolak.
// Error dari driver tidak dibungkus karena pesannya ditampilkan apa adanya saat uji koneksi.
func DefaultFactory(ctx context.Context, conn domain.Connection, password string) (dbdriver.Driver, error) {
	switch conn.DBMS {
	case domain.DBMSPostgres:
		return postgres.New(ctx, conn, password)
	case domain.DBMSMySQL, domain.DBMSMariaDB:
		return mysql.New(ctx, conn, password)
	}
	return nil, fmt.Errorf("dbms %q belum didukung: %w", conn.DBMS, domain.ErrUnsupported)
}
