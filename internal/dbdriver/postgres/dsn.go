package postgres

import (
	"net"
	"net/url"
	"strconv"

	"dbplatform/internal/domain"
)

// applicationName tampil di pg_stat_activity, memudahkan admin database mengenali koneksi platform
const applicationName = "dbplatform"

// BuildURL menyusun URL koneksi PostgreSQL dari data koneksi dan password.
func BuildURL(c domain.Connection, password string) string {
	q := url.Values{}
	q.Set("sslmode", c.SSLMode)
	q.Set("application_name", applicationName)
	q.Set("connect_timeout", strconv.Itoa(int(connectTimeout.Seconds())))

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
