package migration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"dbplatform/internal/clock"
	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
)

type fakeTx struct{ d *fakeDriver }

func (t fakeTx) Exec(_ context.Context, sql string) (int64, error) {
	t.d.executed = append(t.d.executed, sql)
	return 0, t.d.execErr
}

func (t fakeTx) Query(context.Context, string) (*domain.ResultSet, error) {
	return nil, domain.ErrUnsupported
}

type fakeDriver struct {
	transactionalDDL bool
	execErr          error
	executed         []string
	txCalls          int
	txTimeout        time.Duration
	ctxErrAtTx       error
}

func (d *fakeDriver) Dialect() domain.DBMS { return domain.DBMSPostgres }
func (d *fakeDriver) Capabilities() dbdriver.Capabilities {
	return dbdriver.Capabilities{TransactionalDDL: d.transactionalDDL}
}
func (d *fakeDriver) Ping(context.Context) error { return nil }
func (d *fakeDriver) Close()                     {}
func (d *fakeDriver) Introspect(context.Context) (*domain.Schema, error) {
	return nil, domain.ErrUnsupported
}
func (d *fakeDriver) TableData(context.Context, domain.TableRef, domain.PageRequest) (*domain.TableData, error) {
	return nil, domain.ErrUnsupported
}

func (d *fakeDriver) WithTx(ctx context.Context, opts dbdriver.TxOptions, fn func(dbdriver.Tx) error) error {
	d.txCalls++
	d.txTimeout = opts.Timeout
	d.ctxErrAtTx = ctx.Err()
	return fn(fakeTx{d})
}

type fakeProvider struct {
	driver dbdriver.Driver
	err    error
	calls  int
}

func (p *fakeProvider) Get(context.Context, int64) (dbdriver.Driver, error) {
	p.calls++
	return p.driver, p.err
}

var fixedNow = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

type execFixture struct {
	svc      *Service
	store    *fakeStore
	driver   *fakeDriver
	provider *fakeProvider
	clk      *clock.Fake
}

func newExecFixture(sql string) execFixture {
	store := &fakeStore{
		claimResult: domain.Migration{ConnectionID: connA, Version: 1, SQL: sql, Status: domain.MigrationRunning},
		getResult:   domain.Migration{ConnectionID: connA, Version: 1, SQL: sql, Status: domain.MigrationSuccess},
	}
	driver := &fakeDriver{transactionalDDL: true}
	provider := &fakeProvider{driver: driver}
	clk := clock.NewFake(fixedNow)
	authz := &fakeAuthz{levels: map[[2]int64]domain.AccessLevel{
		{editor.UserID, connA}: domain.AccessViewEdit,
		{viewer.UserID, connA}: domain.AccessViewOnly,
	}}
	return execFixture{svc: NewService(store, authz, provider, clk), store: store, driver: driver, provider: provider, clk: clk}
}

func TestExecute_Success(t *testing.T) {
	sql := "CREATE TABLE a (id int);\nINSERT INTO a VALUES (1)"
	f := newExecFixture(sql)

	m, err := f.svc.Execute(context.Background(), editor, connA, 1)
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != domain.MigrationSuccess {
		t.Errorf("hasil dikembalikan dari store: %+v", m)
	}
	if len(f.store.claims) != 1 || f.store.claims[0] != (claimArgs{connA, 1, editor.UserID, fixedNow}) {
		t.Errorf("claims = %+v", f.store.claims)
	}
	if len(f.driver.executed) != 1 || f.driver.executed[0] != sql || f.driver.txCalls != 1 {
		t.Errorf("SQL harus dikirim utuh dalam satu transaction: executed = %q, tx = %d", f.driver.executed, f.driver.txCalls)
	}
	if f.driver.txTimeout != 60*time.Second {
		t.Errorf("timeout = %v, mau 60s", f.driver.txTimeout)
	}
	if len(f.store.finishes) != 1 || f.store.finishes[0] != (finishArgs{connA, 1, domain.MigrationSuccess, "", "", fixedNow}) {
		t.Errorf("finishes = %+v", f.store.finishes)
	}
}

func TestExecute_RequiresViewEditBeforeAnythingElse(t *testing.T) {
	for name, actor := range map[string]domain.Principal{"view_only": viewer, "tanpa akses": outsider, "Platform Admin tanpa group": superuser} {
		t.Run(name, func(t *testing.T) {
			f := newExecFixture("SELECT 1")
			if _, err := f.svc.Execute(context.Background(), actor, connA, 1); !errors.Is(err, domain.ErrForbidden) {
				t.Fatalf("err = %v, mau ErrForbidden", err)
			}
			if len(f.store.claims) != 0 || f.provider.calls != 0 || f.driver.txCalls != 0 || len(f.store.finishes) != 0 {
				t.Error("tidak ada yang boleh disentuh saat akses ditolak")
			}
		})
	}
}

func TestExecute_ClaimRefusalsPropagateWithoutTouchingTarget(t *testing.T) {
	for _, claimErr := range []error{domain.ErrNotFound, domain.ErrMigrationNotDraft, &OutOfOrderError{Blocking: 2}} {
		t.Run(claimErr.Error(), func(t *testing.T) {
			f := newExecFixture("SELECT 1")
			f.store.claimErr = claimErr

			_, err := f.svc.Execute(context.Background(), editor, connA, 3)
			if !errors.Is(err, claimErr) {
				t.Fatalf("err = %v, mau %v", err, claimErr)
			}
			if f.provider.calls != 0 || f.driver.txCalls != 0 || len(f.store.finishes) != 0 {
				t.Error("database target dan pencatatan hasil tidak boleh disentuh")
			}
		})
	}
}

func TestExecute_StatementFailureIsRecordedAsFailedNotReturnedAsError(t *testing.T) {
	f := newExecFixture("CREATE TABLE a (id int); INSERT INTO tidak_ada VALUES (1)")
	f.driver.execErr = errors.New(`ERROR: relation "tidak_ada" does not exist (SQLSTATE 42P01)`)
	f.store.getResult.Status = domain.MigrationFailed

	m, err := f.svc.Execute(context.Background(), editor, connA, 1)
	if err != nil {
		t.Fatalf("kegagalan di target dicatat sebagai hasil, bukan error: %v", err)
	}
	if m.Status != domain.MigrationFailed {
		t.Errorf("status = %s", m.Status)
	}
	got := f.store.finishes[0]
	if got.status != domain.MigrationFailed || !strings.Contains(got.errMsg, "tidak_ada") || got.warning != "" {
		t.Errorf("finish = %+v", got)
	}
}

func TestExecute_LongErrorIsTruncated(t *testing.T) {
	f := newExecFixture("SELECT 1")
	f.driver.execErr = errors.New(strings.Repeat("é", 2500))

	if _, err := f.svc.Execute(context.Background(), editor, connA, 1); err != nil {
		t.Fatal(err)
	}
	msg := f.store.finishes[0].errMsg
	if n := len([]rune(msg)); n != 2000 {
		t.Errorf("panjang pesan = %d karakter, mau 2000", n)
	}
	if strings.ContainsRune(msg, '�') {
		t.Error("pemotongan tidak boleh merusak karakter multibyte")
	}
}

func TestExecute_TargetUnavailableIsRecordedAsFailed(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantMsg string
	}{
		{"koneksi gagal", fmt.Errorf("%w: host tidak terjangkau", domain.ErrConnectionFailed), "host tidak terjangkau"},
		{"koneksi nonaktif", domain.ErrConnectionInactive, msgTargetInactive},
		{"tidak ditemukan", domain.ErrNotFound, msgTargetUnavailable},
		{"error lain", errors.New("pool: password=hunter2 host=10.0.0.5"), msgTargetUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newExecFixture("SELECT 1")
			f.provider.err = tt.err

			if _, err := f.svc.Execute(context.Background(), editor, connA, 1); err != nil {
				t.Fatalf("err = %v", err)
			}
			got := f.store.finishes[0]
			if got.status != domain.MigrationFailed || !strings.Contains(got.errMsg, tt.wantMsg) {
				t.Errorf("finish = %+v, mau pesan memuat %q", got, tt.wantMsg)
			}
			if strings.Contains(got.errMsg, "hunter2") || strings.Contains(got.errMsg, "10.0.0.5") {
				t.Error("detail internal tidak boleh masuk ke pesan yang tersimpan")
			}
			if f.driver.txCalls != 0 {
				t.Error("tanpa driver, transaction tidak boleh dimulai")
			}
		})
	}
}

func TestExecute_SQLRevalidatedAtExecutionTime(t *testing.T) {
	f := newExecFixture("CREATE TABLE a (id int); COMMIT")

	if _, err := f.svc.Execute(context.Background(), editor, connA, 1); err != nil {
		t.Fatal(err)
	}
	got := f.store.finishes[0]
	if got.status != domain.MigrationFailed || !strings.Contains(got.errMsg, "kontrol transaksi") {
		t.Errorf("finish = %+v", got)
	}
	if f.provider.calls != 0 || f.driver.txCalls != 0 {
		t.Error("SQL yang tidak lolos validasi tidak boleh menyentuh target")
	}
}

func TestExecute_DDLWarningOnlyForNonTransactionalDDL(t *testing.T) {
	tests := []struct {
		name          string
		sql           string
		transactional bool
		execErr       error
		wantWarning   bool
	}{
		{"ddl tanpa rollback ddl", "CREATE TABLE a (id int)", false, nil, true},
		{"ddl tanpa rollback ddl lalu gagal", "ALTER TABLE a ADD COLUMN b int", false, errors.New("gagal"), true},
		{"dml tanpa rollback ddl", "INSERT INTO a VALUES (1)", false, nil, false},
		{"ddl dengan rollback ddl", "CREATE TABLE a (id int)", true, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newExecFixture(tt.sql)
			f.driver.transactionalDDL = tt.transactional
			f.driver.execErr = tt.execErr

			if _, err := f.svc.Execute(context.Background(), editor, connA, 1); err != nil {
				t.Fatal(err)
			}
			warning := f.store.finishes[0].warning
			if (warning != "") != tt.wantWarning || (tt.wantWarning && warning != msgDDLWarning) {
				t.Errorf("warning = %q, wantWarning = %v", warning, tt.wantWarning)
			}
		})
	}
}

func TestExecute_CompletesEvenIfRequestIsCancelled(t *testing.T) {
	f := newExecFixture("CREATE TABLE a (id int)")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := f.svc.Execute(ctx, editor, connA, 1); err != nil {
		t.Fatal(err)
	}
	if f.driver.ctxErrAtTx != nil {
		t.Errorf("konteks transaction ikut terbatalkan: %v", f.driver.ctxErrAtTx)
	}
	if len(f.store.finishes) != 1 || f.store.finishes[0].status != domain.MigrationSuccess {
		t.Errorf("hasil harus tetap dicatat: %+v", f.store.finishes)
	}
}

func TestExecute_FinishFailureIsReported(t *testing.T) {
	boom := errors.New("tabel db_migrations tidak terjangkau")
	f := newExecFixture("SELECT 1")
	f.store.finishErr = boom

	if _, err := f.svc.Execute(context.Background(), editor, connA, 1); !errors.Is(err, boom) {
		t.Errorf("err = %v, kegagalan mencatat hasil harus dilaporkan", err)
	}
}

func TestExecute_UsesClockForTimestamps(t *testing.T) {
	f := newExecFixture("SELECT 1")
	f.clk.Advance(36 * time.Hour)

	if _, err := f.svc.Execute(context.Background(), editor, connA, 1); err != nil {
		t.Fatal(err)
	}
	want := fixedNow.Add(36 * time.Hour)
	if !f.store.claims[0].now.Equal(want) || !f.store.finishes[0].finishedAt.Equal(want) {
		t.Errorf("claim = %v, finish = %v, mau %v", f.store.claims[0].now, f.store.finishes[0].finishedAt, want)
	}
}

func TestRecoverStuck(t *testing.T) {
	f := newExecFixture("SELECT 1")
	f.store.stuckCount = 3

	n, err := f.svc.RecoverStuck(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("n = %d, err = %v", n, err)
	}
	got := f.store.stuckCalls[0]
	if !got.before.Equal(fixedNow.Add(-5*time.Minute)) || !got.finishedAt.Equal(fixedNow) || got.message != msgInterrupted {
		t.Errorf("FailStuck dipanggil dengan %+v", got)
	}

	boom := errors.New("gagal")
	f.store.stuckErr = boom
	if _, err := f.svc.RecoverStuck(context.Background()); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}
