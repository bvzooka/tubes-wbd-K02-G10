package mysql

import (
	"crypto/tls"
	"testing"
	gomysql "github.com/go-sql-driver/mysql"
	"dbplatform/internal/domain"
)

func TestBuildConfig_SpecialCharactersSurviveFormatAndParse(t *testing.T) {
	const pass = `p@ss:w/rd#?&=%'"\ ;` + "`"
	c := domain.Connection{DBMS: domain.DBMSMySQL, Host: "db.example", Port: 3306, DatabaseName: "my db", Username: "us:er@x", SSLMode: "disable"}
	cfg, err := BuildConfig(c, pass)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Passwd != pass || cfg.User != "us:er@x" || cfg.DBName != "my db" {
		t.Fatalf("kredensial berubah: %+v", cfg)
	}

	cfg.User = "app"
	back, err := gomysql.ParseDSN(cfg.FormatDSN())
	if err != nil {
		t.Fatalf("DSN hasil format tidak bisa di-parse: %v", err)
	}
	if back.Passwd != pass || back.User != "app" || back.DBName != "my db" || back.Addr != "db.example:3306" {
		t.Errorf("round-trip DSN berubah: %+v", back)
	}
}

func TestBuildConfig_Defaults(t *testing.T) {
	cfg, err := BuildConfig(domain.Connection{Host: "::1", Port: 3307, SSLMode: "prefer"}, "x")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "[::1]:3307" {
		t.Errorf("Addr = %q, IPv6 harus berkurung", cfg.Addr)
	}
	if cfg.MultiStatements {
		t.Error("multiStatements harus mati pada konfigurasi dasar")
	}
	if !cfg.InterpolateParams || cfg.ParseTime {
		t.Errorf("InterpolateParams=%v ParseTime=%v", cfg.InterpolateParams, cfg.ParseTime)
	}
	if cfg.Timeout != connectTimeout {
		t.Errorf("Timeout = %v", cfg.Timeout)
	}
}

func TestApplySSLMode(t *testing.T) {
	cases := map[string]string{
		"disable":     "false",
		"allow":       "preferred",
		"prefer":      "preferred",
		"":            "preferred",
		"require":     "skip-verify",
		"verify-full": "true",
	}
	for mode, want := range cases {
		cfg := gomysql.NewConfig()
		if err := applySSLMode(cfg, mode); err != nil {
			t.Fatalf("%q: %v", mode, err)
		}
		if cfg.TLSConfig != want {
			t.Errorf("%q -> TLSConfig %q, want %q", mode, cfg.TLSConfig, want)
		}
	}

	cfg := gomysql.NewConfig()
	if err := applySSLMode(cfg, "verify-ca"); err != nil {
		t.Fatal(err)
	}
	if cfg.TLS == nil || cfg.TLS.VerifyConnection == nil || cfg.TLS.MinVersion < tls.VersionTLS12 {
		t.Fatalf("verify-ca harus memakai tls.Config kustom: %+v", cfg.TLS)
	}
	if err := cfg.TLS.VerifyConnection(tls.ConnectionState{}); err == nil {
		t.Error("verify-ca tanpa sertifikat harus gagal")
	}

	if err := applySSLMode(gomysql.NewConfig(), "ngawur"); err == nil {
		t.Error("mode tidak dikenal harus ditolak")
	}
	if _, err := BuildConfig(domain.Connection{Host: "h", Port: 1, SSLMode: "ngawur"}, "x"); err == nil {
		t.Error("BuildConfig harus menolak ssl mode tidak valid")
	}
}

func TestQuoteIdent(t *testing.T) {
	cases := map[string]string{
		"orders":                  "`orders`",
		"Mixed Case":              "`Mixed Case`",
		"a`b":                     "`a``b`",
		"x`; DROP TABLE users;--": "`x``; DROP TABLE users;--`",
		"``":                      "``````",
		"":                        "``",
	}
	for in, want := range cases {
		if got := quoteIdent(in); got != want {
			t.Errorf("quoteIdent(%q) = %s, want %s", in, got, want)
		}
	}
	if got := quoteIdents([]string{"a", "b`c"}); got != "`a`, `b``c`" {
		t.Errorf("quoteIdents = %s", got)
	}
}
