package connmgr

import (
	"context"
	"errors"
	"strings"
	"testing"
	"dbplatform/internal/auth/authtest"
	"dbplatform/internal/dbdriver/postgres"
	"dbplatform/internal/domain"
)

func newService(t *testing.T) (*Service, *Repo) {
	t.Helper()
	r, _ := newRepo(t)
	return NewService(r, postgres.New), r
}

func TestService_Integration_NonAdminForbidden(t *testing.T) {
	s, r := newService(t)
	ctx := context.Background()
	c, _ := r.Insert(ctx, goodInput("Sales"), 0)

	checks := map[string]error{}
	_, checks["Create"] = s.Create(ctx, plainPrincipal, goodInput("X"))
	_, checks["Update"] = s.Update(ctx, plainPrincipal, c.ID, goodInput("X"))
	checks["SetActive"] = s.SetActive(ctx, plainPrincipal, c.ID, false)
	_, checks["List"] = s.List(ctx, plainPrincipal)
	_, checks["Get"] = s.Get(ctx, plainPrincipal, c.ID)
	checks["TestConnection"] = s.TestConnection(ctx, plainPrincipal, 0, goodInput("X"))
	for name, err := range checks {
		if !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("%s: mau ErrForbidden, dapat %v", name, err)
		}
	}
	got, _ := r.Get(ctx, c.ID)
	if !got.IsActive || got.Name != "Sales" {
		t.Error("data tidak boleh berubah oleh non-admin")
	}
	if list, _ := r.List(ctx); len(list) != 1 {
		t.Error("Create oleh non-admin tidak boleh menyimpan")
	}
}

func TestService_Integration_CreateValidatesAndNormalizes(t *testing.T) {
	s, r := newService(t)
	ctx := context.Background()

	_, err := s.Create(ctx, adminPrincipal, domain.ConnectionInput{})
	var ve *domain.ValidationError
	if !errors.As(err, &ve) || ve.Fields["name"] == "" || ve.Fields["password"] == "" {
		t.Fatalf("input kosong harus ValidationError: %v", err)
	}
	if list, _ := r.List(ctx); len(list) != 0 {
		t.Fatal("input tidak valid tidak boleh tersimpan")
	}

	in := goodInput("  Spasi  ")
	in.Host = "  db.local "
	in.Password = " pw dengan spasi "
	c, err := s.Create(ctx, adminPrincipal, in)
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "Spasi" || c.Host != "db.local" {
		t.Errorf("field teks harus di-trim: %+v", c)
	}
	if _, pw, _ := r.Load(ctx, c.ID); pw != " pw dengan spasi " {
		t.Errorf("password tidak boleh di-trim: %q", pw)
	}
}

func TestService_Integration_UpdateEmptyPasswordKeepsOld(t *testing.T) {
	s, r := newService(t)
	ctx := context.Background()
	c, _ := s.Create(ctx, adminPrincipal, goodInput("Sales"))

	in := goodInput("Sales")
	in.Password = ""
	in.Host = "baru.local"
	if _, err := s.Update(ctx, adminPrincipal, c.ID, in); err != nil {
		t.Fatal(err)
	}
	got, pw, _ := r.Load(ctx, c.ID)
	if got.Host != "baru.local" || pw != "S3cret-Pass!" {
		t.Errorf("host=%q pw=%q", got.Host, pw)
	}

	in.Port = 0
	var ve *domain.ValidationError
	if _, err := s.Update(ctx, adminPrincipal, c.ID, in); !errors.As(err, &ve) || ve.Fields["port"] == "" {
		t.Errorf("update tidak valid harus ditolak: %v", err)
	}
}

func TestService_Integration_DeactivateActivate(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	c, _ := s.Create(ctx, adminPrincipal, goodInput("Sales"))
	if err := s.SetActive(ctx, adminPrincipal, c.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, adminPrincipal, c.ID); got.IsActive {
		t.Error("harus nonaktif")
	}
	if err := s.SetActive(ctx, adminPrincipal, c.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, adminPrincipal, c.ID); !got.IsActive {
		t.Error("harus aktif lagi")
	}
	list, err := s.List(ctx, adminPrincipal)
	if err != nil || len(list) != 1 {
		t.Errorf("List: %v %v", list, err)
	}
}

func TestService_Integration_TestConnection(t *testing.T) {
	s, _ := newService(t)
	target := authtest.NewTargetDB(t)
	ctx := context.Background()

	in := inputFor(t, target, "Target")
	if err := s.TestConnection(ctx, adminPrincipal, 0, in); err != nil {
		t.Fatalf("koneksi valid harus berhasil: %v", err)
	}

	// Tidak menyimpan apa pun.
	if list, _ := s.List(ctx, adminPrincipal); len(list) != 0 {
		t.Error("TestConnection tidak boleh menyimpan koneksi")
	}

	bad := in
	bad.Port = 1
	bad.Password = "RahasiaBesar"
	err := s.TestConnection(ctx, adminPrincipal, 0, bad)
	if !errors.Is(err, domain.ErrConnectionFailed) {
		t.Fatalf("port salah: mau ErrConnectionFailed, dapat %v", err)
	}
	if strings.Contains(err.Error(), "RahasiaBesar") {
		t.Error("pesan membocorkan password")
	}

	invalid := in
	invalid.Host = ""
	var ve *domain.ValidationError
	if err := s.TestConnection(ctx, adminPrincipal, 0, invalid); !errors.As(err, &ve) {
		t.Errorf("input tidak valid harus ValidationError: %v", err)
	}
}

func TestService_Integration_TestConnectionWrongPassword(t *testing.T) {
	s, _ := newService(t)
	target := authtest.NewTargetDB(t)
	role := newPasswordRole(t, target, "benar-123")
	in := inputFor(t, target, "Target")
	in.Username = role
	in.Password = "salah-123"

	err := s.TestConnection(context.Background(), adminPrincipal, 0, in)
	if err == nil {
		t.Skip("server memakai trust auth untuk TCP; tes password salah dilewati")
	}
	if !errors.Is(err, domain.ErrConnectionFailed) || err.Error() != "Autentikasi database gagal" {
		t.Fatalf("mau 'Autentikasi database gagal', dapat %v", err)
	}
	if strings.Contains(err.Error(), "salah-123") || strings.Contains(err.Error(), "benar-123") {
		t.Error("pesan membocorkan password")
	}

	in.Password = "benar-123"
	if err := s.TestConnection(context.Background(), adminPrincipal, 0, in); err != nil {
		t.Fatalf("password benar harus berhasil: %v", err)
	}
}

func TestService_Integration_TestConnectionUsesStoredPassword(t *testing.T) {
	s, _ := newService(t)
	target := authtest.NewTargetDB(t)
	role := newPasswordRole(t, target, "tersimpan-123")
	ctx := context.Background()

	in := inputFor(t, target, "Target")
	in.Username = role
	in.Password = "tersimpan-123"
	c, err := s.Create(ctx, adminPrincipal, in)
	if err != nil {
		t.Fatal(err)
	}

	form := in
	form.Password = "" // form ubah: kosong berarti pakai password tersimpan
	err = s.TestConnection(ctx, adminPrincipal, c.ID, form)
	if err != nil && !errors.Is(err, domain.ErrConnectionFailed) {
		t.Fatal(err)
	}
	if err != nil {
		t.Fatalf("password tersimpan seharusnya dipakai: %v", err)
	}
	if err := s.TestConnection(ctx, adminPrincipal, 99999, form); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("id tak ada: mau ErrNotFound, dapat %v", err)
	}
}

// Provider + Repo + driver Postgres bekerja bersama.
func TestProvider_Integration_WithRepoAndPostgres(t *testing.T) {
	s, r := newService(t)
	target := authtest.NewTargetDB(t, "CREATE TABLE halo (id int); INSERT INTO halo VALUES (1)")
	ctx := context.Background()
	c, err := s.Create(ctx, adminPrincipal, inputFor(t, target, "Target"))
	if err != nil {
		t.Fatal(err)
	}

	p := NewProvider(r, postgres.New)
	t.Cleanup(p.Close)

	d1, err := p.Get(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	td, err := d1.TableData(ctx, domain.TableRef{Schema: "public", Table: "halo"}, domain.PageRequest{})
	if err != nil || len(td.Rows) != 1 {
		t.Fatalf("TableData lewat provider: %v %+v", err, td)
	}
	if d2, _ := p.Get(ctx, c.ID); d2 != d1 {
		t.Error("driver harus di-cache")
	}

	// Update (updated_at maju) -> driver dibangun ulang.
	in := inputFor(t, target, "Target")
	in.Password = ""
	in.Name = "Target v2"
	if _, err := s.Update(ctx, adminPrincipal, c.ID, in); err != nil {
		t.Fatal(err)
	}
	d3, err := p.Get(ctx, c.ID)
	if err != nil || d3 == d1 {
		t.Fatalf("driver harus dibangun ulang setelah update: err=%v", err)
	}

	// Nonaktif -> ditolak.
	_ = s.SetActive(ctx, adminPrincipal, c.ID, false)
	if _, err := p.Get(ctx, c.ID); !errors.Is(err, domain.ErrConnectionInactive) {
		t.Errorf("mau ErrConnectionInactive, dapat %v", err)
	}
	if _, err := p.Get(ctx, 99999); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("mau ErrNotFound, dapat %v", err)
	}
}
