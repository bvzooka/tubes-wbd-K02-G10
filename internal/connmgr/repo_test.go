package connmgr

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"dbplatform/internal/domain"
)

func goodInput(name string) domain.ConnectionInput {
	return domain.ConnectionInput{
		Name: name, DBMS: domain.DBMSPostgres, Host: "db.local", Port: 5432,
		DatabaseName: "sales", Username: "app", Password: "S3cret-Pass!", SSLMode: "prefer",
	}
}

func TestRepo_Integration_InsertEncryptsPassword(t *testing.T) {
	r, pool := newRepo(t)
	ctx := context.Background()

	c, err := r.Insert(ctx, goodInput("Sales"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == 0 || c.Name != "Sales" || !c.IsActive || c.DBMS != domain.DBMSPostgres || c.Port != 5432 {
		t.Errorf("hasil insert = %+v", c)
	}

	var enc []byte
	if err := pool.QueryRow(ctx, `SELECT db_password_enc FROM connections WHERE id = $1`, c.ID).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte("S3cret-Pass!")) || len(enc) <= len("S3cret-Pass!") {
		t.Fatal("password tidak boleh tersimpan sebagai plaintext")
	}

	got, pw, err := r.Load(ctx, c.ID)
	if err != nil || pw != "S3cret-Pass!" || got.Host != "db.local" {
		t.Fatalf("Load = %+v %q %v", got, pw, err)
	}
}

func TestRepo_Integration_LoadWithWrongKeyFails(t *testing.T) {
	r, pool := newRepo(t)
	c, _ := r.Insert(context.Background(), goodInput("Sales"), 0)
	other := NewRepo(pool, bytes.Repeat([]byte{9}, 32))
	if _, _, err := other.Load(context.Background(), c.ID); err == nil {
		t.Fatal("kunci salah harus gagal mendekripsi")
	}
}

func TestRepo_Integration_DuplicateNameCaseInsensitive(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	if _, err := r.Insert(ctx, goodInput("Sales"), 0); err != nil {
		t.Fatal(err)
	}
	_, err := r.Insert(ctx, goodInput("SALES"), 0)
	var ve *domain.ValidationError
	if !errors.As(err, &ve) || ve.Fields["name"] == "" {
		t.Fatalf("nama ganda harus ValidationError field name, dapat %v", err)
	}
}

func TestRepo_Integration_UpdateKeepsPasswordWhenEmpty(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	c, _ := r.Insert(ctx, goodInput("Sales"), 0)

	in := goodInput("Sales Baru")
	in.Host = "other.local"
	in.Password = ""
	upd, err := r.Update(ctx, c.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if upd.Name != "Sales Baru" || upd.Host != "other.local" {
		t.Errorf("update = %+v", upd)
	}
	if !upd.UpdatedAt.After(c.UpdatedAt) {
		t.Error("updated_at harus maju (dipakai Provider untuk invalidasi cache)")
	}
	if _, pw, _ := r.Load(ctx, c.ID); pw != "S3cret-Pass!" {
		t.Errorf("password kosong harus mempertahankan password lama, dapat %q", pw)
	}

	in.Password = "BaruBanget-123"
	if _, err := r.Update(ctx, c.ID, in); err != nil {
		t.Fatal(err)
	}
	if _, pw, _ := r.Load(ctx, c.ID); pw != "BaruBanget-123" {
		t.Errorf("password harus berubah, dapat %q", pw)
	}
}

func TestRepo_Integration_UpdateErrors(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	if _, err := r.Update(ctx, 9999, goodInput("X")); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("update id tak ada: mau ErrNotFound, dapat %v", err)
	}
	a, _ := r.Insert(ctx, goodInput("A"), 0)
	_, _ = r.Insert(ctx, goodInput("B"), 0)
	in := goodInput("b")
	if _, err := r.Update(ctx, a.ID, in); err == nil {
		t.Error("rename ke nama yang sudah dipakai harus ditolak")
	}
}

func TestRepo_Integration_SetActiveAndStamp(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	c, _ := r.Insert(ctx, goodInput("Sales"), 0)

	if err := r.SetActive(ctx, c.ID, false); err != nil {
		t.Fatal(err)
	}
	updated, active, err := r.Stamp(ctx, c.ID)
	if err != nil || active {
		t.Fatalf("Stamp: active=%v err=%v", active, err)
	}
	if !updated.After(c.UpdatedAt) {
		t.Error("SetActive harus memajukan updated_at")
	}
	got, _ := r.Get(ctx, c.ID)
	if got.IsActive {
		t.Error("koneksi harus nonaktif, bukan terhapus")
	}
	if err := r.SetActive(ctx, 9999, true); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("SetActive id tak ada: %v", err)
	}
	if _, _, err := r.Stamp(ctx, 9999); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Stamp id tak ada: %v", err)
	}
}

func TestRepo_Integration_GetListNotFound(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	if _, err := r.Get(ctx, 123); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get: %v", err)
	}
	if _, _, err := r.Load(ctx, 123); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Load: %v", err)
	}
	list, err := r.List(ctx)
	if err != nil || len(list) != 0 {
		t.Fatalf("List kosong: %v %v", list, err)
	}

	_, _ = r.Insert(ctx, goodInput("beta"), 0)
	_, _ = r.Insert(ctx, goodInput("Alpha"), 0)
	c, _ := r.Insert(ctx, goodInput("Gamma"), 0)
	_ = r.SetActive(ctx, c.ID, false)

	list, _ = r.List(ctx)
	if len(list) != 3 || list[0].Name != "Alpha" || list[1].Name != "beta" || list[2].Name != "Gamma" {
		t.Errorf("List harus terurut tanpa peduli huruf & memuat yang nonaktif: %+v", list)
	}
	if list[2].IsActive {
		t.Error("Gamma harus nonaktif")
	}
}

func TestRepo_Integration_CreatedBy(t *testing.T) {
	r, pool := newRepo(t)
	ctx := context.Background()
	var uid int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (username, password_hash) VALUES ('kurt', 'x') RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	c, err := r.Insert(ctx, goodInput("Sales"), uid)
	if err != nil {
		t.Fatal(err)
	}
	var by *int64
	_ = pool.QueryRow(ctx, `SELECT created_by FROM connections WHERE id = $1`, c.ID).Scan(&by)
	if by == nil || *by != uid {
		t.Errorf("created_by = %v, mau %d", by, uid)
	}
}
