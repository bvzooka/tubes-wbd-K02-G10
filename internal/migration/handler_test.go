package migration_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"dbplatform/internal/authstub"
	"dbplatform/internal/domain"
	"dbplatform/internal/httpx"
	"dbplatform/internal/migration"
	webassets "dbplatform/web"
)

const (
	editorID int64 = 1
	readerID int64 = 2
	dbID     int64 = 5
)

type fakeAccess struct {
	level domain.AccessLevel
	err   error
}

func (f fakeAccess) EffectiveLevel(context.Context, int64, int64) (domain.AccessLevel, error) {
	return f.level, f.err
}

func (f fakeAccess) Can(_ context.Context, _, _ int64, min domain.AccessLevel) (bool, error) {
	return f.level.AtLeast(min), f.err
}

func (f fakeAccess) ListAccessible(context.Context, int64) ([]domain.AccessibleDatabase, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []domain.AccessibleDatabase{{ConnectionID: dbID, Name: "Sampel <db>", DBMS: domain.DBMSPostgres, Level: f.level}}, nil
}

type fakeMigrationService struct {
	list          []domain.Migration
	get           domain.Migration
	createResult  domain.Migration
	executeResult domain.Migration
	listErr       error
	getErr        error
	createErr     error
	updateErr     error
	cancelErr     error
	executeErr    error

	createdIn        migration.Input
	updatedIn        migration.Input
	updatedVersion   int
	executedVersion  int
	executeCalls     int
	cancelledVersion int
	writes           int
}

func writeDenied(actor domain.Principal) bool { return actor.UserID == readerID }

func (f *fakeMigrationService) List(context.Context, domain.Principal, int64) ([]domain.Migration, error) {
	return f.list, f.listErr
}

func (f *fakeMigrationService) Get(context.Context, domain.Principal, int64, int) (domain.Migration, error) {
	return f.get, f.getErr
}

func (f *fakeMigrationService) Create(_ context.Context, actor domain.Principal, _ int64, in migration.Input) (domain.Migration, error) {
	f.createdIn = in
	if writeDenied(actor) {
		return domain.Migration{}, domain.ErrForbidden
	}
	f.writes++
	return f.createResult, f.createErr
}

func (f *fakeMigrationService) Update(_ context.Context, actor domain.Principal, _ int64, version int, in migration.Input) (domain.Migration, error) {
	f.updatedIn, f.updatedVersion = in, version
	if writeDenied(actor) {
		return domain.Migration{}, domain.ErrForbidden
	}
	f.writes++
	return domain.Migration{Version: version}, f.updateErr
}

func (f *fakeMigrationService) Cancel(_ context.Context, actor domain.Principal, _ int64, version int) error {
	f.cancelledVersion = version
	if writeDenied(actor) {
		return domain.ErrForbidden
	}
	f.writes++
	return f.cancelErr
}

func (f *fakeMigrationService) Execute(_ context.Context, actor domain.Principal, _ int64, version int) (domain.Migration, error) {
	f.executeCalls++
	f.executedVersion = version
	if writeDenied(actor) {
		return domain.Migration{}, domain.ErrForbidden
	}
	f.writes++
	return f.executeResult, f.executeErr
}

func newMigrationServer(t *testing.T, svc *fakeMigrationService, actorID int64, access fakeAccess) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	flash := httpx.NewFlasher(make([]byte, 32), false)
	render, err := httpx.NewRenderer(webassets.FS, flash)
	if err != nil {
		t.Fatal(err)
	}
	d := httpx.Deps{
		Logger: logger, Render: render, Flash: flash,
		HandleErr: httpx.HandleError(render, logger),
		Auth:      &authstub.Middleware{Principal: domain.Principal{UserID: actorID, Username: "pengguna"}},
		Authz:     access,
	}
	mux := http.NewServeMux()
	migration.NewHandler(d, svc).Register(mux)
	return mux
}

func editorServer(t *testing.T, svc *fakeMigrationService) http.Handler {
	return newMigrationServer(t, svc, editorID, fakeAccess{level: domain.AccessViewEdit})
}

func readerServer(t *testing.T, svc *fakeMigrationService) http.Handler {
	return newMigrationServer(t, svc, readerID, fakeAccess{level: domain.AccessViewOnly})
}

func httpGet(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func httpPost(h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func flashOf(t *testing.T, rec *httptest.ResponseRecorder) *httpx.Flash {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == httpx.FlashCookieName && c.MaxAge >= 0 {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
			return httpx.NewFlasher(make([]byte, 32), false).Take(httptest.NewRecorder(), req)
		}
	}
	return nil
}

func sampleMigration(version int, status domain.MigrationStatus) domain.Migration {
	created := time.Date(2026, 10, 20, 9, 30, 0, 0, time.UTC)
	return domain.Migration{
		ID: int64(version), ConnectionID: dbID, Version: version, Name: "tambah <tabel>",
		SQL:      "CREATE TABLE a (id int);\nINSERT INTO a VALUES ('<b>x</b>')",
		Checksum: "abc123", Status: status, CreatedBy: editorID, CreatedByName: "budi",
		CreatedAt: created, UpdatedAt: created,
	}
}

func TestMigrationHandler_List(t *testing.T) {
	executed := time.Date(2026, 10, 21, 14, 5, 0, 0, time.UTC)
	done := sampleMigration(1, domain.MigrationSuccess)
	done.ExecutedAt = &executed
	svc := &fakeMigrationService{list: []domain.Migration{sampleMigration(2, domain.MigrationDraft), done}}

	body := httpGet(editorServer(t, svc), "/databases/5/migrations").Body.String()
	for _, want := range []string{
		`href="/databases/5/migrations/1"`, `href="/databases/5/migrations/2"`, "badge-draft", "badge-success",
		"tambah &lt;tabel&gt;", "budi", "20 Oct 2026 09:30", "21 Oct 2026 14:05", "Sampel &lt;db&gt;",
		`href="/databases/5/migrations/new"`, `href="/databases/5/schema-diff"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("daftar tidak memuat %q", want)
		}
	}
	if strings.Contains(body, "tambah <tabel>") || strings.Contains(body, "Sampel <db>") {
		t.Error("nama harus di-escape")
	}
}

func TestMigrationHandler_ListHidesCreateButtonFromViewOnly(t *testing.T) {
	svc := &fakeMigrationService{list: []domain.Migration{sampleMigration(1, domain.MigrationSuccess)}}
	body := httpGet(readerServer(t, svc), "/databases/5/migrations").Body.String()

	if strings.Contains(body, "/migrations/new") || strings.Contains(body, "/schema-diff") {
		t.Error("view_only tidak boleh melihat tombol migration baru maupun schema diff")
	}
	if !strings.Contains(body, `href="/databases/5/migrations/1"`) {
		t.Error("view_only tetap harus bisa melihat riwayat")
	}
}

func TestMigrationHandler_ListEmpty(t *testing.T) {
	body := httpGet(editorServer(t, &fakeMigrationService{}), "/databases/5/migrations").Body.String()
	if !strings.Contains(body, "Belum ada migration") {
		t.Error("keadaan kosong harus ditampilkan")
	}
}

func TestMigrationHandler_NewEditorHasExplainButtonAndCSRF(t *testing.T) {
	rec := httpGet(editorServer(t, &fakeMigrationService{}), "/databases/5/migrations/new")

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, want := range []string{
		`action="/databases/5/migrations"`, `name="csrf_token"`, `name="name"`, `name="sql"`,
		`name="action" value="execute"`, `formaction="/databases/5/explain"`, `name="analyze"`, "Simpan draft",
		`href="/databases/5/schema-diff"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("editor tidak memuat %q", want)
		}
	}
}

func TestMigrationHandler_CreateSavesDraftAndRedirectsToDetail(t *testing.T) {
	svc := &fakeMigrationService{createResult: sampleMigration(3, domain.MigrationDraft)}
	rec := httpPost(editorServer(t, svc), "/databases/5/migrations", url.Values{"name": {"tambah tabel"}, "sql": {"CREATE TABLE a (id int)"}})

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/databases/5/migrations/3" {
		t.Fatalf("status = %d, location = %q", rec.Code, rec.Header().Get("Location"))
	}
	if svc.createdIn.Name != "tambah tabel" || svc.createdIn.SQL != "CREATE TABLE a (id int)" {
		t.Errorf("input ke service = %+v", svc.createdIn)
	}
	if svc.executeCalls != 0 {
		t.Error("simpan draft tidak boleh menjalankan migration")
	}
	if fl := flashOf(t, rec); fl == nil || fl.Kind != httpx.FlashSuccess || !strings.Contains(fl.Message, "versi 3") {
		t.Errorf("flash = %+v", fl)
	}
}

func TestMigrationHandler_CreateThenExecute(t *testing.T) {
	tests := []struct {
		name     string
		result   domain.Migration
		execErr  error
		wantKind string
		wantText string
	}{
		{"berhasil", sampleMigration(3, domain.MigrationSuccess), nil, httpx.FlashSuccess, "berhasil dijalankan"},
		{"gagal dengan rollback", sampleMigration(3, domain.MigrationFailed), nil, httpx.FlashError, "dibatalkan"},
		{"gagal tanpa rollback ddl", func() domain.Migration {
			m := sampleMigration(3, domain.MigrationFailed)
			m.Warning = "tanpa rollback"
			return m
		}(), nil, httpx.FlashError, "peringatan"},
		{"tidak berurutan", domain.Migration{}, &migration.OutOfOrderError{Blocking: 2}, httpx.FlashError, "versi 2 lebih dulu"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeMigrationService{createResult: sampleMigration(3, domain.MigrationDraft), executeResult: tt.result, executeErr: tt.execErr}
			rec := httpPost(editorServer(t, svc), "/databases/5/migrations", url.Values{
				"name": {"x"}, "sql": {"SELECT 1"}, "action": {"execute"},
			})

			if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/databases/5/migrations/3" {
				t.Fatalf("status = %d, location = %q", rec.Code, rec.Header().Get("Location"))
			}
			if svc.executeCalls != 1 || svc.executedVersion != 3 {
				t.Errorf("Execute dipanggil %d kali untuk versi %d", svc.executeCalls, svc.executedVersion)
			}
			if fl := flashOf(t, rec); fl == nil || fl.Kind != tt.wantKind || !strings.Contains(fl.Message, tt.wantText) {
				t.Errorf("flash = %+v, mau %s memuat %q", fl, tt.wantKind, tt.wantText)
			}
		})
	}
}

func TestMigrationHandler_CreateValidationErrorRerendersEditor(t *testing.T) {
	svc := &fakeMigrationService{createErr: &domain.ValidationError{Fields: map[string]string{
		"name": "Nama migration harus 1 sampai 120 karakter.",
		"sql":  "SQL tidak boleh berisi perintah kontrol transaksi (COMMIT).",
	}}}
	rec := httpPost(editorServer(t, svc), "/databases/5/migrations", url.Values{"name": {""}, "sql": {"CREATE TABLE a (id int); COMMIT;"}, "action": {"execute"}})

	body := rec.Body.String()
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, mau 422", rec.Code)
	}
	for _, want := range []string{"Nama migration harus 1 sampai 120 karakter.", "kontrol transaksi (COMMIT)", "CREATE TABLE a (id int); COMMIT;"} {
		if !strings.Contains(body, want) {
			t.Errorf("body tidak memuat %q", want)
		}
	}
	if svc.executeCalls != 0 {
		t.Error("input tidak valid tidak boleh dijalankan")
	}
}

func TestMigrationHandler_DetailShowsEverythingAndEscapes(t *testing.T) {
	m := sampleMigration(2, domain.MigrationFailed)
	executed := time.Date(2026, 10, 21, 14, 5, 7, 0, time.UTC)
	m.ExecutedAt, m.FinishedAt = &executed, &executed
	m.ExecutedByName = "ani"
	m.ErrorMessage = `ERROR: relation "x" does not exist`
	m.Warning = "DBMS ini tidak mendukung rollback untuk DDL"
	svc := &fakeMigrationService{get: m}

	body := httpGet(editorServer(t, svc), "/databases/5/migrations/2").Body.String()
	for _, want := range []string{
		"Versi 2", "badge-failed", "budi", "ani", "21 Oct 2026 14:05:07", "abc123",
		"relation &#34;x&#34; does not exist", "tidak mendukung rollback untuk DDL",
		"CREATE TABLE a (id int);", "&lt;b&gt;x&lt;/b&gt;",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail tidak memuat %q", want)
		}
	}
	if strings.Contains(body, "<b>x</b>") || strings.Contains(body, "tambah <tabel>") {
		t.Error("SQL dan nama harus di-escape")
	}
}

func TestMigrationHandler_DetailActionsOnlyForEditorOnDrafts(t *testing.T) {
	actions := []string{`/migrations/2/execute"`, `/migrations/2/cancel"`, `/migrations/2/edit"`}

	draft := &fakeMigrationService{get: sampleMigration(2, domain.MigrationDraft)}
	body := httpGet(editorServer(t, draft), "/databases/5/migrations/2").Body.String()
	for _, want := range actions {
		if !strings.Contains(body, want) {
			t.Errorf("draft untuk editor harus punya aksi %q", want)
		}
	}

	body = httpGet(readerServer(t, draft), "/databases/5/migrations/2").Body.String()
	for _, bad := range actions {
		if strings.Contains(body, bad) {
			t.Errorf("view_only tidak boleh melihat aksi %q", bad)
		}
	}

	for _, status := range []domain.MigrationStatus{domain.MigrationSuccess, domain.MigrationFailed, domain.MigrationCancelled, domain.MigrationRunning} {
		svc := &fakeMigrationService{get: sampleMigration(2, status)}
		body := httpGet(editorServer(t, svc), "/databases/5/migrations/2").Body.String()
		for _, bad := range actions {
			if strings.Contains(body, bad) {
				t.Errorf("status %s tidak boleh punya aksi %q", status, bad)
			}
		}
	}
}

func TestMigrationHandler_DetailNotFoundAndInvalidIDs(t *testing.T) {
	h := editorServer(t, &fakeMigrationService{getErr: domain.ErrNotFound})
	if rec := httpGet(h, "/databases/5/migrations/9"); rec.Code != http.StatusNotFound {
		t.Errorf("versi tak ada: status = %d", rec.Code)
	}
	for _, path := range []string{"/databases/abc/migrations", "/databases/0/migrations", "/databases/5/migrations/abc", "/databases/5/migrations/0", "/databases/5/migrations/-1"} {
		if rec := httpGet(h, path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, mau 404", path, rec.Code)
		}
	}
}

func TestMigrationHandler_EditEditorPrefilledOnlyForDrafts(t *testing.T) {
	svc := &fakeMigrationService{get: sampleMigration(2, domain.MigrationDraft)}
	body := httpGet(editorServer(t, svc), "/databases/5/migrations/2/edit").Body.String()
	for _, want := range []string{`action="/databases/5/migrations/2"`, `value="tambah &lt;tabel&gt;"`, "CREATE TABLE a (id int);", "Ubah draft versi 2"} {
		if !strings.Contains(body, want) {
			t.Errorf("editor tidak memuat %q", want)
		}
	}

	svc = &fakeMigrationService{get: sampleMigration(2, domain.MigrationSuccess)}
	rec := httpGet(editorServer(t, svc), "/databases/5/migrations/2/edit")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/databases/5/migrations/2" {
		t.Fatalf("status = %d, location = %q", rec.Code, rec.Header().Get("Location"))
	}
	if fl := flashOf(t, rec); fl == nil || fl.Kind != httpx.FlashError {
		t.Errorf("flash = %+v", fl)
	}
}

func TestMigrationHandler_Update(t *testing.T) {
	svc := &fakeMigrationService{}
	rec := httpPost(editorServer(t, svc), "/databases/5/migrations/2", url.Values{"name": {"baru"}, "sql": {"SELECT 2"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/databases/5/migrations/2" {
		t.Fatalf("status = %d, location = %q", rec.Code, rec.Header().Get("Location"))
	}
	if svc.updatedVersion != 2 || svc.updatedIn.Name != "baru" || svc.updatedIn.SQL != "SELECT 2" {
		t.Errorf("update: versi = %d, input = %+v", svc.updatedVersion, svc.updatedIn)
	}

	svc = &fakeMigrationService{executeResult: sampleMigration(2, domain.MigrationSuccess)}
	rec = httpPost(editorServer(t, svc), "/databases/5/migrations/2", url.Values{"name": {"baru"}, "sql": {"SELECT 2"}, "action": {"execute"}})
	if svc.executeCalls != 1 || svc.executedVersion != 2 {
		t.Errorf("simpan lalu jalankan: Execute dipanggil %d kali untuk versi %d", svc.executeCalls, svc.executedVersion)
	}
}

func TestMigrationHandler_UpdateRejections(t *testing.T) {
	svc := &fakeMigrationService{updateErr: &domain.ValidationError{Fields: map[string]string{"sql": "SQL tidak boleh kosong."}}}
	rec := httpPost(editorServer(t, svc), "/databases/5/migrations/2", url.Values{"name": {"x"}, "sql": {" "}})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "SQL tidak boleh kosong.") || !strings.Contains(rec.Body.String(), `action="/databases/5/migrations/2"`) {
		t.Errorf("validasi: status = %d", rec.Code)
	}

	svc = &fakeMigrationService{updateErr: domain.ErrMigrationNotDraft}
	rec = httpPost(editorServer(t, svc), "/databases/5/migrations/2", url.Values{"name": {"x"}, "sql": {"SELECT 1"}})
	if rec.Code != http.StatusSeeOther || flashOf(t, rec) == nil {
		t.Errorf("bukan draft: status = %d", rec.Code)
	}

	svc = &fakeMigrationService{updateErr: domain.ErrNotFound}
	if rec := httpPost(editorServer(t, svc), "/databases/5/migrations/2", url.Values{"name": {"x"}, "sql": {"SELECT 1"}}); rec.Code != http.StatusNotFound {
		t.Errorf("tak ada: status = %d", rec.Code)
	}
}

func TestMigrationHandler_ExecuteOutcomes(t *testing.T) {
	tests := []struct {
		name     string
		svc      *fakeMigrationService
		wantCode int
		wantKind string
		wantText string
	}{
		{"berhasil", &fakeMigrationService{executeResult: sampleMigration(2, domain.MigrationSuccess)}, 303, httpx.FlashSuccess, "berhasil dijalankan"},
		{"gagal", &fakeMigrationService{executeResult: sampleMigration(2, domain.MigrationFailed)}, 303, httpx.FlashError, "seluruh perubahan dibatalkan"},
		{"tidak berurutan", &fakeMigrationService{executeErr: &migration.OutOfOrderError{Blocking: 1}}, 303, httpx.FlashError, "versi 1 lebih dulu"},
		{"sudah dieksekusi", &fakeMigrationService{executeErr: domain.ErrMigrationNotDraft}, 303, httpx.FlashError, "berstatus draft"},
		{"tak ada", &fakeMigrationService{executeErr: domain.ErrNotFound}, 404, "", ""},
		{"error tak terduga", &fakeMigrationService{executeErr: errors.New("koneksi ke 10.0.0.5 password=hunter2")}, 500, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httpPost(editorServer(t, tt.svc), "/databases/5/migrations/2/execute", nil)
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, mau %d", rec.Code, tt.wantCode)
			}
			if tt.wantCode == 303 {
				if rec.Header().Get("Location") != "/databases/5/migrations/2" {
					t.Errorf("location = %q", rec.Header().Get("Location"))
				}
				if fl := flashOf(t, rec); fl == nil || fl.Kind != tt.wantKind || !strings.Contains(fl.Message, tt.wantText) {
					t.Errorf("flash = %+v, mau %s memuat %q", fl, tt.wantKind, tt.wantText)
				}
			}
			if strings.Contains(rec.Body.String(), "hunter2") || strings.Contains(rec.Body.String(), "10.0.0.5") {
				t.Error("detail error tidak boleh bocor")
			}
		})
	}
}

func TestMigrationHandler_Cancel(t *testing.T) {
	svc := &fakeMigrationService{}
	rec := httpPost(editorServer(t, svc), "/databases/5/migrations/2/cancel", nil)
	if rec.Code != http.StatusSeeOther || svc.cancelledVersion != 2 {
		t.Errorf("status = %d, versi = %d", rec.Code, svc.cancelledVersion)
	}
	if fl := flashOf(t, rec); fl == nil || fl.Kind != httpx.FlashSuccess {
		t.Errorf("flash = %+v", fl)
	}

	svc = &fakeMigrationService{cancelErr: domain.ErrMigrationNotDraft}
	rec = httpPost(editorServer(t, svc), "/databases/5/migrations/2/cancel", nil)
	if fl := flashOf(t, rec); rec.Code != http.StatusSeeOther || fl == nil || fl.Kind != httpx.FlashError {
		t.Errorf("bukan draft: status = %d, flash = %+v", rec.Code, fl)
	}
}

func TestMigrationHandler_ViewOnlyCannotWriteEvenIfRoutesReached(t *testing.T) {
	svc := &fakeMigrationService{get: sampleMigration(2, domain.MigrationDraft), createResult: sampleMigration(3, domain.MigrationDraft)}
	h := readerServer(t, svc)
	form := url.Values{"name": {"x"}, "sql": {"DROP TABLE a"}, "action": {"execute"}}

	for _, path := range []string{
		"/databases/5/migrations", "/databases/5/migrations/2", "/databases/5/migrations/2/execute", "/databases/5/migrations/2/cancel",
	} {
		if rec := httpPost(h, path, form); rec.Code != http.StatusForbidden {
			t.Errorf("POST %s: status = %d, mau 403", path, rec.Code)
		}
	}
	if svc.writes != 0 {
		t.Errorf("tidak boleh ada penulisan, dapat %d", svc.writes)
	}
}

func TestMigrationHandler_WriteRoutesRejectGET(t *testing.T) {
	h := editorServer(t, &fakeMigrationService{get: sampleMigration(2, domain.MigrationDraft)})
	for _, path := range []string{"/databases/5/migrations/2/execute", "/databases/5/migrations/2/cancel"} {
		if rec := httpGet(h, path); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: status = %d, mau 405 (aksi tulis tidak boleh lewat GET)", path, rec.Code)
		}
	}
}

func TestMigrationHandler_ForbiddenFromServiceBecomes403(t *testing.T) {
	svc := &fakeMigrationService{listErr: domain.ErrForbidden, getErr: domain.ErrForbidden}
	h := editorServer(t, svc)
	if rec := httpGet(h, "/databases/5/migrations"); rec.Code != http.StatusForbidden {
		t.Errorf("riwayat: status = %d", rec.Code)
	}
	if rec := httpGet(h, "/databases/5/migrations/2"); rec.Code != http.StatusForbidden {
		t.Errorf("detail: status = %d", rec.Code)
	}
}

func TestMigrationHandler_AccessLookupFailureIs500WithoutDetail(t *testing.T) {
	access := fakeAccess{level: domain.AccessViewEdit, err: errors.New("database platform: password=hunter2")}
	h := newMigrationServer(t, &fakeMigrationService{}, editorID, access)

	rec := httpGet(h, "/databases/5/migrations")
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "hunter2") {
		t.Errorf("status = %d, atau detail bocor", rec.Code)
	}
}
