package connmgr

import (
	"context"
	"errors"
	"strings"
	"testing"
	"dbplatform/internal/domain"
)

func TestDefaultFactory_UnknownDBMS(t *testing.T) {
	_, err := DefaultFactory(context.Background(), domain.Connection{DBMS: "oracle"}, "x")
	if !errors.Is(err, domain.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestDefaultFactory_MySQLFamilyRoutesToMySQLDriver(t *testing.T) {
	for _, dbms := range []domain.DBMS{domain.DBMSMySQL, domain.DBMSMariaDB} {
		t.Run(string(dbms), func(t *testing.T) {
			conn := domain.Connection{DBMS: dbms, Host: "127.0.0.1", Port: 1, DatabaseName: "x", Username: "u", SSLMode: "disable"}
			drv, err := DefaultFactory(context.Background(), conn, "rahasia-banget")
			if err == nil {
				drv.Close()
				t.Fatal("seharusnya gagal terhubung")
			}
			if !errors.Is(err, domain.ErrConnectionFailed) {
				t.Fatalf("err = %v, want ErrConnectionFailed", err)
			}
			if strings.Contains(err.Error(), "rahasia-banget") {
				t.Errorf("pesan error membocorkan password: %v", err)
			}
		})
	}
}
