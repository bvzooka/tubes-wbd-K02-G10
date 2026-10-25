package postgres

import (
	"net"
	"net/url"
	"strconv"
	"dbplatform/internal/domain"
)

// BuildURL buat URL koneksi PostgreSQL dari data koneksi dan password.
func BuildURL(c domain.Connection, password string) string {
	q := url.Values{}
	q.Set("sslmode", c.SSLMode)
	q.Set("application_name", "dbplatform")
	q.Set("connect_timeout", "5")

	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(c.Username, password),
		Host:     net.JoinHostPort(c.Host, strconv.Itoa(c.Port)),
		Path:     "/" + c.DatabaseName,
		RawQuery: q.Encode(),
	}
	return u.String()
}
