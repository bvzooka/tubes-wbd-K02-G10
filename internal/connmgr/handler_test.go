package connmgr_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"dbplatform/internal/dbmgmttest"
	"dbplatform/internal/domain"
)

func wantStatus(t *testing.T, resp *http.Response, want int, what string) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("%s: status %d, mau %d", what, resp.StatusCode, want)
	}
}

func adminClient(t *testing.T, a *dbmgmttest.App) *http.Client {
	t.Helper()
	a.SeedUser(t, "root", true)
	c := a.Client()
	a.Login(t, c, "root")
	return c
}

func formFor(a *dbmgmttest.App, name string) url.Values {
	in := a.TargetInput(name)
	return url.Values{
		"name": {in.Name}, "dbms": {string(in.DBMS)}, "host": {in.Host}, "port": {fmt.Sprint(in.Port)},
		"database_name": {in.DatabaseName}, "db_username": {in.Username}, "password": {in.Password}, "ssl_mode": {"disable"},
	}
}

func TestConnections_Integration_RequireLoginAndAdmin(t *testing.T) {
	a := dbmgmttest.New(t)
	paths := []string{"/admin/connections", "/admin/connections/new", fmt.Sprintf("/admin/connections/%d/edit", a.ConnID)}

	anon := a.Client()
	for _, p := range paths {
		resp, _ := a.Get(t, anon, p)
		wantStatus(t, resp, http.StatusSeeOther, p)
	}

	a.SeedUser(t, "biasa", false)
	c := a.Client()
	a.Login(t, c, "biasa")
	for _, p := range paths {
		resp, _ := a.Get(t, c, p)
		wantStatus(t, resp, http.StatusForbidden, p)
	}
	// Aksi tulis oleh non-admin ditolak dan tidak mengubah data.
	for _, p := range []string{
		"/admin/connections", "/admin/connections/test",
		fmt.Sprintf("/admin/connections/%d", a.ConnID),
		fmt.Sprintf("/admin/connections/%d/deactivate", a.ConnID),
	} {
		resp, _ := a.Post(t, c, p, formFor(a, "Penyusup"))
		wantStatus(t, resp, http.StatusForbidden, "POST "+p)
	}
	list, _ := a.Repo.List(t.Context())
	if len(list) != 1 || list[0].Name != "Sampel" || !list[0].IsActive {
		t.Fatalf("data berubah oleh non-admin: %+v", list)
	}
}

func TestConnections_Integration_ListNeverShowsPassword(t *testing.T) {
	a := dbmgmttest.New(t)
	setUniquePassword(t, a)
	c := adminClient(t, a)
	resp, body := a.Get(t, c, "/admin/connections")
	wantStatus(t, resp, http.StatusOK, "list")
	for _, want := range []string{"Sampel", "Aktif", "Tambah koneksi", "/edit", "Nonaktifkan"} {
		if !strings.Contains(body, want) {
			t.Errorf("daftar tidak memuat %q", want)
		}
	}
	if strings.Contains(body, uniquePassword) {
		t.Error("password tidak boleh tampil")
	}
}

func TestConnections_Integration_CreateFlow(t *testing.T) {
	a := dbmgmttest.New(t)
	c := adminClient(t, a)

	resp, body := a.Get(t, c, "/admin/connections/new")
	wantStatus(t, resp, http.StatusOK, "form baru")
	if !strings.Contains(body, `name="csrf_token"`) || !strings.Contains(body, "Uji koneksi") {
		t.Error("form baru harus memuat csrf dan tombol uji koneksi")
	}

	resp, _ = a.Post(t, c, "/admin/connections", formFor(a, "Kedua"))
	wantStatus(t, resp, http.StatusSeeOther, "create")
	if loc := resp.Header.Get("Location"); loc != "/admin/connections" {
		t.Errorf("redirect ke %q", loc)
	}
	_, body = a.Get(t, c, "/admin/connections")
	if !strings.Contains(body, "Kedua") || !strings.Contains(body, "berhasil dibuat") {
		t.Error("koneksi baru dan pesan flash harus tampil")
	}

	var enc []byte
	if err := a.Pool.QueryRow(t.Context(), `SELECT db_password_enc FROM connections WHERE name = 'Kedua'`).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if pw := a.TargetInput("x").Password; strings.Contains(string(enc), pw) {
		t.Error("password harus tersimpan terenkripsi")
	}
}

func TestConnections_Integration_CreateInvalidRerendersWith422(t *testing.T) {
	a := dbmgmttest.New(t)
	c := adminClient(t, a)

	form := formFor(a, "")
	form.Set("host", "db.contoh.local")
	form.Set("password", "")
	resp, body := a.Post(t, c, "/admin/connections", form)
	wantStatus(t, resp, http.StatusUnprocessableEntity, "input tidak valid")
	if !strings.Contains(body, "Nama harus 1 sampai 64 karakter") || !strings.Contains(body, "Password tidak boleh kosong") {
		t.Error("pesan per field harus tampil")
	}
	if !strings.Contains(body, `value="db.contoh.local"`) {
		t.Error("nilai yang sudah diisi harus tampil ulang")
	}

	form = formFor(a, "Port Salah")
	form.Set("port", "abc")
	form.Set("password", "RahasiaForm123")
	resp, body = a.Post(t, c, "/admin/connections", form)
	wantStatus(t, resp, http.StatusUnprocessableEntity, "port bukan angka")
	if !strings.Contains(body, "Port harus berupa angka") {
		t.Error("pesan port tidak ada")
	}
	if strings.Contains(body, "RahasiaForm123") {
		t.Fatal("password tidak boleh dirender ulang")
	}

	resp, body = a.Post(t, c, "/admin/connections", formFor(a, "sampel"))
	wantStatus(t, resp, http.StatusUnprocessableEntity, "nama ganda")
	if !strings.Contains(body, "Nama koneksi sudah dipakai") {
		t.Error("pesan nama ganda tidak ada")
	}
	if list, _ := a.Repo.List(t.Context()); len(list) != 1 {
		t.Errorf("tidak boleh ada yang tersimpan, ada %d", len(list))
	}
}

func TestConnections_Integration_CSRFRequired(t *testing.T) {
	a := dbmgmttest.New(t)
	c := adminClient(t, a)
	resp, _ := a.PostRaw(t, c, "/admin/connections", formFor(a, "TanpaCSRF"))
	wantStatus(t, resp, http.StatusForbidden, "tanpa csrf")
	if list, _ := a.Repo.List(t.Context()); len(list) != 1 {
		t.Error("POST tanpa CSRF tidak boleh menyimpan")
	}
}

func TestConnections_Integration_EditAndUpdate(t *testing.T) {
	a := dbmgmttest.New(t)
	setUniquePassword(t, a)
	c := adminClient(t, a)
	editPath := fmt.Sprintf("/admin/connections/%d", a.ConnID)

	resp, body := a.Get(t, c, editPath+"/edit")
	wantStatus(t, resp, http.StatusOK, "form ubah")
	if !strings.Contains(body, `value="Sampel"`) || !strings.Contains(body, "Kosongkan jika password tidak ingin diubah") {
		t.Error("form ubah harus terisi dan memuat petunjuk password")
	}
	if strings.Contains(body, `type="password" autocomplete="new-password" required`) {
		t.Error("password tidak wajib saat ubah")
	}
	if strings.Contains(body, uniquePassword) {
		t.Error("password tidak boleh ditampilkan di form ubah")
	}

	// Password kosong: tidak diubah.
	form := formFor(a, "Sampel Baru")
	form.Set("password", "")
	resp, _ = a.Post(t, c, editPath, form)
	wantStatus(t, resp, http.StatusSeeOther, "update")
	_, pw, err := a.Repo.Load(t.Context(), a.ConnID)
	if err != nil || pw != uniquePassword {
		t.Fatalf("password lama harus dipertahankan: %q %v", pw, err)
	}
	if got, _ := a.Repo.Get(t.Context(), a.ConnID); got.Name != "Sampel Baru" {
		t.Errorf("nama = %q", got.Name)
	}

	// Input tidak valid: 422 dan data lama utuh.
	form.Set("port", "70000")
	resp, body = a.Post(t, c, editPath, form)
	wantStatus(t, resp, http.StatusUnprocessableEntity, "update invalid")
	if !strings.Contains(body, "Port harus antara 1 sampai 65535") {
		t.Error("pesan validasi port tidak ada")
	}

	resp, _ = a.Get(t, c, "/admin/connections/99999/edit")
	wantStatus(t, resp, http.StatusNotFound, "edit id tak ada")
	resp, _ = a.Get(t, c, "/admin/connections/abc/edit")
	wantStatus(t, resp, http.StatusNotFound, "edit id bukan angka")
	resp, _ = a.Post(t, c, "/admin/connections/99999", formFor(a, "X"))
	wantStatus(t, resp, http.StatusNotFound, "update id tak ada")
}

func TestConnections_Integration_DeactivateActivateAffectsViewer(t *testing.T) {
	a := dbmgmttest.New(t)
	u := a.SeedUser(t, "pembaca", false)
	a.GrantTo(t, "readers", u, domain.AccessViewOnly)
	admin := adminClient(t, a)
	reader := a.Client()
	a.Login(t, reader, "pembaca")
	base := fmt.Sprintf("/admin/connections/%d", a.ConnID)

	if resp, _ := a.Get(t, reader, fmt.Sprintf("/databases/%d", a.ConnID)); resp.StatusCode != http.StatusOK {
		t.Fatalf("awal: %d", resp.StatusCode)
	}

	resp, _ := a.Post(t, admin, base+"/deactivate", nil)
	wantStatus(t, resp, http.StatusSeeOther, "deactivate")
	_, body := a.Get(t, admin, "/admin/connections")
	if !strings.Contains(body, "Nonaktif") || !strings.Contains(body, "Aktifkan") {
		t.Error("daftar admin harus menampilkan status nonaktif dan tombol aktifkan")
	}
	if resp, _ := a.Get(t, reader, fmt.Sprintf("/databases/%d", a.ConnID)); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("setelah dinonaktifkan: %d, mau 403", resp.StatusCode)
	}
	if got, _ := a.Repo.Get(t.Context(), a.ConnID); got.ID == 0 {
		t.Fatal("koneksi tidak boleh terhapus")
	}

	resp, _ = a.Post(t, admin, base+"/activate", nil)
	wantStatus(t, resp, http.StatusSeeOther, "activate")
	if resp, _ := a.Get(t, reader, fmt.Sprintf("/databases/%d", a.ConnID)); resp.StatusCode != http.StatusOK {
		t.Fatalf("setelah diaktifkan: %d", resp.StatusCode)
	}
	resp, _ = a.Post(t, admin, "/admin/connections/99999/deactivate", nil)
	wantStatus(t, resp, http.StatusNotFound, "deactivate id tak ada")
}

func TestConnections_Integration_TestEndpoint(t *testing.T) {
	a := dbmgmttest.New(t)
	c := adminClient(t, a)

	resp, body := a.Post(t, c, "/admin/connections/test", formFor(a, "Uji"))
	wantStatus(t, resp, http.StatusOK, "uji berhasil")
	if !strings.Contains(body, "Koneksi berhasil") || !strings.Contains(body, "alert-success") {
		t.Error("harus menampilkan keberhasilan")
	}
	if list, _ := a.Repo.List(t.Context()); len(list) != 1 {
		t.Error("uji koneksi tidak boleh menyimpan")
	}

	bad := formFor(a, "Uji")
	bad.Set("port", "1")
	bad.Set("password", "PasswordRahasia")
	resp, body = a.Post(t, c, "/admin/connections/test", bad)
	wantStatus(t, resp, http.StatusOK, "uji gagal")
	if !strings.Contains(body, "Tidak dapat terhubung ke database") || !strings.Contains(body, "alert-error") {
		t.Error("harus menampilkan kegagalan yang ramah")
	}
	if strings.Contains(body, "PasswordRahasia") {
		t.Fatal("password tidak boleh dirender")
	}

	invalid := formFor(a, "Uji")
	invalid.Set("host", "")
	resp, body = a.Post(t, c, "/admin/connections/test", invalid)
	wantStatus(t, resp, http.StatusUnprocessableEntity, "uji input tidak valid")
	if !strings.Contains(body, "Host tidak boleh kosong") {
		t.Error("pesan validasi host tidak ada")
	}

	// Form ubah: password kosong memakai password tersimpan.
	edit := formFor(a, "Sampel")
	edit.Set("password", "")
	edit.Set("id", fmt.Sprint(a.ConnID))
	resp, body = a.Post(t, c, "/admin/connections/test", edit)
	wantStatus(t, resp, http.StatusOK, "uji dengan password tersimpan")
	if !strings.Contains(body, "Koneksi berhasil") {
		t.Error("password tersimpan harus dipakai")
	}
	edit.Set("id", "99999")
	resp, _ = a.Post(t, c, "/admin/connections/test", edit)
	wantStatus(t, resp, http.StatusNotFound, "uji id tak ada")
}

const uniquePassword = "KataSandi-Unik-987"

// setUniquePassword mengganti password koneksi sampel dengan nilai yang mudah dicari
// di HTML (password asli sama dengan username "postgres", sehingga ambigu).
func setUniquePassword(t *testing.T, a *dbmgmttest.App) {
	t.Helper()
	in := a.TargetInput("Sampel")
	in.Password = uniquePassword
	if _, err := a.Repo.Update(t.Context(), a.ConnID, in); err != nil {
		t.Fatal(err)
	}
}
