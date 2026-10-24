package admin_test

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"dbplatform/internal/admin"
	"dbplatform/internal/domain"
	"dbplatform/internal/testutil"
)

type bcryptHasher struct{}

func (bcryptHasher) Hash(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.MinCost)
	return string(b), err
}

func (bcryptHasher) Verify(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

var root = domain.Principal{UserID: 1, Username: "root", IsPlatformAdmin: true}

func TestUserRepo_Integration_CreateAndList(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	repo := admin.NewUserRepo(testutil.NewAppDB(t))

	b, err := repo.Create(ctx, "budi", "hash-b", false)
	if err != nil {
		t.Fatal(err)
	}
	if b.ID == 0 || !b.IsActive || b.IsPlatformAdmin || b.CreatedAt.IsZero() {
		t.Errorf("user = %+v", b)
	}
	if _, err := repo.Create(ctx, "ani", "hash-a", true); err != nil {
		t.Fatal(err)
	}

	users, err := repo.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0].Username != "ani" || users[1].Username != "budi" {
		t.Fatalf("List harus berurut menurut username: %+v", users)
	}
	if !users[0].IsPlatformAdmin || users[1].IsPlatformAdmin {
		t.Errorf("flag admin tidak tersimpan: %+v", users)
	}
}

func TestUserRepo_Integration_DuplicateUsernameIsConflict(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	repo := admin.NewUserRepo(testutil.NewAppDB(t))

	if _, err := repo.Create(ctx, "budi", "h", false); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(ctx, "budi", "h2", false); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, mau ErrConflict", err)
	}
}

func TestService_Integration_CreateUserStoresOnlyHash(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	pool := testutil.NewAppDB(t)
	svc := admin.NewService(admin.NewUserRepo(pool), bcryptHasher{}, &testutil.SpySessionAdmin{})

	const password = "rahasia-panjang-1"
	u, err := svc.CreateUser(ctx, root, admin.UserInput{Username: " Budi ", Password: password})
	if err != nil {
		t.Fatal(err)
	}

	var stored string
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, u.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == password || len(stored) < 4 || stored[:2] != "$2" {
		t.Errorf("password harus tersimpan sebagai hash bcrypt, dapat %q", stored)
	}
	if !(bcryptHasher{}).Verify(stored, password) {
		t.Error("hash tersimpan tidak cocok dengan password asli")
	}
	if u.Username != "budi" {
		t.Errorf("username harus dinormalkan, dapat %q", u.Username)
	}
}

func TestService_Integration_DuplicateUsernameIsCaseInsensitive(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	svc := admin.NewService(admin.NewUserRepo(testutil.NewAppDB(t)), bcryptHasher{}, &testutil.SpySessionAdmin{})
	in := admin.UserInput{Username: "budi", Password: "rahasia-panjang-1"}

	if _, err := svc.CreateUser(ctx, root, in); err != nil {
		t.Fatal(err)
	}
	in.Username = "BUDI"
	_, err := svc.CreateUser(ctx, root, in)

	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Fields["username"] == "" {
		t.Fatalf("err = %v, mau error validasi pada field username", err)
	}
}

func TestService_Integration_NonAdminCannotCreate(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	pool := testutil.NewAppDB(t)
	svc := admin.NewService(admin.NewUserRepo(pool), bcryptHasher{}, &testutil.SpySessionAdmin{})

	_, err := svc.CreateUser(ctx, domain.Principal{UserID: 9, Username: "biasa"},
		admin.UserInput{Username: "baru", Password: "rahasia-panjang-1"})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("err = %v, mau ErrForbidden", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil || n != 0 {
		t.Errorf("tidak boleh ada user yang dibuat: n=%d err=%v", n, err)
	}
}
