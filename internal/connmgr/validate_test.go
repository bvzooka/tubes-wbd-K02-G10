package connmgr

import (
	"dbplatform/internal/domain"
	"errors"
	"strings"
	"testing"
)

func validInput() domain.ConnectionInput {
	return domain.ConnectionInput{
		Name: "sampledb",
		DBMS: domain.DBMSPostgres,
		Host: "localhost",
		Port: 5433,
		DatabaseName: "sampledb",
		Username: "postgres",
		Password: "postgres",
		SSLMode: "disable",
	}
}

func TestValidateInput_Valid(t *testing.T) {
	if err := ValidateInput(validInput(), false); err != nil {
		t.Fatalf("input valid ditolak: %v", err)
	}
}

func TestValidateInput_Fields(t *testing.T) {
	tests := []struct {
		name string
		mutate func(*domain.ConnectionInput)
		isUpdate bool
		wantField string
	}{
		{"nama kosong", func(c *domain.ConnectionInput) { c.Name = "" }, false, "name"},
		{"nama hanya spasi", func(c *domain.ConnectionInput) { c.Name = "   " }, false, "name"},
		{"nama 65 karakter", func(c *domain.ConnectionInput) { c.Name = strings.Repeat("a", 65) }, false, "name"},
		{"nama 64 karakter", func(c *domain.ConnectionInput) { c.Name = strings.Repeat("a", 64) }, false, ""},
		{"dbms tidak dikenal", func(c *domain.ConnectionInput) { c.DBMS = "oracle" }, false, "dbms"},
		{"dbms mysql", func(c *domain.ConnectionInput) { c.DBMS = domain.DBMSMySQL }, false, ""},
		{"host kosong", func(c *domain.ConnectionInput) { c.Host = "" }, false, "host"},
		{"port 0", func(c *domain.ConnectionInput) { c.Port = 0 }, false, "port"},
		{"port negatif", func(c *domain.ConnectionInput) { c.Port = -1 }, false, "port"},
		{"port 65536", func(c *domain.ConnectionInput) { c.Port = 65536 }, false, "port"},
		{"port 65535", func(c *domain.ConnectionInput) { c.Port = 65535 }, false, ""},
		{"nama database kosong", func(c *domain.ConnectionInput) { c.DatabaseName = "" }, false, "database_name"},
		{"username kosong", func(c *domain.ConnectionInput) { c.Username = "" }, false, "db_username"},
		{"password kosong saat create", func(c *domain.ConnectionInput) { c.Password = "" }, false, "password"},
		{"password kosong saat update", func(c *domain.ConnectionInput) { c.Password = "" }, true, ""},
		{"ssl mode kosong", func(c *domain.ConnectionInput) { c.SSLMode = "" }, false, "ssl_mode"},
		{"ssl mode asal", func(c *domain.ConnectionInput) { c.SSLMode = "bebas" }, false, "ssl_mode"},
		{"ssl mode verify-full", func(c *domain.ConnectionInput) { c.SSLMode = "verify-full" }, false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := validInput()
			tc.mutate(&in)
			err := ValidateInput(in, tc.isUpdate)

			if tc.wantField == "" {
				if err != nil {
					t.Fatalf("seharusnya lolos, dapat: %v", err)
				}
				return
			}
			var ve *domain.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("want *ValidationError, got %v", err)
			}
			if _, ok := ve.Fields[tc.wantField]; !ok {
				t.Errorf("field %q tidak ada di %v", tc.wantField, ve.Fields)
			}
			if !errors.Is(err, domain.ErrValidation) {
				t.Error("errors.Is(err, ErrValidation) harus true")
			}
		})
	}
}

func TestValidateInput_MultipleErrors(t *testing.T) {
	err := ValidateInput(domain.ConnectionInput{}, false)
	var ve *domain.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want *ValidationError, got %v", err)
	}
	for _, f := range []string{"name", "dbms", "host", "port", "database_name", "db_username", "password", "ssl_mode"} {
		if _, ok := ve.Fields[f]; !ok {
			t.Errorf("field %q seharusnya ada", f)
		}
	}
}

func TestNormalizeInput_TrimsButKeepsPassword(t *testing.T) {
	in := validInput()
	in.Name = "  db  "
	in.Host = " localhost "
	in.Password = " pass "
	got := NormalizeInput(in)
	if got.Name != "db" || got.Host != "localhost" {
		t.Errorf("trim gagal: %+v", got)
	}
	if got.Password != " pass " {
		t.Errorf("password tidak boleh di-trim: %q", got.Password)
	}
}
