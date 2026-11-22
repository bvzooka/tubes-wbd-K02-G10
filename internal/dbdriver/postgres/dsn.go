package postgres

import (
	"dbplatform/internal/domain"
	"net"
	"net/url"
	"strconv"
)

// BuildURL buat URL koneksi PostgreSQL dari data koneksi dan password.
func BuildURL(c domain.Connection, password string) string {
	q := url.Values{}
	q.Set("sslmode", c.SSLMode)
	q.Set("application_name", "dbplatform")
	q.Set("connect_timeout", "5")

	// url.URL meng-escape username dan password otomatis, jadi karakter khusus di password tidak merusak URL
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(c.Username, password),
		Host:     net.JoinHostPort(c.Host, strconv.Itoa(c.Port)),
		Path:     "/" + c.DatabaseName,
		RawQuery: q.Encode(),
	}
	return u.String()
}
