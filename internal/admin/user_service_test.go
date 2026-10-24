package admin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"dbplatform/internal/domain"
	"dbplatform/internal/testutil"
)

var (
	adminActor = domain.Principal{UserID: 1, Username: "root", IsPlatformAdmin: true}
	plainActor = domain.Principal{UserID: 2, Username: "budi"}
)

type createCall struct {
	username, hash string
	isAdmin        bool
}

type updateCall struct {
	id      int64
	hash    string
	isAdmin bool
}

type activeCall struct {
	id     int64
	active bool
}

type fakeStore struct {
	created   []createCall
	createErr error
	listed    []domain.User

	got          domain.User
	getErr       error
	updated      []updateCall
	updateErr    error
	wasAdmin     bool
	activeCalls  []activeCall
	setActiveErr error
}

func (f *fakeStore) Get(context.Context, int64) (domain.User, error) { return f.got, f.getErr }

func (f *fakeStore) Update(_ context.Context, id int64, hash string, isAdmin bool) (domain.User, bool, error) {
	if f.updateErr != nil {
		return domain.User{}, false, f.updateErr
	}
	f.updated = append(f.updated, updateCall{id, hash, isAdmin})
	return domain.User{ID: id, Username: "target", IsPlatformAdmin: isAdmin, IsActive: true}, f.wasAdmin, nil
}

func (f *fakeStore) SetActive(_ context.Context, id int64, active bool) (domain.User, error) {
	if f.setActiveErr != nil {
		return domain.User{}, f.setActiveErr
	}
	f.activeCalls = append(f.activeCalls, activeCall{id, active})
	return domain.User{ID: id, Username: "target", IsActive: active}, nil
}

func (f *fakeStore) Create(_ context.Context, username, hash string, isAdmin bool) (domain.User, error) {
	if f.createErr != nil {
		return domain.User{}, f.createErr
	}
	f.created = append(f.created, createCall{username, hash, isAdmin})
	return domain.User{ID: int64(len(f.created)), Username: username, IsPlatformAdmin: isAdmin, IsActive: true, CreatedAt: time.Now()}, nil
}

func (f *fakeStore) List(context.Context) ([]domain.User, error) { return f.listed, nil }

type fakeHasher struct{ err error }

func (h fakeHasher) Hash(plain string) (string, error) {
	if h.err != nil {
		return "", h.err
	}
	return "hashed:" + plain, nil
}
func (fakeHasher) Verify(hash, plain string) bool { return hash == "hashed:"+plain }

func newTestService(store *fakeStore, hasher fakeHasher) *Service {
	return NewService(store, hasher, &testutil.SpySessionAdmin{})
}

func TestService_CreateUser_Success(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(store, fakeHasher{})

	u, err := svc.CreateUser(context.Background(), adminActor, UserInput{
		Username: "  Budi.Santoso ", Password: "rahasia-panjang-1", IsPlatformAdmin: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "budi.santoso" || !u.IsPlatformAdmin {
		t.Errorf("user = %+v", u)
	}
	if len(store.created) != 1 {
		t.Fatalf("Create dipanggil %d kali", len(store.created))
	}
	got := store.created[0]
	if got.username != "budi.santoso" || !got.isAdmin {
		t.Errorf("argumen repo = %+v", got)
	}
	if got.hash == "rahasia-panjang-1" || !strings.HasPrefix(got.hash, "hashed:") {
		t.Errorf("repo harus menerima hash, bukan password asli: %q", got.hash)
	}
}

func TestService_CreateUser_NonAdminForbidden(t *testing.T) {
	store := &fakeStore{}
	svc := newTestService(store, fakeHasher{})

	_, err := svc.CreateUser(context.Background(), plainActor, UserInput{Username: "baru", Password: "rahasia-panjang-1"})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("err = %v, mau ErrForbidden", err)
	}
	if _, err := svc.ListUsers(context.Background(), plainActor); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("ListUsers err = %v, mau ErrForbidden", err)
	}
	if len(store.created) != 0 {
		t.Error("repo tidak boleh disentuh untuk non-admin")
	}
}

func TestService_CreateUser_Validation(t *testing.T) {
	tests := []struct {
		name       string
		in         UserInput
		wantFields []string
	}{
		{"username dan password salah", UserInput{Username: "a", Password: "pendek"}, []string{"username", "password"}},
		{"hanya username salah", UserInput{Username: "bu di", Password: "rahasia-panjang-1"}, []string{"username"}},
		{"hanya password salah", UserInput{Username: "budi", Password: "pendek"}, []string{"password"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{}
			_, err := newTestService(store, fakeHasher{}).CreateUser(context.Background(), adminActor, tt.in)

			var verr *domain.ValidationError
			if !errors.As(err, &verr) || !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("err = %v, mau *ValidationError", err)
			}
			if len(verr.Fields) != len(tt.wantFields) {
				t.Errorf("fields = %v, mau %v", verr.Fields, tt.wantFields)
			}
			for _, f := range tt.wantFields {
				if verr.Fields[f] == "" {
					t.Errorf("field %q tidak punya pesan", f)
				}
			}
			if len(store.created) != 0 {
				t.Error("repo tidak boleh dipanggil saat input tidak valid")
			}
		})
	}
}

func TestService_CreateUser_DuplicateUsernameBecomesFieldError(t *testing.T) {
	store := &fakeStore{createErr: domain.ErrConflict}
	_, err := newTestService(store, fakeHasher{}).CreateUser(context.Background(), adminActor,
		UserInput{Username: "budi", Password: "rahasia-panjang-1"})

	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Fields["username"] != msgUsernameTaken {
		t.Fatalf("err = %v, mau error validasi 'username sudah dipakai'", err)
	}
}

func TestService_CreateUser_OtherErrorsPropagate(t *testing.T) {
	boom := errors.New("koneksi putus")

	_, err := newTestService(&fakeStore{createErr: boom}, fakeHasher{}).CreateUser(context.Background(), adminActor,
		UserInput{Username: "budi", Password: "rahasia-panjang-1"})
	if !errors.Is(err, boom) || errors.Is(err, domain.ErrValidation) {
		t.Errorf("error repo harus diteruskan apa adanya, dapat %v", err)
	}

	_, err = newTestService(&fakeStore{}, fakeHasher{err: boom}).CreateUser(context.Background(), adminActor,
		UserInput{Username: "budi", Password: "rahasia-panjang-1"})
	if !errors.Is(err, boom) {
		t.Errorf("error hasher harus dibungkus dengan %%w, dapat %v", err)
	}
}

func TestService_ListUsers(t *testing.T) {
	store := &fakeStore{listed: []domain.User{{ID: 1, Username: "a"}, {ID: 2, Username: "b"}}}
	users, err := newTestService(store, fakeHasher{}).ListUsers(context.Background(), adminActor)
	if err != nil || len(users) != 2 {
		t.Fatalf("users = %v, err = %v", users, err)
	}
}
