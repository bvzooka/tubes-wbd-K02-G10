package admin_test

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

	"dbplatform/internal/admin"
	"dbplatform/internal/authstub"
	"dbplatform/internal/domain"
	"dbplatform/internal/httpx"
	webassets "dbplatform/web"
)

type fakeUserService struct {
	users     []domain.User
	createErr error
	gotIn     admin.UserInput
	calls     int
}

func (f *fakeUserService) CreateUser(_ context.Context, actor domain.Principal, in admin.UserInput) (domain.User, error) {
	f.calls++
	f.gotIn = in
	if !actor.IsPlatformAdmin {
		return domain.User{}, domain.ErrForbidden
	}
	if f.createErr != nil {
		return domain.User{}, f.createErr
	}
	return domain.User{ID: 7, Username: admin.NormalizeUsername(in.Username), IsActive: true}, nil
}

func (f *fakeUserService) ListUsers(_ context.Context, actor domain.Principal) ([]domain.User, error) {
	if !actor.IsPlatformAdmin {
		return nil, domain.ErrForbidden
	}
	return f.users, nil
}

func newHandlerServer(t *testing.T, svc *fakeUserService, actor domain.Principal) http.Handler {
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
	admin.NewUserHandler(d, svc).Register(mux)
	return mux
}

func postForm(h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestUserHandler_List(t *testing.T) {
	svc := &fakeUserService{users: []domain.User{
		{ID: 1, Username: "ani", IsPlatformAdmin: true, IsActive: true, CreatedAt: time.Date(2026, 10, 20, 9, 30, 0, 0, time.UTC)},
		{ID: 2, Username: "<script>x</script>", IsActive: false, CreatedAt: time.Now()},
	}}
	rec := get(newHandlerServer(t, svc, root), "/admin/users")

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, want := range []string{"ani", "Platform Admin", "Nonaktif", "20 Oct 2026 09:30", "/admin/users/new"} {
		if !strings.Contains(body, want) {
			t.Errorf("body tidak memuat %q", want)
		}
	}
	if strings.Contains(body, "<script>x</script>") {
		t.Error("username harus di-escape")
	}
}

func TestUserHandler_ListEmpty(t *testing.T) {
	rec := get(newHandlerServer(t, &fakeUserService{}, root), "/admin/users")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Belum ada pengguna") {
		t.Errorf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestUserHandler_NewForm(t *testing.T) {
	rec := get(newHandlerServer(t, &fakeUserService{}, root), "/admin/users/new")
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, want := range []string{`name="csrf_token"`, `name="username"`, `name="password"`, `type="password"`, `name="is_platform_admin"`, `action="/admin/users"`} {
		if !strings.Contains(body, want) {
			t.Errorf("form tidak memuat %q", want)
		}
	}
}

func TestUserHandler_CreateSuccess(t *testing.T) {
	svc := &fakeUserService{}
	rec := postForm(newHandlerServer(t, svc, root), "/admin/users", url.Values{
		"username": {"  Budi "}, "password": {"rahasia-panjang-1"}, "is_platform_admin": {"on"},
	})

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/users" {
		t.Fatalf("status = %d, location = %q", rec.Code, rec.Header().Get("Location"))
	}
	if svc.gotIn.Username != "  Budi " || svc.gotIn.Password != "rahasia-panjang-1" || !svc.gotIn.IsPlatformAdmin {
		t.Errorf("input ke service = %+v", svc.gotIn)
	}
	var flash *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == httpx.FlashCookieName {
			flash = c
		}
	}
	if flash == nil || flash.Value == "" {
		t.Error("cookie flash harus dipasang")
	}
}

func TestUserHandler_CreateCheckboxOffMeansNotAdmin(t *testing.T) {
	svc := &fakeUserService{}
	postForm(newHandlerServer(t, svc, root), "/admin/users", url.Values{
		"username": {"budi"}, "password": {"rahasia-panjang-1"},
	})
	if svc.gotIn.IsPlatformAdmin {
		t.Error("tanpa centang, user tidak boleh menjadi admin")
	}
}

func TestUserHandler_CreateValidationErrorRerendersForm(t *testing.T) {
	svc := &fakeUserService{createErr: &domain.ValidationError{Fields: map[string]string{
		"username": "Username sudah dipakai.",
		"password": "Password harus 10 sampai 72 karakter.",
	}}}
	rec := postForm(newHandlerServer(t, svc, root), "/admin/users", url.Values{
		"username": {"Budi"}, "password": {"pendek-banget"}, "is_platform_admin": {"on"},
	})

	body := rec.Body.String()
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, mau 422", rec.Code)
	}
	for _, want := range []string{"Username sudah dipakai.", "Password harus 10 sampai 72 karakter.", `value="budi"`, "checked"} {
		if !strings.Contains(body, want) {
			t.Errorf("body tidak memuat %q", want)
		}
	}
	if strings.Contains(body, "pendek-banget") {
		t.Error("password tidak boleh dikembalikan ke form")
	}
}

func TestUserHandler_NonAdminGets403(t *testing.T) {
	svc := &fakeUserService{}
	h := newHandlerServer(t, svc, domain.Principal{UserID: 5, Username: "biasa"})

	if rec := get(h, "/admin/users"); rec.Code != http.StatusForbidden {
		t.Errorf("GET status = %d, mau 403", rec.Code)
	}
	rec := postForm(h, "/admin/users", url.Values{"username": {"baru"}, "password": {"rahasia-panjang-1"}})
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST status = %d, mau 403", rec.Code)
	}
}

func TestUserHandler_UnexpectedErrorIs500WithoutDetail(t *testing.T) {
	svc := &fakeUserService{createErr: errors.New("koneksi ke 10.0.0.5 password=hunter2")}
	rec := postForm(newHandlerServer(t, svc, root), "/admin/users", url.Values{
		"username": {"budi"}, "password": {"rahasia-panjang-1"},
	})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, mau 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "hunter2") || strings.Contains(rec.Body.String(), "10.0.0.5") {
		t.Error("detail error tidak boleh bocor ke response")
	}
}
