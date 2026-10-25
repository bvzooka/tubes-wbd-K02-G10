package migration_test

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/domain"
	"dbplatform/internal/migration"
	"dbplatform/internal/testutil"
)

type repoEnv struct {
	pool *pgxpool.Pool
	repo *migration.Repo
	user domain.User
	conn int64
}

func newRepoEnv(t *testing.T) repoEnv {
	t.Helper()
	pool := testutil.NewAppDB(t)
	return repoEnv{
		pool: pool,
		repo: migration.NewRepo(pool),
		user: testutil.SeedUser(t, pool, "budi", false),
		conn: testutil.SeedConnection(t, pool, "sample", true),
	}
}

func (e repoEnv) create(t *testing.T, connID int64, name, sql string) domain.Migration {
	t.Helper()
	m, err := e.repo.Create(context.Background(), connID, name, sql, migration.Checksum(sql), e.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (e repoEnv) setStatus(t *testing.T, connID int64, version int, status string) {
	t.Helper()
	_, err := e.pool.Exec(context.Background(),
		`UPDATE db_migrations SET status = $3 WHERE connection_id = $1 AND version = $2`, connID, version, status)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRepo_Integration_CreateStoresDraftWithLinearVersions(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	e := newRepoEnv(t)
	other := testutil.SeedConnection(t, e.pool, "lain", true)

	a := e.create(t, e.conn, "satu", "CREATE TABLE a (id int)")
	b := e.create(t, e.conn, "dua", "CREATE TABLE b (id int)")
	c := e.create(t, other, "koneksi lain", "CREATE TABLE c (id int)")

	if a.Version != 1 || b.Version != 2 || c.Version != 1 {
		t.Errorf("versi = %d, %d, %d; mau 1, 2, 1 (terpisah per koneksi)", a.Version, b.Version, c.Version)
	}
	if a.Status != domain.MigrationDraft || a.Name != "satu" || a.SQL != "CREATE TABLE a (id int)" {
		t.Errorf("migration = %+v", a)
	}
	if a.Checksum != migration.Checksum("CREATE TABLE a (id int)") {
		t.Errorf("checksum = %s", a.Checksum)
	}
	if a.CreatedBy != e.user.ID || a.CreatedByName != "budi" || a.CreatedAt.IsZero() || a.ID == 0 {
		t.Errorf("pembuat atau waktu tidak terisi: %+v", a)
	}
	if a.ExecutedBy != nil || a.ExecutedAt != nil || a.FinishedAt != nil || a.ExecutedByName != "" {
		t.Errorf("draft belum dieksekusi: %+v", a)
	}
}

func TestRepo_Integration_ConcurrentCreatesGetGaplessUniqueVersions(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newRepoEnv(t)
	other := testutil.SeedConnection(t, e.pool, "lain", true)

	const perConn = 10
	var wg sync.WaitGroup
	var mu sync.Mutex
	versions := map[int64][]int{}
	var failures []error
	for _, connID := range []int64{e.conn, other} {
		for i := 0; i < perConn; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				m, err := e.repo.Create(ctx, connID, "paralel", "SELECT 1", migration.Checksum("SELECT 1"), e.user.ID)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					failures = append(failures, err)
					return
				}
				versions[connID] = append(versions[connID], m.Version)
			}()
		}
	}
	wg.Wait()

	if len(failures) > 0 {
		t.Fatalf("pembuatan paralel gagal: %v", failures)
	}
	for connID, got := range versions {
		sort.Ints(got)
		if len(got) != perConn {
			t.Fatalf("koneksi %d: %d migration, mau %d", connID, len(got), perConn)
		}
		for i, v := range got {
			if v != i+1 {
				t.Errorf("koneksi %d: versi = %v, mau 1 sampai %d tanpa lubang dan tanpa duplikat", connID, got, perConn)
				break
			}
		}
	}
}

func TestRepo_Integration_CreateForUnknownConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	e := newRepoEnv(t)
	_, err := e.repo.Create(context.Background(), 424242, "x", "SELECT 1", migration.Checksum("SELECT 1"), e.user.ID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, mau ErrNotFound", err)
	}
}

func TestRepo_Integration_GetIsScopedToConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newRepoEnv(t)
	other := testutil.SeedConnection(t, e.pool, "lain", true)
	created := e.create(t, e.conn, "rahasia", "SELECT 1")

	got, err := e.repo.Get(ctx, e.conn, created.Version)
	if err != nil || got.Name != "rahasia" || got.SQL != "SELECT 1" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if _, err := e.repo.Get(ctx, other, created.Version); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("koneksi lain: err = %v, mau ErrNotFound (IDOR)", err)
	}
	if _, err := e.repo.Get(ctx, e.conn, 99); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("versi tak ada: err = %v", err)
	}
}

func TestRepo_Integration_ListNewestFirstWithoutSQLText(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newRepoEnv(t)
	other := testutil.SeedConnection(t, e.pool, "lain", true)
	e.create(t, e.conn, "satu", "SELECT 1")
	e.create(t, e.conn, "dua", "SELECT 2")
	e.create(t, other, "milik lain", "SELECT 3")

	list, err := e.repo.List(ctx, e.conn)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Version != 2 || list[1].Version != 1 {
		t.Fatalf("List = %+v, mau versi 2 lalu 1 dan tanpa milik koneksi lain", list)
	}
	if list[0].SQL != "" {
		t.Error("daftar tidak perlu memuat teks SQL")
	}
	if list[0].CreatedByName != "budi" || list[0].Name != "dua" {
		t.Errorf("baris = %+v", list[0])
	}

	empty, err := e.repo.List(ctx, 424242)
	if err != nil || len(empty) != 0 {
		t.Errorf("koneksi tanpa migration: %v, %v", empty, err)
	}
}

func TestRepo_Integration_UpdateDraft(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newRepoEnv(t)
	other := testutil.SeedConnection(t, e.pool, "lain", true)
	created := e.create(t, e.conn, "lama", "SELECT 1")
	time.Sleep(15 * time.Millisecond)

	got, err := e.repo.UpdateDraft(ctx, e.conn, created.Version, "baru", "SELECT 2", migration.Checksum("SELECT 2"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "baru" || got.SQL != "SELECT 2" || got.Checksum != migration.Checksum("SELECT 2") {
		t.Errorf("setelah ubah = %+v", got)
	}
	if !got.UpdatedAt.After(created.UpdatedAt) || !got.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("updated_at harus maju dan created_at tetap: %v -> %v", created.UpdatedAt, got.UpdatedAt)
	}

	if _, err := e.repo.UpdateDraft(ctx, other, created.Version, "retas", "DROP TABLE x", "x"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("koneksi lain: err = %v, mau ErrNotFound (IDOR)", err)
	}
	if untouched, _ := e.repo.Get(ctx, e.conn, created.Version); untouched.Name != "baru" {
		t.Errorf("migration milik koneksi lain tidak boleh berubah: %+v", untouched)
	}
	if _, err := e.repo.UpdateDraft(ctx, e.conn, 99, "x", "SELECT 1", "x"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("versi tak ada: err = %v", err)
	}
}

func TestRepo_Integration_OnlyDraftsCanBeUpdatedOrCancelled(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newRepoEnv(t)

	for _, status := range []string{"running", "success", "failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			m := e.create(t, e.conn, "x", "SELECT 1")
			e.setStatus(t, e.conn, m.Version, status)

			if _, err := e.repo.UpdateDraft(ctx, e.conn, m.Version, "ubah", "SELECT 2", "c"); !errors.Is(err, domain.ErrMigrationNotDraft) {
				t.Errorf("UpdateDraft: err = %v, mau ErrMigrationNotDraft", err)
			}
			if err := e.repo.Cancel(ctx, e.conn, m.Version); !errors.Is(err, domain.ErrMigrationNotDraft) {
				t.Errorf("Cancel: err = %v, mau ErrMigrationNotDraft", err)
			}
			got, _ := e.repo.Get(ctx, e.conn, m.Version)
			if got.Name != "x" || got.SQL != "SELECT 1" || string(got.Status) != status {
				t.Errorf("migration berubah padahal ditolak: %+v", got)
			}
			e.setStatus(t, e.conn, m.Version, "draft")
		})
	}
}

func TestRepo_Integration_Cancel(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newRepoEnv(t)
	other := testutil.SeedConnection(t, e.pool, "lain", true)
	m := e.create(t, e.conn, "x", "SELECT 1")

	if err := e.repo.Cancel(ctx, other, m.Version); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("koneksi lain: err = %v, mau ErrNotFound (IDOR)", err)
	}
	if got, _ := e.repo.Get(ctx, e.conn, m.Version); got.Status != domain.MigrationDraft {
		t.Errorf("status = %s, harus tetap draft", got.Status)
	}

	if err := e.repo.Cancel(ctx, e.conn, m.Version); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.repo.Get(ctx, e.conn, m.Version); got.Status != domain.MigrationCancelled {
		t.Errorf("status = %s, mau cancelled", got.Status)
	}
	if err := e.repo.Cancel(ctx, e.conn, m.Version); !errors.Is(err, domain.ErrMigrationNotDraft) {
		t.Errorf("batal dua kali: err = %v", err)
	}
	if err := e.repo.Cancel(ctx, e.conn, 99); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("versi tak ada: err = %v", err)
	}
}

func TestRepo_Integration_VersionNumbersAreNeverReused(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newRepoEnv(t)

	first := e.create(t, e.conn, "satu", "SELECT 1")
	second := e.create(t, e.conn, "dua", "SELECT 2")
	if err := e.repo.Cancel(ctx, e.conn, second.Version); err != nil {
		t.Fatal(err)
	}
	e.setStatus(t, e.conn, first.Version, "failed")

	next := e.create(t, e.conn, "tiga", "SELECT 3")
	if next.Version != 3 {
		t.Errorf("versi = %d, mau 3: migration yang dibatalkan atau gagal tetap memakai nomornya", next.Version)
	}
}
