package mysql

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"dbplatform/internal/domain"
)

func serverHasTLS(t *testing.T, d *Driver) bool {
	t.Helper()
	var name, val string
	err := rawDB(t, d).QueryRow("SHOW GLOBAL VARIABLES LIKE 'have_ssl'").Scan(&name, &val)
	if err == nil {
		return strings.EqualFold(val, "YES")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("have_ssl: %v", err)
	}
	err = rawDB(t, d).QueryRow("SHOW GLOBAL STATUS LIKE 'Ssl_server_not_after'").Scan(&name, &val)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Ssl_server_not_after: %v", err)
	}
	return err == nil && val != ""
}

func sessionCipher(t *testing.T, d *Driver) string {
	t.Helper()
	var name, val string
	if err := rawDB(t, d).QueryRow("SHOW SESSION STATUS LIKE 'Ssl_cipher'").Scan(&name, &val); err != nil {
		t.Fatalf("Ssl_cipher: %v", err)
	}
	return val
}

func TestSSLModes_Integration(t *testing.T) {
	admin := newTestDriver(t)
	tls := serverHasTLS(t, admin)

	connect := func(mode string) (*Driver, error) {
		c := testConn()
		c.SSLMode = mode
		drv, err := New(context.Background(), c, testPass)
		if err != nil {
			return nil, err
		}
		t.Cleanup(drv.Close)
		return drv.(*Driver), nil
	}

	d, err := connect("disable")
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if c := sessionCipher(t, d); c != "" {
		t.Errorf("disable tidak boleh memakai TLS, cipher = %q", c)
	}

	for _, mode := range []string{"allow", "prefer"} {
		d, err := connect(mode)
		if err != nil && strings.HasPrefix(err.Error(), "Negosiasi SSL") {
			t.Skipf("server ini menolak cipher TLS bawaan Go (mis. MySQL 5.7); gunakan ssl_mode disable: %v", err)
		}
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if got := sessionCipher(t, d) != ""; got != tls {
			t.Errorf("%s: TLS dipakai = %v, server mendukung TLS = %v", mode, got, tls)
		}
	}

	if tls {
		d, err := connect("require")
		if err != nil {
			t.Fatalf("require: %v", err)
		}
		if sessionCipher(t, d) == "" {
			t.Error("require harus memakai TLS")
		}
		for _, mode := range []string{"verify-ca", "verify-full"} {
			_, err := connect(mode)
			if !errors.Is(err, domain.ErrConnectionFailed) || err.Error() != "Sertifikat SSL server tidak dapat diverifikasi" {
				t.Errorf("%s terhadap sertifikat self-signed: %v", mode, err)
			}
			if err != nil && strings.Contains(err.Error(), testPass) {
				t.Errorf("%s: pesan membocorkan password", mode)
			}
		}
		return
	}

	for _, mode := range []string{"require", "verify-ca", "verify-full"} {
		_, err := connect(mode)
		if !errors.Is(err, domain.ErrConnectionFailed) || err.Error() != "Tidak dapat terhubung ke database (server tidak mendukung SSL)" {
			t.Errorf("%s terhadap server tanpa TLS: %v", mode, err)
		}
	}
}
