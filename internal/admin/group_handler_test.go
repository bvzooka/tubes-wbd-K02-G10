package admin_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"dbplatform/internal/admin"
	"dbplatform/internal/authstub"
	"dbplatform/internal/domain"
	"dbplatform/internal/httpx"
	webassets "dbplatform/web"
)

type grantRecord struct {
	groupID, connID int64
	level           domain.AccessLevel
}

type fakeGroupService struct {
	groups    []admin.GroupRow
	detail    admin.GroupDetail
	detailErr error
	createErr error
	updateErr error
	deleteErr error
	addErr    error
	removeErr error
	grantErr  error
	revokeErr error

	createdIn  admin.GroupInput
	updatedIn  admin.GroupInput
	updatedID  int64
	deletedID  int64
	added      [2]int64
	addCalls   int
	removed    [2]int64
	grant      grantRecord
	grantCalls int
	revoked    [2]int64
}

func forbid(actor domain.Principal) error {
	if !actor.IsPlatformAdmin {
		return domain.ErrForbidden
	}
	return nil
}

func (f *fakeGroupService) CreateGroup(_ context.Context, actor domain.Principal, in admin.GroupInput) (domain.Group, error) {
	f.createdIn = in
	if err := forbid(actor); err != nil {
		return domain.Group{}, err
	}
	return domain.Group{ID: 9, Name: strings.TrimSpace(in.Name)}, f.createErr
}

func (f *fakeGroupService) ListGroups(_ context.Context, actor domain.Principal) ([]admin.GroupRow, error) {
	return f.groups, forbid(actor)
}

func (f *fakeGroupService) GroupDetail(_ context.Context, actor domain.Principal, _ int64) (admin.GroupDetail, error) {
	if err := forbid(actor); err != nil {
		return admin.GroupDetail{}, err
	}
	return f.detail, f.detailErr
}

func (f *fakeGroupService) UpdateGroup(_ context.Context, actor domain.Principal, id int64, in admin.GroupInput) (domain.Group, error) {
	f.updatedID, f.updatedIn = id, in
	if err := forbid(actor); err != nil {
		return domain.Group{}, err
	}
	return domain.Group{ID: id, Name: in.Name}, f.updateErr
}

func (f *fakeGroupService) DeleteGroup(_ context.Context, actor domain.Principal, id int64) error {
	f.deletedID = id
	if err := forbid(actor); err != nil {
		return err
	}
	return f.deleteErr
}

func (f *fakeGroupService) AddMember(_ context.Context, actor domain.Principal, groupID, userID int64) error {
	f.addCalls++
	f.added = [2]int64{groupID, userID}
	if err := forbid(actor); err != nil {
		return err
	}
	return f.addErr
}

func (f *fakeGroupService) RemoveMember(_ context.Context, actor domain.Principal, groupID, userID int64) error {
	f.removed = [2]int64{groupID, userID}
	if err := forbid(actor); err != nil {
		return err
	}
	return f.removeErr
}

func (f *fakeGroupService) GrantAccess(_ context.Context, actor domain.Principal, groupID, connID int64, level domain.AccessLevel) error {
	f.grantCalls++
	f.grant = grantRecord{groupID, connID, level}
	if err := forbid(actor); err != nil {
		return err
	}
	return f.grantErr
}

func (f *fakeGroupService) RevokeAccess(_ context.Context, actor domain.Principal, groupID, connID int64) error {
	f.revoked = [2]int64{groupID, connID}
	if err := forbid(actor); err != nil {
		return err
	}
	return f.revokeErr
}

func newGroupServer(t *testing.T, svc *fakeGroupService, actor domain.Principal) http.Handler {
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
		Auth:      &authstub.Middleware{Principal: actor},
	}
	mux := http.NewServeMux()
	admin.NewGroupHandler(d, svc).Register(mux)
	return mux
}

func sampleDetail() admin.GroupDetail {
	return admin.GroupDetail{
		Group: domain.Group{ID: 5, Name: "Tim <Data>", Description: "analis"},
		Members: []admin.GroupMember{
			{UserID: 11, Username: "budi", IsActive: true},
			{UserID: 12, Username: "ani", IsActive: false},
		},
		Accesses: []admin.GroupAccess{
			{ConnectionID: 3, ConnectionName: "sample", Level: domain.AccessViewEdit},
			{ConnectionID: 4, ConnectionName: "hr", Level: domain.AccessViewOnly},
		},
		AvailableUsers:       []admin.UserOption{{ID: 13, Username: "citra"}},
		AvailableConnections: []admin.ConnOption{{ID: 3, Name: "sample"}, {ID: 4, Name: "hr"}},
	}
}

func TestGroupHandler_List(t *testing.T) {
	svc := &fakeGroupService{groups: []admin.GroupRow{
		{ID: 1, Name: "Tim <A>", Description: "deskripsi", MemberCount: 3, AccessCount: 2, CreatedAt: time.Date(2026, 10, 20, 9, 30, 0, 0, time.UTC)},
		{ID: 2, Name: "Kosong", CreatedAt: time.Now()},
	}}
	rec := get(newGroupServer(t, svc, root), "/admin/groups")

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, want := range []string{`href="/admin/groups/1"`, `href="/admin/groups/2"`, "Tim &lt;A&gt;", "20 Oct 2026 09:30", `href="/admin/groups/new"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body tidak memuat %q", want)
		}
	}
	if strings.Contains(body, "Tim <A>") {
		t.Error("nama group harus di-escape")
	}
}

func TestGroupHandler_ListEmpty(t *testing.T) {
	rec := get(newGroupServer(t, &fakeGroupService{}, root), "/admin/groups")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Belum ada group") {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestGroupHandler_NewForm(t *testing.T) {
	rec := get(newGroupServer(t, &fakeGroupService{}, root), "/admin/groups/new")
	body := rec.Body.String()
	for _, want := range []string{`action="/admin/groups"`, `name="csrf_token"`, `name="name"`, `name="description"`} {
		if !strings.Contains(body, want) {
			t.Errorf("form tidak memuat %q", want)
		}
	}
}

func TestGroupHandler_CreateSuccessRedirectsToDetail(t *testing.T) {
	svc := &fakeGroupService{}
	rec := postForm(newGroupServer(t, svc, root), "/admin/groups", url.Values{"name": {" Tim Data "}, "description": {"analis"}})

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/groups/9" {
		t.Fatalf("status = %d, location = %q", rec.Code, rec.Header().Get("Location"))
	}
	if svc.createdIn.Name != " Tim Data " || svc.createdIn.Description != "analis" {
		t.Errorf("input ke service = %+v", svc.createdIn)
	}
	if fl := flashFrom(t, rec); fl == nil || fl.Kind != httpx.FlashSuccess || !strings.Contains(fl.Message, "Tim Data") {
		t.Errorf("flash = %+v", fl)
	}
}

func TestGroupHandler_CreateValidationErrorKeepsValues(t *testing.T) {
	svc := &fakeGroupService{createErr: &domain.ValidationError{Fields: map[string]string{"name": "Nama group sudah dipakai."}}}
	rec := postForm(newGroupServer(t, svc, root), "/admin/groups", url.Values{"name": {"Tim"}, "description": {"isi deskripsi"}})

	body := rec.Body.String()
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, mau 422", rec.Code)
	}
	for _, want := range []string{"Nama group sudah dipakai.", `value="Tim"`, "isi deskripsi"} {
		if !strings.Contains(body, want) {
			t.Errorf("body tidak memuat %q", want)
		}
	}
}

func TestGroupHandler_DetailRenders(t *testing.T) {
	svc := &fakeGroupService{detail: sampleDetail()}
	rec := get(newGroupServer(t, svc, root), "/admin/groups/5")

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, want := range []string{
		`action="/admin/groups/5"`, `action="/admin/groups/5/members"`, `action="/admin/groups/5/access"`,
		`action="/admin/groups/5/members/11/delete"`, `action="/admin/groups/5/members/12/delete"`,
		`action="/admin/groups/5/access/3/delete"`, `action="/admin/groups/5/access/4/delete"`,
		`<option value="13">citra</option>`, `<option value="3">sample</option>`, `<option value="4">hr</option>`,
		`value="view_only"`, `value="view_edit"`, "View + Edit", "View Only", "Nonaktif",
		"Tim &lt;Data&gt;", `value="Tim &lt;Data&gt;"`, "analis", `name="csrf_token"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail tidak memuat %q", want)
		}
	}
	if strings.Contains(body, "Tim <Data>") {
		t.Error("nama group harus di-escape")
	}
}

func TestGroupHandler_DetailDeleteButtonFollowsCanDelete(t *testing.T) {
	deletable := sampleDetail()
	deletable.Members, deletable.Accesses, deletable.CanDelete = nil, nil, true
	body := get(newGroupServer(t, &fakeGroupService{detail: deletable}, root), "/admin/groups/5").Body.String()
	if !strings.Contains(body, `action="/admin/groups/5/delete"`) {
		t.Error("group kosong harus punya tombol hapus")
	}

	body = get(newGroupServer(t, &fakeGroupService{detail: sampleDetail()}, root), "/admin/groups/5").Body.String()
	if strings.Contains(body, `action="/admin/groups/5/delete"`) {
		t.Error("group berisi tidak boleh punya tombol hapus")
	}
	if !strings.Contains(body, "Kosongkan dulu") {
		t.Error("alasan tidak bisa menghapus harus ditampilkan")
	}
}

func TestGroupHandler_DetailHidesFormsWithoutOptions(t *testing.T) {
	d := sampleDetail()
	d.AvailableUsers, d.AvailableConnections = nil, nil
	body := get(newGroupServer(t, &fakeGroupService{detail: d}, root), "/admin/groups/5").Body.String()

	for _, bad := range []string{`name="user_id"`, `name="connection_id"`} {
		if strings.Contains(body, bad) {
			t.Errorf("form %s tidak boleh tampil tanpa pilihan", bad)
		}
	}
}

func TestGroupHandler_DetailNotFoundAndInvalidID(t *testing.T) {
	h := newGroupServer(t, &fakeGroupService{detailErr: domain.ErrNotFound}, root)
	if rec := get(h, "/admin/groups/99"); rec.Code != http.StatusNotFound {
		t.Errorf("group tak ada: status = %d", rec.Code)
	}
	for _, path := range []string{"/admin/groups/abc", "/admin/groups/0", "/admin/groups/-3"} {
		if rec := get(h, path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, mau 404", path, rec.Code)
		}
	}
}

func TestGroupHandler_UpdateSuccess(t *testing.T) {
	svc := &fakeGroupService{detail: sampleDetail()}
	rec := postForm(newGroupServer(t, svc, root), "/admin/groups/5", url.Values{"name": {"Nama Baru"}, "description": {"d"}})

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/groups/5" {
		t.Fatalf("status = %d, location = %q", rec.Code, rec.Header().Get("Location"))
	}
	if svc.updatedID != 5 || svc.updatedIn.Name != "Nama Baru" {
		t.Errorf("update: id = %d, input = %+v", svc.updatedID, svc.updatedIn)
	}
}

func TestGroupHandler_UpdateValidationErrorShowsTypedValues(t *testing.T) {
	svc := &fakeGroupService{
		detail:    sampleDetail(),
		updateErr: &domain.ValidationError{Fields: map[string]string{"name": "Nama group harus 1 sampai 64 karakter."}},
	}
	rec := postForm(newGroupServer(t, svc, root), "/admin/groups/5", url.Values{"name": {""}, "description": {"diketik user"}})

	body := rec.Body.String()
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, mau 422", rec.Code)
	}
	for _, want := range []string{"Nama group harus 1 sampai 64 karakter.", "diketik user", `action="/admin/groups/5/members"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body tidak memuat %q", want)
		}
	}
}

func TestGroupHandler_Delete(t *testing.T) {
	svc := &fakeGroupService{}
	rec := postForm(newGroupServer(t, svc, root), "/admin/groups/5/delete", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/groups" || svc.deletedID != 5 {
		t.Errorf("status = %d, location = %q, id = %d", rec.Code, rec.Header().Get("Location"), svc.deletedID)
	}

	svc = &fakeGroupService{deleteErr: admin.ErrGroupNotEmpty}
	rec = postForm(newGroupServer(t, svc, root), "/admin/groups/5/delete", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/groups/5" {
		t.Fatalf("ditolak: status = %d, location = %q", rec.Code, rec.Header().Get("Location"))
	}
	if fl := flashFrom(t, rec); fl == nil || fl.Kind != httpx.FlashError || !strings.Contains(fl.Message, "Kosongkan") {
		t.Errorf("flash = %+v", fl)
	}

	svc = &fakeGroupService{deleteErr: domain.ErrNotFound}
	if rec := postForm(newGroupServer(t, svc, root), "/admin/groups/5/delete", nil); rec.Code != http.StatusNotFound {
		t.Errorf("group tak ada: status = %d", rec.Code)
	}
}

func TestGroupHandler_AddMember(t *testing.T) {
	svc := &fakeGroupService{detail: sampleDetail()}
	rec := postForm(newGroupServer(t, svc, root), "/admin/groups/5/members", url.Values{"user_id": {"13"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/groups/5" || svc.added != [2]int64{5, 13} {
		t.Errorf("status = %d, location = %q, added = %v", rec.Code, rec.Header().Get("Location"), svc.added)
	}
}

func TestGroupHandler_AddMemberRejections(t *testing.T) {
	for _, bad := range []string{"", "abc", "0", "-1"} {
		svc := &fakeGroupService{detail: sampleDetail()}
		rec := postForm(newGroupServer(t, svc, root), "/admin/groups/5/members", url.Values{"user_id": {bad}})
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Pilih user yang valid.") {
			t.Errorf("user_id=%q: status = %d", bad, rec.Code)
		}
		if svc.addCalls != 0 {
			t.Errorf("user_id=%q: service tidak boleh dipanggil", bad)
		}
	}

	svc := &fakeGroupService{
		detail: sampleDetail(),
		addErr: &domain.ValidationError{Fields: map[string]string{"user_id": "Pilih user yang valid."}},
	}
	rec := postForm(newGroupServer(t, svc, root), "/admin/groups/5/members", url.Values{"user_id": {"424242"}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("error validasi service: status = %d", rec.Code)
	}

	svc = &fakeGroupService{addErr: domain.ErrNotFound}
	if rec := postForm(newGroupServer(t, svc, root), "/admin/groups/99/members", url.Values{"user_id": {"13"}}); rec.Code != http.StatusNotFound {
		t.Errorf("group tak ada: status = %d", rec.Code)
	}
}

func TestGroupHandler_RemoveMember(t *testing.T) {
	svc := &fakeGroupService{}
	h := newGroupServer(t, svc, root)

	rec := postForm(h, "/admin/groups/5/members/11/delete", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/groups/5" || svc.removed != [2]int64{5, 11} {
		t.Errorf("status = %d, removed = %v", rec.Code, svc.removed)
	}
	if rec := postForm(h, "/admin/groups/5/members/abc/delete", nil); rec.Code != http.StatusNotFound {
		t.Errorf("id user tidak valid: status = %d", rec.Code)
	}

	svc.removeErr = domain.ErrNotFound
	if rec := postForm(h, "/admin/groups/5/members/11/delete", nil); rec.Code != http.StatusNotFound {
		t.Errorf("bukan anggota: status = %d", rec.Code)
	}
}

func TestGroupHandler_GrantAccess(t *testing.T) {
	svc := &fakeGroupService{detail: sampleDetail()}
	rec := postForm(newGroupServer(t, svc, root), "/admin/groups/5/access", url.Values{
		"connection_id": {"3"}, "access_level": {"view_edit"},
	})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/groups/5" {
		t.Fatalf("status = %d, location = %q", rec.Code, rec.Header().Get("Location"))
	}
	if svc.grant != (grantRecord{5, 3, domain.AccessViewEdit}) {
		t.Errorf("grant = %+v", svc.grant)
	}
}

func TestGroupHandler_GrantAccessRejections(t *testing.T) {
	svc := &fakeGroupService{detail: sampleDetail()}
	rec := postForm(newGroupServer(t, svc, root), "/admin/groups/5/access", url.Values{"connection_id": {"x"}, "access_level": {"view_only"}})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Pilih koneksi yang valid.") || svc.grantCalls != 0 {
		t.Errorf("connection_id salah: status = %d, calls = %d", rec.Code, svc.grantCalls)
	}

	svc = &fakeGroupService{
		detail:   sampleDetail(),
		grantErr: &domain.ValidationError{Fields: map[string]string{"access_level": "Pilih level akses yang valid."}},
	}
	rec = postForm(newGroupServer(t, svc, root), "/admin/groups/5/access", url.Values{"connection_id": {"3"}, "access_level": {"admin"}})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Pilih level akses yang valid.") {
		t.Errorf("level salah: status = %d", rec.Code)
	}
	if svc.grant.level != domain.AccessLevel("admin") {
		t.Errorf("handler harus meneruskan level apa adanya ke service untuk divalidasi, dapat %q", svc.grant.level)
	}
}

func TestGroupHandler_RevokeAccess(t *testing.T) {
	svc := &fakeGroupService{}
	h := newGroupServer(t, svc, root)

	rec := postForm(h, "/admin/groups/5/access/3/delete", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/groups/5" || svc.revoked != [2]int64{5, 3} {
		t.Errorf("status = %d, revoked = %v", rec.Code, svc.revoked)
	}
	svc.revokeErr = domain.ErrNotFound
	if rec := postForm(h, "/admin/groups/5/access/3/delete", nil); rec.Code != http.StatusNotFound {
		t.Errorf("akses tak ada: status = %d", rec.Code)
	}
}

func TestGroupHandler_AllRoutesRejectNonAdmin(t *testing.T) {
	svc := &fakeGroupService{detail: sampleDetail()}
	h := newGroupServer(t, svc, domain.Principal{UserID: 7, Username: "biasa"})
	form := url.Values{"name": {"x"}, "user_id": {"13"}, "connection_id": {"3"}, "access_level": {"view_only"}}

	for _, path := range []string{"/admin/groups", "/admin/groups/5"} {
		if rec := get(h, path); rec.Code != http.StatusForbidden {
			t.Errorf("GET %s: status = %d, mau 403", path, rec.Code)
		}
	}
	for _, path := range []string{
		"/admin/groups", "/admin/groups/5", "/admin/groups/5/delete", "/admin/groups/5/members",
		"/admin/groups/5/members/11/delete", "/admin/groups/5/access", "/admin/groups/5/access/3/delete",
	} {
		if rec := postForm(h, path, form); rec.Code != http.StatusForbidden {
			t.Errorf("POST %s: status = %d, mau 403", path, rec.Code)
		}
	}
}

func TestGroupHandler_WriteRoutesRejectGET(t *testing.T) {
	h := newGroupServer(t, &fakeGroupService{detail: sampleDetail()}, root)
	for _, path := range []string{
		"/admin/groups/5/delete", "/admin/groups/5/members", "/admin/groups/5/members/11/delete",
		"/admin/groups/5/access", "/admin/groups/5/access/3/delete",
	} {
		if rec := get(h, path); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: status = %d, mau 405", path, rec.Code)
		}
	}
}

func TestGroupHandler_UnexpectedErrorIs500WithoutDetail(t *testing.T) {
	svc := &fakeGroupService{createErr: errors.New("koneksi ke 10.0.0.5 password=hunter2")}
	rec := postForm(newGroupServer(t, svc, root), "/admin/groups", url.Values{"name": {"Tim"}})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, mau 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "hunter2") || strings.Contains(rec.Body.String(), "10.0.0.5") {
		t.Error("detail error tidak boleh bocor ke response")
	}
}
