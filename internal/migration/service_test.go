package migration

import (
	"context"
	"errors"
	"testing"
	"time"

	"dbplatform/internal/clock"
	"dbplatform/internal/domain"
)

type fakeAuthz struct {
	levels map[[2]int64]domain.AccessLevel
	err    error
	calls  int
}

func (f *fakeAuthz) EffectiveLevel(_ context.Context, userID, connID int64) (domain.AccessLevel, error) {
	return f.levels[[2]int64{userID, connID}], f.err
}

func (f *fakeAuthz) Can(_ context.Context, userID, connID int64, min domain.AccessLevel) (bool, error) {
	f.calls++
	if f.err != nil {
		return false, f.err
	}
	return f.levels[[2]int64{userID, connID}].AtLeast(min), nil
}

func (f *fakeAuthz) ListAccessible(context.Context, int64) ([]domain.AccessibleDatabase, error) {
	return nil, nil
}

type createArgs struct {
	connID    int64
	name, sql string
	checksum  string
	createdBy int64
}

type updateArgs struct {
	connID    int64
	version   int
	name, sql string
	checksum  string
}

type claimArgs struct {
	connID     int64
	version    int
	executedBy int64
	now        time.Time
}

type finishArgs struct {
	connID     int64
	version    int
	status     domain.MigrationStatus
	errMsg     string
	warning    string
	finishedAt time.Time
}

type stuckArgs struct {
	before     time.Time
	message    string
	finishedAt time.Time
}

type fakeStore struct {
	created   []createArgs
	updated   []updateArgs
	cancelled [][2]int64
	getCalls  int
	listCalls int

	getResult domain.Migration
	listed    []domain.Migration
	err       error

	claimResult domain.Migration
	claimErr    error
	claims      []claimArgs
	finishErr   error
	finishes    []finishArgs
	stuckCount  int
	stuckErr    error
	stuckCalls  []stuckArgs
}

func (f *fakeStore) Claim(_ context.Context, connID int64, version int, executedBy int64, now time.Time) (domain.Migration, error) {
	if f.claimErr != nil {
		return domain.Migration{}, f.claimErr
	}
	f.claims = append(f.claims, claimArgs{connID, version, executedBy, now})
	return f.claimResult, nil
}

func (f *fakeStore) Finish(_ context.Context, connID int64, version int, status domain.MigrationStatus, errMsg, warning string, finishedAt time.Time) error {
	if f.finishErr != nil {
		return f.finishErr
	}
	f.finishes = append(f.finishes, finishArgs{connID, version, status, errMsg, warning, finishedAt})
	return nil
}

func (f *fakeStore) FailStuck(_ context.Context, before time.Time, message string, finishedAt time.Time) (int, error) {
	f.stuckCalls = append(f.stuckCalls, stuckArgs{before, message, finishedAt})
	return f.stuckCount, f.stuckErr
}

func (f *fakeStore) Create(_ context.Context, connID int64, name, sql, checksum string, createdBy int64) (domain.Migration, error) {
	if f.err != nil {
		return domain.Migration{}, f.err
	}
	f.created = append(f.created, createArgs{connID, name, sql, checksum, createdBy})
	return domain.Migration{ConnectionID: connID, Version: len(f.created), Name: name, SQL: sql, Status: domain.MigrationDraft}, nil
}

func (f *fakeStore) Get(context.Context, int64, int) (domain.Migration, error) {
	f.getCalls++
	return f.getResult, f.err
}

func (f *fakeStore) List(context.Context, int64) ([]domain.Migration, error) {
	f.listCalls++
	return f.listed, f.err
}

func (f *fakeStore) UpdateDraft(_ context.Context, connID int64, version int, name, sql, checksum string) (domain.Migration, error) {
	if f.err != nil {
		return domain.Migration{}, f.err
	}
	f.updated = append(f.updated, updateArgs{connID, version, name, sql, checksum})
	return domain.Migration{ConnectionID: connID, Version: version, Name: name, SQL: sql}, nil
}

func (f *fakeStore) Cancel(_ context.Context, connID int64, version int) error {
	if f.err != nil {
		return f.err
	}
	f.cancelled = append(f.cancelled, [2]int64{connID, int64(version)})
	return nil
}

const connA int64 = 10

var (
	editor    = domain.Principal{UserID: 1, Username: "penyunting"}
	viewer    = domain.Principal{UserID: 2, Username: "pembaca"}
	outsider  = domain.Principal{UserID: 3, Username: "orang-luar"}
	superuser = domain.Principal{UserID: 4, Username: "root", IsPlatformAdmin: true}
)

func newTestService(store *fakeStore) (*Service, *fakeAuthz) {
	authz := &fakeAuthz{levels: map[[2]int64]domain.AccessLevel{
		{editor.UserID, connA}: domain.AccessViewEdit,
		{viewer.UserID, connA}: domain.AccessViewOnly,
	}}
	return NewService(store, authz, nil, clock.NewFake(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))), authz
}

func TestService_Create_Success(t *testing.T) {
	store := &fakeStore{}
	svc, _ := newTestService(store)

	m, err := svc.Create(context.Background(), editor, connA, Input{Name: "  tambah tabel ", SQL: "\n CREATE TABLE a (id int); \n"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != domain.MigrationDraft || len(store.created) != 1 {
		t.Fatalf("migration = %+v, created = %d", m, len(store.created))
	}
	got := store.created[0]
	want := createArgs{connA, "tambah tabel", "CREATE TABLE a (id int);", Checksum("CREATE TABLE a (id int);"), editor.UserID}
	if got != want {
		t.Errorf("argumen repo = %+v, mau %+v", got, want)
	}
}

func TestService_Create_ValidationErrorsDoNotTouchStore(t *testing.T) {
	tests := []struct {
		name       string
		in         Input
		wantFields []string
	}{
		{"nama dan sql kosong", Input{}, []string{"name", "sql"}},
		{"hanya nama kosong", Input{Name: " ", SQL: "SELECT 1"}, []string{"name"}},
		{"sql berisi commit", Input{Name: "x", SQL: "CREATE TABLE a (id int); COMMIT;"}, []string{"sql"}},
		{"sql tak ditutup", Input{Name: "x", SQL: "SELECT 'abc"}, []string{"sql"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{}
			svc, _ := newTestService(store)

			_, err := svc.Create(context.Background(), editor, connA, tt.in)
			var verr *domain.ValidationError
			if !errors.As(err, &verr) {
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

func TestService_WriteActionsRequireViewEdit(t *testing.T) {
	ctx := context.Background()
	valid := Input{Name: "x", SQL: "SELECT 1"}
	actions := map[string]func(*Service, domain.Principal) error{
		"Create": func(s *Service, a domain.Principal) error { _, err := s.Create(ctx, a, connA, valid); return err },
		"Update": func(s *Service, a domain.Principal) error { _, err := s.Update(ctx, a, connA, 1, valid); return err },
		"Cancel": func(s *Service, a domain.Principal) error { return s.Cancel(ctx, a, connA, 1) },
	}
	denied := map[string]domain.Principal{
		"view_only":                      viewer,
		"tanpa akses":                    outsider,
		"Platform Admin tanpa group":     superuser,
		"user yang tidak dikenal sistem": {UserID: 999},
	}
	for action, call := range actions {
		for who, actor := range denied {
			t.Run(action+"/"+who, func(t *testing.T) {
				store := &fakeStore{}
				svc, _ := newTestService(store)

				if err := call(svc, actor); !errors.Is(err, domain.ErrForbidden) {
					t.Errorf("err = %v, mau ErrForbidden", err)
				}
				if len(store.created)+len(store.updated)+len(store.cancelled) != 0 {
					t.Error("repo tidak boleh disentuh saat akses ditolak")
				}
			})
		}
	}
}

func TestService_AccessIsPerConnection(t *testing.T) {
	const connB int64 = 20
	store := &fakeStore{}
	svc, _ := newTestService(store)
	ctx := context.Background()
	valid := Input{Name: "x", SQL: "SELECT 1"}

	if _, err := svc.Create(ctx, editor, connA, valid); err != nil {
		t.Fatalf("editor di koneksi A harus boleh: %v", err)
	}
	if _, err := svc.Create(ctx, editor, connB, valid); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("editor di koneksi B (tanpa akses): err = %v, mau ErrForbidden", err)
	}
	if _, err := svc.List(ctx, editor, connB); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("List koneksi B: err = %v, mau ErrForbidden", err)
	}
	if len(store.created) != 1 || store.created[0].connID != connA {
		t.Errorf("created = %+v, mau hanya satu di koneksi A", store.created)
	}
}

func TestService_ForbiddenComesBeforeValidation(t *testing.T) {
	svc, _ := newTestService(&fakeStore{})
	_, err := svc.Create(context.Background(), viewer, connA, Input{})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, mau ErrForbidden (hak akses diperiksa sebelum validasi)", err)
	}
}

func TestService_ReadActionsAllowViewOnlyAndAbove(t *testing.T) {
	ctx := context.Background()
	for name, actor := range map[string]domain.Principal{"view_only": viewer, "view_edit": editor} {
		t.Run(name, func(t *testing.T) {
			store := &fakeStore{listed: []domain.Migration{{Version: 2}, {Version: 1}}, getResult: domain.Migration{Version: 1, Name: "satu"}}
			svc, _ := newTestService(store)

			list, err := svc.List(ctx, actor, connA)
			if err != nil || len(list) != 2 {
				t.Errorf("List = %v, %v", list, err)
			}
			m, err := svc.Get(ctx, actor, connA, 1)
			if err != nil || m.Name != "satu" {
				t.Errorf("Get = %+v, %v", m, err)
			}
		})
	}
}

func TestService_ReadActionsDenyWithoutAccessAndDoNotLeakExistence(t *testing.T) {
	ctx := context.Background()
	for name, actor := range map[string]domain.Principal{"tanpa akses": outsider, "Platform Admin tanpa group": superuser} {
		t.Run(name, func(t *testing.T) {
			store := &fakeStore{getResult: domain.Migration{Version: 1}}
			svc, _ := newTestService(store)

			if _, err := svc.List(ctx, actor, connA); !errors.Is(err, domain.ErrForbidden) {
				t.Errorf("List err = %v", err)
			}
			if _, err := svc.Get(ctx, actor, connA, 1); !errors.Is(err, domain.ErrForbidden) {
				t.Errorf("Get err = %v", err)
			}
			if _, err := svc.Get(ctx, actor, connA, 999); !errors.Is(err, domain.ErrForbidden) {
				t.Errorf("Get versi yang tidak ada harus tetap ErrForbidden, bukan ErrNotFound: %v", err)
			}
			if store.getCalls != 0 || store.listCalls != 0 {
				t.Error("repo tidak boleh disentuh saat akses ditolak")
			}
		})
	}
}

func TestService_AuthorizerFailureIsNotTurnedIntoForbidden(t *testing.T) {
	boom := errors.New("database platform tidak terjangkau")
	store := &fakeStore{}
	svc, authz := newTestService(store)
	authz.err = boom

	_, err := svc.Create(context.Background(), editor, connA, Input{Name: "x", SQL: "SELECT 1"})
	if !errors.Is(err, boom) || errors.Is(err, domain.ErrForbidden) {
		t.Errorf("err = %v, mau error asli yang dibungkus (bukan ErrForbidden)", err)
	}
	if _, err := svc.List(context.Background(), viewer, connA); !errors.Is(err, boom) {
		t.Errorf("List err = %v", err)
	}
	if len(store.created) != 0 {
		t.Error("repo tidak boleh disentuh saat pemeriksaan akses gagal")
	}
}

func TestService_Update(t *testing.T) {
	store := &fakeStore{}
	svc, _ := newTestService(store)

	m, err := svc.Update(context.Background(), editor, connA, 3, Input{Name: " baru ", SQL: " SELECT 2 "})
	if err != nil || m.Version != 3 {
		t.Fatalf("Update = %+v, %v", m, err)
	}
	want := updateArgs{connA, 3, "baru", "SELECT 2", Checksum("SELECT 2")}
	if len(store.updated) != 1 || store.updated[0] != want {
		t.Errorf("argumen repo = %+v, mau %+v", store.updated, want)
	}

	var verr *domain.ValidationError
	if _, err := svc.Update(context.Background(), editor, connA, 3, Input{Name: "x", SQL: "COMMIT"}); !errors.As(err, &verr) || verr.Fields["sql"] == "" {
		t.Errorf("SQL kontrol transaksi: err = %v", err)
	}
	if len(store.updated) != 1 {
		t.Error("update tidak valid tidak boleh sampai ke repo")
	}
}

func TestService_StoreErrorsPropagate(t *testing.T) {
	for _, storeErr := range []error{domain.ErrNotFound, domain.ErrMigrationNotDraft} {
		store := &fakeStore{err: storeErr}
		svc, _ := newTestService(store)
		valid := Input{Name: "x", SQL: "SELECT 1"}

		if _, err := svc.Update(context.Background(), editor, connA, 1, valid); !errors.Is(err, storeErr) {
			t.Errorf("Update err = %v, mau %v", err, storeErr)
		}
		if err := svc.Cancel(context.Background(), editor, connA, 1); !errors.Is(err, storeErr) {
			t.Errorf("Cancel err = %v, mau %v", err, storeErr)
		}
	}
	store := &fakeStore{err: domain.ErrNotFound}
	svc, _ := newTestService(store)
	if _, err := svc.Get(context.Background(), viewer, connA, 5); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get err = %v, mau ErrNotFound untuk user yang berhak", err)
	}
}

func TestService_Cancel(t *testing.T) {
	store := &fakeStore{}
	svc, _ := newTestService(store)
	if err := svc.Cancel(context.Background(), editor, connA, 4); err != nil {
		t.Fatal(err)
	}
	if len(store.cancelled) != 1 || store.cancelled[0] != [2]int64{connA, 4} {
		t.Errorf("cancelled = %v", store.cancelled)
	}
}
