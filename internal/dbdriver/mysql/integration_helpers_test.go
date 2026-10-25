package mysql

import (
	"context"
	"database/sql"
	"dbplatform/internal/domain"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"strconv"
	"testing"
	"time"
)

const (
	testDB = "sampledb"
	testUser = "root"
	testPass = "root"
)

var (
	testHost = envOr("TEST_MYSQL_HOST", "127.0.0.1")
	testPort = envInt("TEST_MYSQL_PORT", 3307)
	testDBMS = domain.DBMS(envOr("TEST_MYSQL_DBMS", string(domain.DBMSMySQL)))
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return n
	}
	return def
}

func testConn() domain.Connection {
	return domain.Connection{
		DBMS: testDBMS, Host: testHost, Port: testPort,
		DatabaseName: testDB, Username: testUser, SSLMode: "disable",
	}
}

func skipIfNoServer(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration")
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort(testHost, fmt.Sprint(testPort)), 2*time.Second)
	if err != nil {
		t.Skip("targetmysql tidak berjalan: docker compose --profile mysql up -d targetmysql")
	}
	_ = c.Close()
}

func newTestDriver(t *testing.T) *Driver {
	t.Helper()
	skipIfNoServer(t)
	return newDriverFor(t, testConn(), testPass)
}

func newDriverFor(t *testing.T, c domain.Connection, password string) *Driver {
	t.Helper()
	var lastErr error
	for i := 0; i < 30; i++ {
		drv, err := New(context.Background(), c, password)
		if err == nil {
			t.Cleanup(drv.Close)
			return drv.(*Driver)
		}
		lastErr = err
		time.Sleep(time.Second)
	}
	t.Fatalf("tidak bisa terhubung ke targetmysql: %v", lastErr)
	return nil
}

func rawDB(t *testing.T, d *Driver) *sql.DB {
	t.Helper()
	return d.ro
}

func uniqueName(prefix string) string {
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixNano()%1_000_000, rand.Intn(1000))
}

func scratchTable(t *testing.T, d *Driver, ddl string) string {
	t.Helper()
	name := uniqueName("dbp_t")
	db := rawDB(t, d)
	if _, err := db.Exec(fmt.Sprintf(ddl, quoteIdent(name))); err != nil {
		t.Fatalf("buat tabel uji: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DROP TABLE IF EXISTS " + quoteIdent(name)) })
	return name
}

func countRows(t *testing.T, d *Driver, table string) int {
	t.Helper()
	var n int
	if err := rawDB(t, d).QueryRow("SELECT COUNT(*) FROM " + quoteIdent(table)).Scan(&n); err != nil {
		t.Fatalf("hitung baris: %v", err)
	}
	return n
}

func isErr(err error, target error) bool { return errors.Is(err, target) }

func serverCaps(t *testing.T, d *Driver) serverInfo {
	t.Helper()
	var info serverInfo
	err := d.runTx(context.Background(), d.ro, txSpec{readOnly: true}, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		info, err = detectServer(ctx, tx)
		return err
	})
	if err != nil {
		t.Fatalf("deteksi server: %v", err)
	}
	return info
}
