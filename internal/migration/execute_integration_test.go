package migration_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/access"
	"dbplatform/internal/clock"
	"dbplatform/internal/domain"
	"dbplatform/internal/migration"
	"dbplatform/internal/testutil"
)

type execEnv struct {
	pool    *pgxpool.Pool
	target  *pgxpool.Pool
	svc     *migration.Service
	repo    *migration.Repo
	drivers *testutil.FakeDriverProvider
	clk     *clock.Fake
	editor  domain.Principal
	conn    int64
}

func newExecEnv(t *testing.T) execEnv {
	t.Helper()
	pool := testutil.NewAppDB(t)
	repo := migration.NewRepo(pool)
	drivers := testutil.NewFakeDriverProvider()
	clk := clock.NewFake(time.Now().Truncate(time.Microsecond))
	e := execEnv{
		pool: pool, repo: repo, drivers: drivers, clk: clk,
		svc:    migration.NewService(repo, access.New(pool), drivers, clk),
		target: testutil.NewTargetDB(t, `CREATE TABLE seed (id int)`),
		conn:   testutil.SeedConnection(t, pool, "sample", true),
	}
	drivers.Set(e.conn, testutil.NewPgDriver(e.target))

	u := testutil.SeedUser(t, pool, "penyunting", false)
	e.editor = domain.Principal{UserID: u.ID, Username: "penyunting"}
	g := testutil.SeedGroup(t, pool, "penyunting-grup", u.ID)
	testutil.Grant(t, pool, g, e.conn, domain.AccessViewEdit)
	return e
}

func (e execEnv) draft(t *testing.T, name, sql string) domain.Migration {
	t.Helper()
	m, err := e.svc.Create(context.Background(), e.editor, e.conn, migration.Input{Name: name, SQL: sql})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (e execEnv) tableExists(t *testing.T, name string) bool {
	t.Helper()
	var ok bool
	if err := e.target.QueryRow(context.Background(), `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	return ok
}

func (e execEnv) countRows(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := e.target.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestExecute_Integration_SuccessChangesTargetAndRecordsHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newExecEnv(t)
	m := e.draft(t, "tabel pelanggan", "CREATE TABLE pelanggan (id int PRIMARY KEY, nama text);\nINSERT INTO pelanggan VALUES (1, 'ani');\nINSERT INTO pelanggan VALUES (2, 'budi');")

	got, err := e.svc.Execute(ctx, e.editor, e.conn, m.Version)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.MigrationSuccess || got.ErrorMessage != "" || got.Warning != "" {
		t.Errorf("hasil = %+v", got)
	}
	if got.ExecutedBy == nil || *got.ExecutedBy != e.editor.UserID || got.ExecutedByName != "penyunting" {
		t.Errorf("pelaksana = %v %q", got.ExecutedBy, got.ExecutedByName)
	}
	if got.ExecutedAt == nil || got.FinishedAt == nil || got.FinishedAt.Before(*got.ExecutedAt) {
		t.Errorf("waktu: dieksekusi = %v, selesai = %v", got.ExecutedAt, got.FinishedAt)
	}
	if !e.tableExists(t, "pelanggan") || e.countRows(t, "pelanggan") != 2 {
		t.Error("target harus berubah: tabel pelanggan dengan 2 baris")
	}
}

func TestExecute_Integration_SecondStatementFailureRollsBackEverything(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newExecEnv(t)
	m := e.draft(t, "migration rusak", "CREATE TABLE baru (id int);\nINSERT INTO baru VALUES (1);\nINSERT INTO tidak_ada VALUES (1);")

	got, err := e.svc.Execute(ctx, e.editor, e.conn, m.Version)
	if err != nil {
		t.Fatalf("kegagalan di target dicatat sebagai hasil: %v", err)
	}
	if got.Status != domain.MigrationFailed || !strings.Contains(got.ErrorMessage, "tidak_ada") {
		t.Fatalf("hasil = %+v, mau failed dengan pesan error", got)
	}
	if got.FinishedAt == nil {
		t.Error("finished_at harus terisi")
	}

	if e.tableExists(t, "baru") {
		t.Fatal("ROLLBACK GAGAL: tabel 'baru' dari statement pertama masih ada di target")
	}
	if !e.tableExists(t, "seed") {
		t.Error("tabel yang sudah ada sebelumnya tidak boleh ikut hilang")
	}

	if _, err := e.svc.Execute(ctx, e.editor, e.conn, m.Version); !errors.Is(err, domain.ErrMigrationNotDraft) {
		t.Errorf("menjalankan ulang yang gagal: err = %v, mau ErrMigrationNotDraft", err)
	}
	next := e.draft(t, "perbaikan", "CREATE TABLE baru (id int)")
	if next.Version != m.Version+1 {
		t.Errorf("versi berikutnya = %d, mau %d: yang gagal tetap memakai nomornya", next.Version, m.Version+1)
	}
}

func TestExecute_Integration_FailureAfterDMLLeavesNoPartialData(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newExecEnv(t)
	m := e.draft(t, "data lalu error", "INSERT INTO seed VALUES (1); INSERT INTO seed VALUES (2); SELECT 1/0;")

	got, err := e.svc.Execute(ctx, e.editor, e.conn, m.Version)
	if err != nil || got.Status != domain.MigrationFailed {
		t.Fatalf("hasil = %+v, err = %v", got, err)
	}
	if n := e.countRows(t, "seed"); n != 0 {
		t.Errorf("seed berisi %d baris, mau 0: dua INSERT sebelum error harus ikut dibatalkan", n)
	}
}

func TestExecute_Integration_SyntaxErrorIsRecorded(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	e := newExecEnv(t)
	m := e.draft(t, "salah ketik", "CREAT TABLE x (id int)")

	got, err := e.svc.Execute(context.Background(), e.editor, e.conn, m.Version)
	if err != nil || got.Status != domain.MigrationFailed || got.ErrorMessage == "" {
		t.Errorf("hasil = %+v, err = %v", got, err)
	}
}

func TestExecute_Integration_VersionsMustRunInOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newExecEnv(t)
	e.draft(t, "satu", "CREATE TABLE t1 (id int)")
	e.draft(t, "dua", "CREATE TABLE t2 (id int)")

	_, err := e.svc.Execute(ctx, e.editor, e.conn, 2)
	var ooo *migration.OutOfOrderError
	if !errors.As(err, &ooo) || ooo.Blocking != 1 {
		t.Fatalf("err = %v, mau OutOfOrderError yang menyebut versi 1", err)
	}
	if e.tableExists(t, "t2") {
		t.Error("versi 2 tidak boleh menyentuh target")
	}
	if got, _ := e.repo.Get(ctx, e.conn, 2); got.Status != domain.MigrationDraft {
		t.Errorf("versi 2 harus tetap draft, dapat %s", got.Status)
	}

	if got, err := e.svc.Execute(ctx, e.editor, e.conn, 1); err != nil || got.Status != domain.MigrationSuccess {
		t.Fatalf("versi 1: %+v, %v", got, err)
	}
	if got, err := e.svc.Execute(ctx, e.editor, e.conn, 2); err != nil || got.Status != domain.MigrationSuccess {
		t.Fatalf("versi 2 setelah versi 1: %+v, %v", got, err)
	}
	if !e.tableExists(t, "t1") || !e.tableExists(t, "t2") {
		t.Error("kedua tabel harus ada")
	}
}

func TestExecute_Integration_CannotRunTwice(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newExecEnv(t)
	m := e.draft(t, "sekali saja", "INSERT INTO seed VALUES (1)")

	if _, err := e.svc.Execute(ctx, e.editor, e.conn, m.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Execute(ctx, e.editor, e.conn, m.Version); !errors.Is(err, domain.ErrMigrationNotDraft) {
		t.Errorf("err = %v, mau ErrMigrationNotDraft", err)
	}
	if n := e.countRows(t, "seed"); n != 1 {
		t.Errorf("seed = %d baris, mau 1: tidak boleh dijalankan dua kali", n)
	}
	if _, err := e.svc.Update(ctx, e.editor, e.conn, m.Version, migration.Input{Name: "ubah", SQL: "SELECT 1"}); !errors.Is(err, domain.ErrMigrationNotDraft) {
		t.Errorf("mengubah yang sudah dieksekusi: err = %v", err)
	}
}

func TestExecute_Integration_ConcurrentExecutionRunsExactlyOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newExecEnv(t)
	m := e.draft(t, "satu kali", "INSERT INTO seed VALUES (1)")

	const workers = 6
	results := make([]error, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, results[i] = e.svc.Execute(ctx, e.editor, e.conn, m.Version)
		}()
	}
	close(start)
	wg.Wait()

	var ok, refused int
	for _, err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, domain.ErrMigrationNotDraft):
			refused++
		default:
			t.Fatalf("error tak terduga: %v", err)
		}
	}
	if ok != 1 || refused != workers-1 {
		t.Errorf("berhasil = %d, ditolak = %d; mau 1 dan %d", ok, refused, workers-1)
	}
	if n := e.countRows(t, "seed"); n != 1 {
		t.Errorf("INSERT berjalan %d kali, mau tepat 1", n)
	}
}

func TestExecute_Integration_ViewOnlyAndOutsidersCannotExecute(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newExecEnv(t)
	m := e.draft(t, "bahaya", "DROP TABLE seed")

	reader := testutil.SeedUser(t, e.pool, "pembaca", false)
	g := testutil.SeedGroup(t, e.pool, "pembaca-grup", reader.ID)
	testutil.Grant(t, e.pool, g, e.conn, domain.AccessViewOnly)
	admin := testutil.SeedUser(t, e.pool, "root", true)
	loner := testutil.SeedUser(t, e.pool, "sendirian", false)

	for name, actor := range map[string]domain.Principal{
		"view_only":                  {UserID: reader.ID, Username: "pembaca"},
		"Platform Admin tanpa group": {UserID: admin.ID, IsPlatformAdmin: true},
		"user tanpa group":           {UserID: loner.ID},
	} {
		if _, err := e.svc.Execute(ctx, actor, e.conn, m.Version); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("%s: err = %v, mau ErrForbidden", name, err)
		}
	}
	if !e.tableExists(t, "seed") {
		t.Fatal("DROP TABLE tidak boleh pernah sampai ke target")
	}
	if got, _ := e.repo.Get(ctx, e.conn, m.Version); got.Status != domain.MigrationDraft || got.ExecutedBy != nil {
		t.Errorf("status harus tetap draft tanpa pelaksana: %+v", got)
	}
}

func TestExecute_Integration_TargetUnavailableIsRecordedAndDoesNotBlockLaterVersions(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newExecEnv(t)
	orphan := testutil.SeedConnection(t, e.pool, "tanpa-driver", true)
	g := testutil.SeedGroup(t, e.pool, "grup-orphan", e.editor.UserID)
	testutil.Grant(t, e.pool, g, orphan, domain.AccessViewEdit)

	first, err := e.svc.Create(ctx, e.editor, orphan, migration.Input{Name: "satu", SQL: "CREATE TABLE a (id int)"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Create(ctx, e.editor, orphan, migration.Input{Name: "dua", SQL: "CREATE TABLE b (id int)"}); err != nil {
		t.Fatal(err)
	}

	got, err := e.svc.Execute(ctx, e.editor, orphan, first.Version)
	if err != nil || got.Status != domain.MigrationFailed || got.ErrorMessage == "" {
		t.Fatalf("hasil = %+v, err = %v", got, err)
	}
	e.drivers.Set(orphan, testutil.NewPgDriver(e.target))
	if got, err := e.svc.Execute(ctx, e.editor, orphan, 2); err != nil || got.Status != domain.MigrationSuccess {
		t.Errorf("versi yang gagal tidak boleh menghalangi versi berikutnya: %+v, %v", got, err)
	}
}

func TestExecute_Integration_RecoverStuck(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newExecEnv(t)
	stale := e.draft(t, "terputus", "SELECT 1")
	e.draft(t, "menunggu", "SELECT 2")

	_, err := e.pool.Exec(ctx, `
		UPDATE db_migrations SET status = 'running', executed_at = $3
		WHERE connection_id = $1 AND version = $2`, e.conn, stale.Version, e.clk.Now().Add(-10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Execute(ctx, e.editor, e.conn, 2); !errors.Is(err, domain.ErrMigrationOutOfOrder) {
		t.Fatalf("selama versi 1 running, versi 2 harus menunggu: err = %v", err)
	}

	n, err := e.svc.RecoverStuck(ctx)
	if err != nil || n != 1 {
		t.Fatalf("RecoverStuck = %d, %v; mau 1", n, err)
	}
	got, _ := e.repo.Get(ctx, e.conn, stale.Version)
	if got.Status != domain.MigrationFailed || !strings.Contains(got.ErrorMessage, "periksa manual") || got.FinishedAt == nil {
		t.Errorf("yang terputus = %+v", got)
	}
	if got, err := e.svc.Execute(ctx, e.editor, e.conn, 2); err != nil || got.Status != domain.MigrationSuccess {
		t.Errorf("setelah pemulihan, versi 2 harus bisa jalan: %+v, %v", got, err)
	}
}

func TestExecute_Integration_RecoverStuckLeavesFreshRunningAlone(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newExecEnv(t)
	m := e.draft(t, "baru berjalan", "SELECT 1")
	_, err := e.pool.Exec(ctx, `
		UPDATE db_migrations SET status = 'running', executed_at = $3
		WHERE connection_id = $1 AND version = $2`, e.conn, m.Version, e.clk.Now().Add(-1*time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	if n, err := e.svc.RecoverStuck(ctx); err != nil || n != 0 {
		t.Errorf("RecoverStuck = %d, %v; mau 0", n, err)
	}
	if got, _ := e.repo.Get(ctx, e.conn, m.Version); got.Status != domain.MigrationRunning {
		t.Errorf("yang baru berjalan tidak boleh disentuh: %s", got.Status)
	}
}

func TestExecute_Integration_TimestampsComeFromClock(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	e := newExecEnv(t)
	fixed := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	e.clk = clock.NewFake(fixed)
	svc := migration.NewService(e.repo, access.New(e.pool), e.drivers, e.clk)
	m := e.draft(t, "waktu tetap", "SELECT 1")

	got, err := svc.Execute(context.Background(), e.editor, e.conn, m.Version)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExecutedAt == nil || !got.ExecutedAt.Equal(fixed) || got.FinishedAt == nil || !got.FinishedAt.Equal(fixed) {
		t.Errorf("executed_at = %v, finished_at = %v, mau %v", got.ExecutedAt, got.FinishedAt, fixed)
	}
}
