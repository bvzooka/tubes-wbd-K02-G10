package admin_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"dbplatform/internal/admin"
	"dbplatform/internal/domain"
	"dbplatform/internal/httpx"
)

func flashFrom(t *testing.T, rec *httptest.ResponseRecorder) *httpx.Flash {
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

func targetUser() domain.User {
	return domain.User{ID: 2, Username: "budi", IsActive: true, CreatedAt: time.Now()}
}

func TestUserHandler_ListShowsActionButtons(t *testing.T) {
	svc := &fakeUserService{users: []domain.User{
		{ID: 1, Username: "root", IsPlatformAdmin: true, IsActive: true, CreatedAt: time.Now()},
		{ID: 2, Username: "aktif", IsActive: true, CreatedAt: time.Now()},
		{ID: 3, Username: "tidur", IsActive: false, CreatedAt: time.Now()},
	}}
	body := get(newHandlerServer(t, svc, root), "/admin/users").Body.String()

	for _, want := range []string{
		`href="/admin/users/1/edit"`, `href="/admin/users/2/edit"`, `href="/admin/users/3/edit"`,
		`action="/admin/users/2/deactivate"`, `action="/admin/users/3/activate"`, "(Anda)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body tidak memuat %q", want)
		}
	}
	for _, bad := range []string{`/admin/users/1/deactivate`, `/admin/users/1/activate`, `/admin/users/2/activate`, `/admin/users/3/deactivate`} {
		if strings.Contains(body, bad) {
			t.Errorf("body tidak boleh memuat %q", bad)
		}
	}
}

func TestUserHandler_EditForm(t *testing.T) {
	svc := &fakeUserService{getUser: domain.User{ID: 2, Username: "<b>budi</b>", IsPlatformAdmin: true, IsActive: true}}
	rec := get(newHandlerServer(t, svc, root), "/admin/users/2/edit")

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, want := range []string{`action="/admin/users/2"`, `name="csrf_token"`, `name="password"`, `type="checkbox" name="is_platform_admin"`, "checked", "&lt;b&gt;budi&lt;/b&gt;"} {
		if !strings.Contains(body, want) {
			t.Errorf("form tidak memuat %q", want)
		}
	}
	if strings.Contains(body, "<b>budi</b>") {
		t.Error("username harus di-escape")
	}
}

func TestUserHandler_EditFormForSelfLocksAdminRole(t *testing.T) {
	svc := &fakeUserService{getUser: domain.User{ID: root.UserID, Username: "root", IsPlatformAdmin: true, IsActive: true}}
	body := get(newHandlerServer(t, svc, root), "/admin/users/1/edit").Body.String()

	if !strings.Contains(body, `type="hidden" name="is_platform_admin" value="on"`) {
		t.Error("akun sendiri harus mengirim peran admin lewat input tersembunyi")
	}
	if strings.Contains(body, `type="checkbox"`) {
		t.Error("akun sendiri tidak boleh punya checkbox peran")
	}
}

func TestUserHandler_EditFormUnknownOrInvalidID(t *testing.T) {
	h := newHandlerServer(t, &fakeUserService{getErr: domain.ErrNotFound}, root)
	if rec := get(h, "/admin/users/999/edit"); rec.Code != http.StatusNotFound {
		t.Errorf("id tak ada: status = %d, mau 404", rec.Code)
	}
	for _, path := range []string{"/admin/users/abc/edit", "/admin/users/0/edit", "/admin/users/-4/edit"} {
		if rec := get(h, path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, mau 404", path, rec.Code)
		}
	}
}

func TestUserHandler_UpdateSuccess(t *testing.T) {
	svc := &fakeUserService{getUser: targetUser()}
	rec := postForm(newHandlerServer(t, svc, root), "/admin/users/2", url.Values{
		"password": {"rahasia-baru-123"}, "is_platform_admin": {"on"},
	})

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/users" {
		t.Fatalf("status = %d, location = %q", rec.Code, rec.Header().Get("Location"))
	}
	if svc.gotID != 2 || svc.gotUpdate.Password != "rahasia-baru-123" || !svc.gotUpdate.IsPlatformAdmin {
		t.Errorf("input ke service: id = %d, %+v", svc.gotID, svc.gotUpdate)
	}
	fl := flashFrom(t, rec)
	if fl == nil || fl.Kind != httpx.FlashSuccess || !strings.Contains(fl.Message, "sesi") {
		t.Errorf("flash = %+v, harus sukses dan menyebut sesi diakhiri bila password diganti", fl)
	}
}

func TestUserHandler_UpdateWithoutPasswordFlashHasNoSessionNote(t *testing.T) {
	svc := &fakeUserService{getUser: targetUser()}
	rec := postForm(newHandlerServer(t, svc, root), "/admin/users/2", url.Values{"password": {""}})

	if svc.gotUpdate.Password != "" || svc.gotUpdate.IsPlatformAdmin {
		t.Errorf("input ke service = %+v", svc.gotUpdate)
	}
	if fl := flashFrom(t, rec); fl == nil || strings.Contains(fl.Message, "sesi") {
		t.Errorf("flash = %+v, tidak boleh menyebut sesi", fl)
	}
}

func TestUserHandler_UpdateRejectionsRerenderForm(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"validasi password", &domain.ValidationError{Fields: map[string]string{"password": "Password harus 10 sampai 72 karakter."}}, "Password harus 10 sampai 72 karakter."},
		{"mencabut peran sendiri", admin.ErrSelfDemote, "tidak dapat mencabut peran admin dari diri sendiri"},
		{"admin terakhir", admin.ErrLastAdmin, "minimal satu Platform Admin aktif"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeUserService{getUser: targetUser(), updateErr: tt.err}
			rec := postForm(newHandlerServer(t, svc, root), "/admin/users/2", url.Values{
				"password": {"rahasia-baru-123"}, "is_platform_admin": {"on"},
			})

			body := rec.Body.String()
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, mau 422", rec.Code)
			}
			if !strings.Contains(body, tt.want) {
				t.Errorf("body tidak memuat %q", tt.want)
			}
			if !strings.Contains(body, "checked") {
				t.Error("pilihan admin yang dikirim harus dipertahankan")
			}
			if strings.Contains(body, "rahasia-baru-123") {
				t.Error("password tidak boleh dikembalikan ke form")
			}
		})
	}
}

func TestUserHandler_UpdateUnknownUserIs404(t *testing.T) {
	svc := &fakeUserService{updateErr: domain.ErrNotFound, getErr: domain.ErrNotFound}
	rec := postForm(newHandlerServer(t, svc, root), "/admin/users/999", url.Values{"password": {""}})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, mau 404", rec.Code)
	}
}

func TestUserHandler_DeactivateAndActivate(t *testing.T) {
	svc := &fakeUserService{}
	h := newHandlerServer(t, svc, root)

	rec := postForm(h, "/admin/users/2/deactivate", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/users" || svc.offID != 2 {
		t.Errorf("deactivate: status = %d, location = %q, id = %d", rec.Code, rec.Header().Get("Location"), svc.offID)
	}
	if fl := flashFrom(t, rec); fl == nil || fl.Kind != httpx.FlashSuccess || !strings.Contains(fl.Message, "dinonaktifkan") {
		t.Errorf("flash deactivate = %+v", fl)
	}

	rec = postForm(h, "/admin/users/3/activate", nil)
	if rec.Code != http.StatusSeeOther || svc.onID != 3 {
		t.Errorf("activate: status = %d, id = %d", rec.Code, svc.onID)
	}
	if fl := flashFrom(t, rec); fl == nil || !strings.Contains(fl.Message, "diaktifkan") {
		t.Errorf("flash activate = %+v", fl)
	}
}

func TestUserHandler_DeactivateRefusalsRedirectWithErrorFlash(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"diri sendiri", admin.ErrSelfDeactivate, "tidak dapat menonaktifkan akun Anda sendiri"},
		{"admin terakhir", admin.ErrLastAdmin, "minimal satu Platform Admin aktif"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := postForm(newHandlerServer(t, &fakeUserService{offErr: tt.err}, root), "/admin/users/2/deactivate", nil)
			if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/users" {
				t.Fatalf("status = %d, location = %q", rec.Code, rec.Header().Get("Location"))
			}
			fl := flashFrom(t, rec)
			if fl == nil || fl.Kind != httpx.FlashError || !strings.Contains(fl.Message, tt.want) {
				t.Errorf("flash = %+v, mau pesan error memuat %q", fl, tt.want)
			}
		})
	}
}

func TestUserHandler_ManagementRoutesRejectNonAdmin(t *testing.T) {
	svc := &fakeUserService{getUser: targetUser()}
	h := newHandlerServer(t, svc, domain.Principal{UserID: 5, Username: "biasa"})

	if rec := get(h, "/admin/users/2/edit"); rec.Code != http.StatusForbidden {
		t.Errorf("GET edit: status = %d, mau 403", rec.Code)
	}
	for _, path := range []string{"/admin/users/2", "/admin/users/2/deactivate", "/admin/users/2/activate"} {
		if rec := postForm(h, path, url.Values{"password": {"rahasia-baru-123"}}); rec.Code != http.StatusForbidden {
			t.Errorf("POST %s: status = %d, mau 403", path, rec.Code)
		}
	}
}

func TestUserHandler_ManagementRoutesRejectGET(t *testing.T) {
	h := newHandlerServer(t, &fakeUserService{}, root)
	for _, path := range []string{"/admin/users/2/deactivate", "/admin/users/2/activate"} {
		rec := get(h, path)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: status = %d, mau 405 (aksi tulis tidak boleh lewat GET)", path, rec.Code)
		}
	}
}
