package explain_test

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"dbplatform/internal/access"
	"dbplatform/internal/auth/authtest"
	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
	"dbplatform/internal/explain"
)

type staticProvider struct{ drv dbdriver.Driver }

func (p staticProvider) Get(context.Context, int64) (dbdriver.Driver, error) { return p.drv, nil }

type levelAuthz domain.AccessLevel

func (l levelAuthz) EffectiveLevel(context.Context, int64, int64) (domain.AccessLevel, error) {
	return domain.AccessLevel(l), nil
}
func (l levelAuthz) Can(_ context.Context, _, _ int64, min domain.AccessLevel) (bool, error) {
	return domain.AccessLevel(l).AtLeast(min), nil
}
func (l levelAuthz) ListAccessible(context.Context, int64) ([]domain.AccessibleDatabase, error) {
	return nil, nil
}

const targetSQL = `
CREATE TABLE customers (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE orders (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  customer_id BIGINT NOT NULL REFERENCES customers (id), total NUMERIC(12,2) NOT NULL);
INSERT INTO customers (name) SELECT 'c' || g FROM generate_series(1, 50) g;
INSERT INTO orders (customer_id, total) SELECT (g % 50) + 1, g FROM generate_series(1, 200) g;
ANALYZE;`

func newTarget(t *testing.T) *authtest.PgDriver {
	t.Helper()
	if testing.Short() {
		t.Skip("integration")
	}
	return &authtest.PgDriver{Pool: authtest.NewTargetDB(t, targetSQL)}
}

func countOrders(t *testing.T, d *authtest.PgDriver) int {
	t.Helper()
	var n int
	if err := d.Pool.QueryRow(context.Background(), `SELECT count(*) FROM orders`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

var editor = domain.Principal{UserID: 1, Username: "editor"}

func TestExplain_Integration_SelectPlan(t *testing.T) {
	drv := newTarget(t)
	svc := explain.NewService(levelAuthz(domain.AccessViewEdit), staticProvider{drv})
	res, err := svc.Run(context.Background(), editor, 1,
		"SELECT o.id, c.name FROM orders o JOIN customers c ON c.id = o.customer_id WHERE o.total > 10;", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Plan == nil || len(res.Plan.Nodes) < 2 {
		t.Fatalf("plan = %+v raw=%q", res.Plan, res.RawText)
	}
	if res.Plan.ExecutionTimeMs != nil {
		t.Fatal("tanpa ANALYZE tidak boleh ada execution time")
	}
	if res.SQL != "SELECT o.id, c.name FROM orders o JOIN customers c ON c.id = o.customer_id WHERE o.total > 10" {
		t.Fatalf("SQL = %q", res.SQL)
	}
}

func TestExplain_Integration_AnalyzeInsertRollsBack(t *testing.T) {
	drv := newTarget(t)
	svc := explain.NewService(levelAuthz(domain.AccessViewEdit), staticProvider{drv})
	before := countOrders(t, drv)
	res, err := svc.Run(context.Background(), editor, 1,
		"INSERT INTO orders (customer_id, total) SELECT 1, 5 FROM generate_series(1, 25)", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Plan == nil || res.Plan.ExecutionTimeMs == nil || !res.Plan.Nodes[0].Analyzed {
		t.Fatalf("ANALYZE harus berisi waktu aktual: %+v", res.Plan)
	}
	if after := countOrders(t, drv); after != before {
		t.Fatalf("jumlah baris berubah %d -> %d", before, after)
	}
}

func TestExplain_Integration_PlainExplainOfDeleteIsReadOnly(t *testing.T) {
	drv := newTarget(t)
	svc := explain.NewService(levelAuthz(domain.AccessViewEdit), staticProvider{drv})
	before := countOrders(t, drv)
	if _, err := svc.Run(context.Background(), editor, 1, "DELETE FROM orders", false); err != nil {
		t.Fatal(err)
	}
	if countOrders(t, drv) != before {
		t.Fatal("EXPLAIN tanpa ANALYZE tidak boleh mengubah data")
	}
}

func TestExplain_Integration_Rejections(t *testing.T) {
	drv := newTarget(t)
	ctx := context.Background()

	viewOnly := explain.NewService(levelAuthz(domain.AccessViewOnly), staticProvider{drv})
	if _, err := viewOnly.Run(ctx, editor, 1, "SELECT 1", false); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("view_only: %v", err)
	}

	svc := explain.NewService(levelAuthz(domain.AccessViewEdit), staticProvider{drv})
	if _, err := svc.Run(ctx, editor, 1, "DROP TABLE orders", true); !errors.Is(err, domain.ErrUnsupported) {
		t.Fatalf("DDL: %v", err)
	}
	if _, err := svc.Run(ctx, editor, 1, "SELECT 1; DELETE FROM orders", true); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("multi statement: %v", err)
	}
	_, err := svc.Run(ctx, editor, 1, "SELECT * FROM missing_table", false)
	var ie *explain.InputError
	if !errors.As(err, &ie) || !strings.Contains(ie.Message, "missing_table") {
		t.Fatalf("tabel tidak ada harus jadi pesan untuk user: %v", err)
	}
	if countOrders(t, drv) != 200 {
		t.Fatal("data target berubah")
	}
}

func TestExplain_Integration_WithRealAuthorizer(t *testing.T) {
	drv := newTarget(t)
	app := authtest.NewAppDB(t)
	viewer := authtest.SeedUser(t, app, "viewer", "x", false)
	editorUser := authtest.SeedUser(t, app, "editor", "x", false)
	conn := authtest.SeedConnection(t, app, "sample", true)
	authtest.Grant(t, app, authtest.SeedGroup(t, app, "viewers", viewer.ID), conn, domain.AccessViewOnly)
	authtest.Grant(t, app, authtest.SeedGroup(t, app, "editors", editorUser.ID), conn, domain.AccessViewEdit)

	svc := explain.NewService(access.New(app), staticProvider{drv})
	ctx := context.Background()
	if _, err := svc.Run(ctx, domain.Principal{UserID: viewer.ID}, conn, "SELECT 1", false); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("view_only: %v", err)
	}
	if _, err := svc.Run(ctx, domain.Principal{UserID: editorUser.ID}, conn, "SELECT 1", false); err != nil {
		t.Fatalf("view_edit: %v", err)
	}
}

// handler 

type recRenderer struct {
	status int
	data   explain.ResultPage
}

func (r *recRenderer) Page(w http.ResponseWriter, _ *http.Request, status int, _ string, data any) {
	r.status, r.data = status, data.(explain.ResultPage)
	w.WriteHeader(status)
}

type passMW struct{ principal domain.Principal }

func (m passMW) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(domain.WithPrincipal(r.Context(), m.principal)))
	})
}
func (passMW) RequireAdmin(next http.Handler) http.Handler { return next }
func (passMW) RequireDBAccess(domain.AccessLevel) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler { return h }
}
func (passMW) CSRF(next http.Handler) http.Handler { return next }

type fakeRunner struct {
	res *explain.Result
	err error
	got struct {
		dbID    int64
		sql     string
		analyze bool
	}
}

func (f *fakeRunner) Run(_ context.Context, _ domain.Principal, dbID int64, sql string, analyze bool) (*explain.Result, error) {
	f.got.dbID, f.got.sql, f.got.analyze = dbID, sql, analyze
	return f.res, f.err
}

func TestHandler_Explain(t *testing.T) {
	tests := []struct {
		name    string
		runErr  error
		status  int
		wantErr bool
	}{
		{"sukses", nil, http.StatusOK, false},
		{"ddl ditolak", &explain.InputError{Kind: domain.ErrUnsupported, Message: "EXPLAIN hanya mendukung statement DML"}, http.StatusUnprocessableEntity, true},
		{"forbidden lewat error handler", domain.ErrForbidden, http.StatusForbidden, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			run := &fakeRunner{res: &explain.Result{SQL: "SELECT 1"}, err: tc.runErr}
			rr := &recRenderer{}
			h := explain.NewHandler(run, rr, func(w http.ResponseWriter, _ *http.Request, err error) {
				if errors.Is(err, domain.ErrForbidden) {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				w.WriteHeader(http.StatusInternalServerError)
			}, passMW{principal: editor})
			mux := http.NewServeMux()
			h.Register(mux)
			form := url.Values{"name": {"tambah index"}, "sql": {"SELECT 1"}, "analyze": {"on"}}
			r := httptest.NewRequest(http.MethodPost, "/databases/7/explain", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, r)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
			if run.got.dbID != 7 || run.got.sql != "SELECT 1" || !run.got.analyze {
				t.Fatalf("argumen: %+v", run.got)
			}
			if (tc.status == http.StatusOK || tc.wantErr) && (rr.data.Name != "tambah index" || rr.data.SQL != "SELECT 1") {
				t.Fatalf("nama dan SQL harus dibawa ke halaman hasil: %+v", rr.data)
			}
			if tc.wantErr && rr.data.Error == "" {
				t.Fatal("pesan error harus dirender")
			}
		})
	}
}

func TestHandler_ExplainForm(t *testing.T) {
	run := &fakeRunner{}
	rr := &recRenderer{}
	var gotErr error
	h := explain.NewHandler(run, rr, func(w http.ResponseWriter, _ *http.Request, err error) {
		gotErr = err
		w.WriteHeader(http.StatusNotFound)
	}, passMW{principal: editor})
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/databases/7/explain", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rr.data.DBID != 7 || rr.data.SQL != "" || rr.data.Result != nil {
		t.Fatalf("data form: %+v", rr.data)
	}
	if run.got.dbID != 0 {
		t.Fatal("GET tidak boleh menjalankan EXPLAIN")
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/databases/abc/explain", nil))
	if !errors.Is(gotErr, domain.ErrNotFound) {
		t.Fatalf("dbID tidak valid: err = %v, want ErrNotFound", gotErr)
	}
}

//  template 

const stubLayout = `{{define "base"}}{{template "content" .}}{{end}}{{define "csrf"}}<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">{{end}}`

type stubPageData struct {
	CSRFToken string
	Data      any
}

func TestResultTemplate_Renders(t *testing.T) {
	tmpl := template.Must(template.New("stub").Parse(stubLayout))
	tmpl = template.Must(tmpl.ParseFiles(filepath.Join("..", "..", "web", "templates", "explain", "result.html")))

	plan, err := explain.ParsePostgresPlan(`[{"Plan": {"Node Type": "Seq Scan", "Relation Name": "t<x>", "Alias": "t", "Total Cost": 1, "Plan Rows": 1, "Plan Width": 4, "Actual Total Time": 0.1, "Actual Rows": 1, "Actual Loops": 1}, "Planning Time": 0.1, "Execution Time": 0.2}]`)
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range []explain.ResultPage{
		{DBID: 3, Name: "cek <i>", SQL: "SELECT '<b>'", Analyze: true, Result: &explain.Result{SQL: "SELECT 1", Analyze: true, Plan: plan}},
		{DBID: 3, SQL: "DROP TABLE t", Error: "EXPLAIN hanya mendukung statement DML"},
		{DBID: 3, SQL: "SELECT 1", Result: &explain.Result{Dialect: domain.DBMSMySQL, RawText: "-> Table scan"}},
		{DBID: 3},
	} {
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "base", stubPageData{CSRFToken: "tok", Data: page}); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		if !strings.Contains(out, `value="tok"`) || !strings.Contains(out, "/databases/3/explain") {
			t.Fatal("form CSRF atau action hilang")
		}
		if !strings.Contains(out, `formaction="/databases/3/migrations"`) || !strings.Contains(out, `value="execute"`) {
			t.Fatal("tombol simpan sebagai migration hilang")
		}
		if strings.Contains(out, "<b>") || strings.Contains(out, "t<x>") || strings.Contains(out, "<i>") {
			t.Fatal("data harus di-escape")
		}
		if strings.Contains(out, "%!") {
			t.Fatalf("format angka rusak: %s", out)
		}
		if page.Analyze && (!strings.Contains(out, "0.100 ms") || !strings.Contains(out, "0.200 ms")) {
			t.Fatal("planning dan execution time harus tampil sebagai angka")
		}
	}
}
