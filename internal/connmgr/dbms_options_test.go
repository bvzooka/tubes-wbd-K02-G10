package connmgr

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"
	"dbplatform/internal/domain"
	"dbplatform/internal/httpx"
	webassets "dbplatform/web"
)

func validInputFor(dbms domain.DBMS) domain.ConnectionInput {
	return domain.ConnectionInput{
		Name: "uji", DBMS: dbms, Host: "h", Port: 3306, DatabaseName: "d",
		Username: "u", Password: "p", SSLMode: "disable",
	}
}

func TestDBMSOptions_ValuesMatchValidateInput(t *testing.T) {
	var got []string
	seen := map[string]bool{}
	for _, o := range dbmsOptions {
		if o.Label == "" {
			t.Errorf("label kosong untuk %q", o.Value)
		}
		if seen[o.Value] {
			t.Errorf("nilai ganda %q", o.Value)
		}
		seen[o.Value] = true
		got = append(got, o.Value)

		if err := ValidateInput(validInputFor(domain.DBMS(o.Value)), false); err != nil {
			t.Errorf("opsi %q ditolak ValidateInput: %v", o.Value, err)
		}
	}
	sort.Strings(got)
	want := []string{string(domain.DBMSMariaDB), string(domain.DBMSMySQL), string(domain.DBMSPostgres)}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("opsi = %v, want %v", got, want)
	}

	err := ValidateInput(validInputFor("oracle"), false)
	ve, ok := err.(*domain.ValidationError)
	if !ok || ve.Fields["dbms"] == "" {
		t.Errorf("dbms asing harus ditolak pada field dbms: %v", err)
	}
}

func TestNewFormPage_DoesNotForceDefaultPort(t *testing.T) {
	p := newFormPage()
	if p.Values.Port == "5432" {
		t.Error("port bawaan tidak boleh dipaksa 5432")
	}
	if p.Values.DBMS != string(domain.DBMSPostgres) {
		t.Errorf("DBMS bawaan = %q", p.Values.DBMS)
	}
	if len(p.DBMSOptions) != 3 {
		t.Errorf("opsi DBMS = %d", len(p.DBMSOptions))
	}
}

func TestFormTemplate_OffersAllDBMSWithoutPrefilledPort(t *testing.T) {
	render, err := httpx.NewRenderer(webassets.FS, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	render.Page(rec, httptest.NewRequest(http.MethodGet, "/admin/connections/new", nil), http.StatusOK, "connections/form.html", newFormPage())
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, body)
	}

	for _, want := range []string{
		`<option value="postgres" selected>PostgreSQL</option>`,
		`<option value="mysql" >MySQL</option>`,
		`<option value="mariadb" >MariaDB</option>`,
		"Bawaan: PostgreSQL 5432, MySQL dan MariaDB 3306.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("form tidak memuat %q", want)
		}
	}
	if !regexp.MustCompile(`id="port"[^>]*value=""`).MatchString(body) {
		t.Error("kolom port harus kosong pada form baru")
	}
	if strings.Contains(body, `value="5432"`) {
		t.Error("form tidak boleh mengisi port 5432")
	}
}

func TestFormTemplate_KeepsChosenDBMSAndPortOnRerender(t *testing.T) {
	render, err := httpx.NewRenderer(webassets.FS, nil)
	if err != nil {
		t.Fatal(err)
	}
	page := newFormPage()
	page.Values.DBMS = string(domain.DBMSMariaDB)
	page.Values.Port = "3307"
	rec := httptest.NewRecorder()
	render.Page(rec, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusUnprocessableEntity, "connections/form.html", page)
	body := rec.Body.String()
	if !strings.Contains(body, `<option value="mariadb" selected>MariaDB</option>`) || !strings.Contains(body, `value="3307"`) {
		t.Error("pilihan DBMS dan port harus dipertahankan")
	}
	if strings.Contains(body, `<option value="postgres" selected>`) {
		t.Error("hanya satu opsi yang boleh terpilih")
	}
}
