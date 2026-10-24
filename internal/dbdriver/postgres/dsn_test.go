package postgres

import (
	"net/url"
	"testing"
	"dbplatform/internal/domain"
)

func testConn() domain.Connection {
	return domain.Connection{
		Host: "db.example.com",
		Port: 5432,
		DatabaseName: "sales",
		Username: "app_user",
		SSLMode: "require",
	}
}

func TestBuildURL_Basic(t *testing.T) {
	u, err := url.Parse(BuildURL(testConn(), "secret"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if u.Scheme != "postgres" || u.Host != "db.example.com:5432" || u.Path != "/sales" {
		t.Errorf("url salah: %s", u.String())
	}
	q := u.Query()
	if q.Get("sslmode") != "require" || q.Get("application_name") != "dbplatform" || q.Get("connect_timeout") != "5" {
		t.Errorf("query salah: %v", q)
	}
}

func TestBuildURL_SpecialCharPassword(t *testing.T) {
	for _, pw := range []string{"p@ss:w/ord?#", "a b%c&d=e", "日本語", "\"'; --"} {
		c := testConn()
		c.Username = "us@er:name"
		u, err := url.Parse(BuildURL(c, pw))
		if err != nil {
			t.Fatalf("parse %q: %v", pw, err)
		}
		got, _ := u.User.Password()
		if got != pw || u.User.Username() != c.Username {
			t.Errorf("kredensial tidak round-trip: user=%q pass=%q", u.User.Username(), got)
		}
		if u.Host != "db.example.com:5432" || u.Path != "/sales" {
			t.Errorf("host/path rusak oleh password %q: %s", pw, u.String())
		}
	}
}

func TestBuildURL_IPv6Host(t *testing.T) {
	c := testConn()
	c.Host = "::1"
	u, err := url.Parse(BuildURL(c, "x"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Hostname() != "::1" || u.Port() != "5432" {
		t.Errorf("host ipv6 salah: %s", u.Host)
	}
}
