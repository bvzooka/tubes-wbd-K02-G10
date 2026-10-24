package schemadiff_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"dbplatform/internal/authstub"
	"dbplatform/internal/domain"
	"dbplatform/internal/httpx"
	"dbplatform/internal/schemadiff"
	webassets "dbplatform/web"
)

const (
	hEditorID int64 = 1
	hReaderID int64 = 2
	hDBID     int64 = 5
	hSourceID int64 = 7
)

type hAccess struct{}

func (hAccess) EffectiveLevel(context.Context, int64, int64) (domain.AccessLevel, error) {
	return domain.AccessViewEdit, nil
}

func (hAccess) Can(context.Context, int64, int64, domain.AccessLevel) (bool, error) { return true, nil }

func (hAccess) ListAccessible(context.Context, int64) ([]domain.AccessibleDatabase, error) {
	return []domain.AccessibleDatabase{{ConnectionID: hDBID, Name: "Tujuan <db>", DBMS: domain.DBMSPostgres, Level: domain.AccessViewEdit}}, nil
}

type hService struct {
	sources    []domain.AccessibleDatabase
	sourcesErr error
	preview    *schemadiff.Result
	previewErr error
	draft      domain.Migration
	draftErr   error

	previewCalls int
	previewArgs  [2]int64
	draftCalls   int
	draftArgs    [2]int64
	draftIDs     []string
}

func (s *hService) Sources(_ context.Context, actor domain.Principal, _ int64) ([]domain.AccessibleDatabase, error) {
	if actor.UserID == hReaderID {
		return nil, domain.ErrForbidden
	}
	return s.sources, s.sourcesErr
}

func (s *hService) Preview(_ context.Context, actor domain.Principal, targetID, sourceID int64) (*schemadiff.Result, error) {
	s.previewCalls++
	s.previewArgs = [2]int64{targetID, sourceID}
	if actor.UserID == hReaderID {
		return nil, domain.ErrForbidden
	}
	return s.preview, s.previewErr
}

func (s *hService) CreateDraft(_ context.Context, actor domain.Principal, targetID, sourceID int64, selected []string) (domain.Migration, error) {
	s.draftCalls++
	s.draftArgs = [2]int64{targetID, sourceID}
	s.draftIDs = selected
	if actor.UserID == hReaderID {
		return domain.Migration{}, domain.ErrForbidden
	}
	return s.draft, s.draftErr
}

func hChanges() []schemadiff.Change {
	return []schemadiff.Change{
		{ID: "aaaaaaaaaaaa", Kind: schemadiff.KindCreateTable, Object: "public.<t>", SQL: "CREATE TABLE \"public\".\"t\" (\n  \"id\" integer NOT NULL\n);"},
		{ID: "bbbbbbbbbbbb", Kind: schemadiff.KindAddColumn, Object: "public.t.note", SQL: `ALTER TABLE "public"."t" ADD COLUMN "note" text;`},
		{ID: "cccccccccccc", Kind: schemadiff.KindDropColumn, Object: "public.t.legacy", SQL: `ALTER TABLE "public"."t" DROP COLUMN "legacy";`, Destructive: true},
	}
}

func hService5() *hService {
	return &hService{
		sources: []domain.AccessibleDatabase{
			{ConnectionID: hSourceID, Name: "Sumber <1>", DBMS: domain.DBMSPostgres, Level: domain.AccessViewOnly},
			{ConnectionID: 8, Name: "Sumber dua", DBMS: domain.DBMSPostgres, Level: domain.AccessViewEdit},
		},
		preview: &schemadiff.Result{TargetID: hDBID, TargetName: "Tujuan <db>", SourceID: hSourceID, SourceName: "Sumber <1>", Changes: hChanges()},
		draft:   domain.Migration{ConnectionID: hDBID, Version: 3},
	}
}

func hServer(t *testing.T, svc *hService, actorID int64) http.Handler {
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
		Authz:     hAccess{},
	}
	mux := http.NewServeMux()
	schemadiff.NewHandler(d, svc).Register(mux)
	return mux
}

func hGet(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func hPost(h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func hFlash(t *testing.T, rec *httptest.ResponseRecorder) *httpx.Flash {
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

func previewForm() url.Values {
	return url.Values{"source_id": {fmt.Sprint(hSourceID)}, "action": {"preview"}}
}

func checkedState(body, id string) (present, checked bool) {
	needle := fmt.Sprintf(`value="%s"`, id)
	i := strings.Index(body, needle)
	if i < 0 {
		return false, false
	}
	end := strings.Index(body[i:], ">")
	return true, strings.Contains(body[i:i+end], "checked")
}

func TestSchemaDiffHandler_PickPage(t *testing.T) {
	rec := hGet(hServer(t, hService5(), hEditorID), "/databases/5/schema-diff")

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, want := range []string{
		`action="/databases/5/schema-diff"`, `name="csrf_token"`, `name="source_id"`, `name="action" value="preview"`,
		`<option value="7">Sumber &lt;1&gt;</option>`, `<option value="8">Sumber dua</option>`, "Tujuan &lt;db&gt;",
		`href="/databases/5/migrations"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("halaman tidak memuat %q", want)
		}
	}
	if strings.Contains(body, "Sumber <1>") || strings.Contains(body, `name="change"`) {
		t.Error("nama harus di-escape dan belum ada daftar perubahan")
	}
}

func TestSchemaDiffHandler_PickPageWithoutSources(t *testing.T) {
	svc := hService5()
	svc.sources = nil
	body := hGet(hServer(t, svc, hEditorID), "/databases/5/schema-diff").Body.String()

	if !strings.Contains(body, "Tidak ada database PostgreSQL lain") || strings.Contains(body, `name="source_id"`) {
		t.Error("tanpa sumber, form pemilihan tidak boleh tampil")
	}
}

func TestSchemaDiffHandler_PickPageForbiddenAndInvalidIDs(t *testing.T) {
	if rec := hGet(hServer(t, hService5(), hReaderID), "/databases/5/schema-diff"); rec.Code != http.StatusForbidden {
		t.Errorf("tanpa hak edit: status = %d", rec.Code)
	}
	h := hServer(t, hService5(), hEditorID)
	for _, path := range []string{"/databases/abc/schema-diff", "/databases/0/schema-diff"} {
		if rec := hGet(h, path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, mau 404", path, rec.Code)
		}
	}
}

func TestSchemaDiffHandler_PreviewShowsChangesWithSafeDefaults(t *testing.T) {
	svc := hService5()
	rec := hPost(hServer(t, svc, hEditorID), "/databases/5/schema-diff", previewForm())

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if svc.previewCalls != 1 || svc.previewArgs != [2]int64{hDBID, hSourceID} {
		t.Errorf("Preview dipanggil %d kali dengan %v", svc.previewCalls, svc.previewArgs)
	}
	for _, want := range []string{
		"3 perubahan dari Sumber &lt;1&gt;", "Buat tabel", "Tambah kolom", "Hapus kolom",
		"public.&lt;t&gt;", `ADD COLUMN &#34;note&#34; text;`, `name="source_id" value="7"`, `name="action" value="create"`,
		"1 perubahan dapat menghapus data", "Menghapus data",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("pratinjau tidak memuat %q", want)
		}
	}
	for id, wantChecked := range map[string]bool{"aaaaaaaaaaaa": true, "bbbbbbbbbbbb": true, "cccccccccccc": false} {
		present, checked := checkedState(body, id)
		if !present || checked != wantChecked {
			t.Errorf("checkbox %s: ada = %v, tercentang = %v, mau tercentang = %v", id, present, checked, wantChecked)
		}
	}
	if strings.Contains(body, "public.<t>") {
		t.Error("objek harus di-escape")
	}
}

func TestSchemaDiffHandler_PreviewWithoutDifferences(t *testing.T) {
	svc := hService5()
	svc.preview.Changes = nil
	body := hPost(hServer(t, svc, hEditorID), "/databases/5/schema-diff", previewForm()).Body.String()

	if !strings.Contains(body, "Tidak ada perbedaan") || strings.Contains(body, `value="create"`) {
		t.Error("tanpa perbedaan, tombol buat draft tidak boleh tampil")
	}
}

func TestSchemaDiffHandler_PreviewWithoutDestructiveChangesHasNoWarning(t *testing.T) {
	svc := hService5()
	svc.preview.Changes = hChanges()[:2]
	body := hPost(hServer(t, svc, hEditorID), "/databases/5/schema-diff", previewForm()).Body.String()
	if strings.Contains(body, "dapat menghapus data") {
		t.Error("tanpa perubahan destruktif, peringatan tidak boleh tampil")
	}
}

func TestSchemaDiffHandler_PreviewRejections(t *testing.T) {
	for _, bad := range []string{"", "abc", "0", "-3"} {
		svc := hService5()
		rec := hPost(hServer(t, svc, hEditorID), "/databases/5/schema-diff", url.Values{"source_id": {bad}})
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Pilih database sumber.") {
			t.Errorf("source_id=%q: status = %d", bad, rec.Code)
		}
		if svc.previewCalls != 0 {
			t.Errorf("source_id=%q: service tidak boleh dipanggil", bad)
		}
	}

	tests := []struct {
		name     string
		err      error
		wantCode int
		wantText string
	}{
		{"sumber sama dengan target", &domain.ValidationError{Fields: map[string]string{"source_id": "Pilih database sumber yang berbeda dari database tujuan."}}, 422, "yang berbeda dari database tujuan"},
		{"bukan PostgreSQL", fmt.Errorf("%w: schema diff hanya mendukung PostgreSQL", domain.ErrUnsupported), 422, "hanya mendukung database PostgreSQL"},
		{"tanpa akses ke sumber", domain.ErrForbidden, 403, ""},
		{"koneksi gagal", domain.ErrConnectionFailed, 502, ""},
		{"error tak terduga", errors.New("koneksi ke 10.0.0.5 password=hunter2"), 500, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := hService5()
			svc.previewErr = tt.err
			rec := hPost(hServer(t, svc, hEditorID), "/databases/5/schema-diff", previewForm())
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, mau %d", rec.Code, tt.wantCode)
			}
			if tt.wantText != "" && !strings.Contains(rec.Body.String(), tt.wantText) {
				t.Errorf("body tidak memuat %q", tt.wantText)
			}
			if strings.Contains(rec.Body.String(), "hunter2") || strings.Contains(rec.Body.String(), "10.0.0.5") {
				t.Error("detail error tidak boleh bocor")
			}
		})
	}
}

func TestSchemaDiffHandler_CreateRedirectsToEditorForReview(t *testing.T) {
	svc := hService5()
	form := url.Values{"source_id": {fmt.Sprint(hSourceID)}, "action": {"create"}, "change": {"aaaaaaaaaaaa", "bbbbbbbbbbbb"}}
	rec := hPost(hServer(t, svc, hEditorID), "/databases/5/schema-diff", form)

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/databases/5/migrations/3/edit" {
		t.Fatalf("status = %d, location = %q", rec.Code, rec.Header().Get("Location"))
	}
	if svc.draftArgs != [2]int64{hDBID, hSourceID} || len(svc.draftIDs) != 2 || svc.draftIDs[0] != "aaaaaaaaaaaa" || svc.draftIDs[1] != "bbbbbbbbbbbb" {
		t.Errorf("CreateDraft: args = %v, ids = %v", svc.draftArgs, svc.draftIDs)
	}
	if fl := hFlash(t, rec); fl == nil || fl.Kind != httpx.FlashSuccess || !strings.Contains(fl.Message, "versi 3") || !strings.Contains(fl.Message, "Tinjau") {
		t.Errorf("flash = %+v", fl)
	}
}

func TestSchemaDiffHandler_CreateFailuresRerenderWithSelectionKept(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		wanted string
	}{
		{"tidak ada yang dipilih", &domain.ValidationError{Fields: map[string]string{"changes": "Pilih minimal satu perubahan."}}, "Pilih minimal satu perubahan."},
		{"pilihan kedaluwarsa", &domain.ValidationError{Fields: map[string]string{"changes": "Perubahan yang dipilih sudah tidak berlaku karena schema berubah. Hitung ulang perbedaannya."}}, "sudah tidak berlaku"},
		{"validasi migration", &domain.ValidationError{Fields: map[string]string{"sql": "SQL maksimal 256 KB."}}, "SQL maksimal 256 KB."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := hService5()
			svc.draftErr = tt.err
			form := url.Values{"source_id": {fmt.Sprint(hSourceID)}, "action": {"create"}, "change": {"cccccccccccc"}}
			rec := hPost(hServer(t, svc, hEditorID), "/databases/5/schema-diff", form)

			body := rec.Body.String()
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, mau 422", rec.Code)
			}
			if !strings.Contains(body, tt.wanted) {
				t.Errorf("pesan %q tidak tampil", tt.wanted)
			}
			for id, wantChecked := range map[string]bool{"aaaaaaaaaaaa": false, "bbbbbbbbbbbb": false, "cccccccccccc": true} {
				present, checked := checkedState(body, id)
				if !present || checked != wantChecked {
					t.Errorf("checkbox %s: ada = %v, tercentang = %v, mau %v (pilihan pengguna harus dipertahankan)", id, present, checked, wantChecked)
				}
			}
			if !strings.Contains(body, `value="create"`) {
				t.Error("daftar perubahan harus tampil lagi")
			}
		})
	}
}

func TestSchemaDiffHandler_CreateOtherFailures(t *testing.T) {
	form := url.Values{"source_id": {fmt.Sprint(hSourceID)}, "action": {"create"}, "change": {"aaaaaaaaaaaa"}}

	if rec := hPost(hServer(t, hService5(), hReaderID), "/databases/5/schema-diff", form); rec.Code != http.StatusForbidden {
		t.Errorf("tanpa hak: status = %d, mau 403", rec.Code)
	}

	svc := hService5()
	svc.draftErr = &domain.ValidationError{Fields: map[string]string{"source_id": "Pilih database sumber yang berbeda dari database tujuan."}}
	rec := hPost(hServer(t, svc, hEditorID), "/databases/5/schema-diff", form)
	if rec.Code != http.StatusUnprocessableEntity || strings.Contains(rec.Body.String(), `value="create"`) {
		t.Errorf("error sumber harus kembali ke halaman pilih: status = %d", rec.Code)
	}

	svc = hService5()
	svc.draftErr = errors.New("koneksi ke 10.0.0.5 password=hunter2")
	rec = hPost(hServer(t, svc, hEditorID), "/databases/5/schema-diff", form)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "hunter2") {
		t.Errorf("error tak terduga: status = %d atau detail bocor", rec.Code)
	}
}

func TestSchemaDiffHandler_NoSelectionIsForwardedToServiceForValidation(t *testing.T) {
	svc := hService5()
	svc.draftErr = &domain.ValidationError{Fields: map[string]string{"changes": "Pilih minimal satu perubahan."}}
	rec := hPost(hServer(t, svc, hEditorID), "/databases/5/schema-diff", url.Values{"source_id": {fmt.Sprint(hSourceID)}, "action": {"create"}})

	if rec.Code != http.StatusUnprocessableEntity || svc.draftCalls != 1 || len(svc.draftIDs) != 0 {
		t.Errorf("status = %d, panggilan = %d, ids = %v", rec.Code, svc.draftCalls, svc.draftIDs)
	}
}
