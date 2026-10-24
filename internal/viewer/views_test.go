package viewer

import (
	"testing"
	"dbplatform/internal/domain"
)

func TestDBMSLabel(t *testing.T) {
	cases := map[domain.DBMS]string{
		domain.DBMSPostgres: "PostgreSQL",
		domain.DBMSMySQL: "MySQL",
		domain.DBMSMariaDB: "MariaDB",
		"oracle": "oracle",
	}
	for in, want := range cases {
		if got := dbmsLabel(in); got != want {
			t.Errorf("dbmsLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewDatabaseItem_UsesDBMSLabel(t *testing.T) {
	for dbms, want := range map[domain.DBMS]string{domain.DBMSMySQL: "MySQL", domain.DBMSMariaDB: "MariaDB"} {
		item := newDatabaseItem(domain.AccessibleDatabase{ConnectionID: 1, Name: "x", DBMS: dbms, Level: domain.AccessViewOnly})
		if item.DBMSLabel != want || item.DBMS != string(dbms) {
			t.Errorf("%s: DBMS=%q DBMSLabel=%q", dbms, item.DBMS, item.DBMSLabel)
		}
	}
}
