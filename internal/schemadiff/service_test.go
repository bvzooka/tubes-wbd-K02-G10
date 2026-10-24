package schemadiff

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
	"dbplatform/internal/migration"
)

const (
	targetID int64 = 10
	sourceID int64 = 20
	otherID  int64 = 30
	mysqlID  int64 = 40
)

var (
	editorUser   = domain.Principal{UserID: 1, Username: "penyunting"}
	readerUser   = domain.Principal{UserID: 2, Username: "pembaca"}
	noSourceUser = domain.Principal{UserID: 3, Username: "tanpa-sumber"}
	rootUser     = domain.Principal{UserID: 4, Username: "root", IsPlatformAdmin: true}
)

type svcAuthz struct {
	levels     map[[2]int64]domain.AccessLevel
	accessible []domain.AccessibleDatabase
	err        error
}

func (a *svcAuthz) EffectiveLevel(_ context.Context, userID, connID int64) (domain.AccessLevel, error) {
	return a.levels[[2]int64{userID, connID}], a.err
}

func (a *svcAuthz) Can(_ context.Context, userID, connID int64, min domain.AccessLevel) (bool, error) {
	if a.err != nil {
		return false, a.err
	}
	return a.levels[[2]int64{userID, connID}].AtLeast(min), nil
}

func (a *svcAuthz) ListAccessible(context.Context, int64) ([]domain.AccessibleDatabase, error) {
	return a.accessible, a.err
}

type svcDriver struct {
	dialect    domain.DBMS
	schema     *domain.Schema
	err        error
	introspect int
}

func (d *svcDriver) Dialect() domain.DBMS                { return d.dialect }
func (d *svcDriver) Capabilities() dbdriver.Capabilities { return dbdriver.Capabilities{} }
func (d *svcDriver) Ping(context.Context) error          { return nil }
func (d *svcDriver) Close()                              {}
func (d *svcDriver) TableData(context.Context, domain.TableRef, domain.PageRequest) (*domain.TableData, error) {
	return nil, domain.ErrUnsupported
}

func (d *svcDriver) WithTx(context.Context, dbdriver.TxOptions, func(dbdriver.Tx) error) error {
	return domain.ErrUnsupported
}

func (d *svcDriver) Introspect(context.Context) (*domain.Schema, error) {
	d.introspect++
	return d.schema, d.err
}

type svcProvider struct {
	drivers map[int64]*svcDriver
	errs    map[int64]error
	calls   int
}

func (p *svcProvider) Get(_ context.Context, id int64) (dbdriver.Driver, error) {
	p.calls++
	if err := p.errs[id]; err != nil {
		return nil, err
	}
	d, ok := p.drivers[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return d, nil
}

type svcMigrations struct {
	calls []migration.Input
	conns []int64
	err   error
}

func (m *svcMigrations) Create(_ context.Context, _ domain.Principal, connID int64, in migration.Input) (domain.Migration, error) {
	if m.err != nil {
		return domain.Migration{}, m.err
	}
	m.calls = append(m.calls, in)
	m.conns = append(m.conns, connID)
	return domain.Migration{ConnectionID: connID, Version: len(m.calls), Name: in.Name, SQL: in.SQL, Status: domain.MigrationDraft}, nil
}

type svcFixture struct {
	svc        *Service
	authz      *svcAuthz
	provider   *svcProvider
	migrations *svcMigrations
}

func orders() domain.Table {
	return table("public", "orders",
		[]domain.Column{col(1, "id", "bigint", false, nil), col(2, "total", "numeric(12,2)", false, nil)},
		[]domain.Constraint{pk("orders_pkey", "id")}, nil)
}

func newSvcFixture() svcFixture {
	authz := &svcAuthz{
		levels: map[[2]int64]domain.AccessLevel{
			{editorUser.UserID, targetID}:   domain.AccessViewEdit,
			{editorUser.UserID, sourceID}:   domain.AccessViewOnly,
			{editorUser.UserID, otherID}:    domain.AccessViewEdit,
			{editorUser.UserID, mysqlID}:    domain.AccessViewOnly,
			{readerUser.UserID, targetID}:   domain.AccessViewOnly,
			{readerUser.UserID, sourceID}:   domain.AccessViewOnly,
			{noSourceUser.UserID, targetID}: domain.AccessViewEdit,
		},
		accessible: []domain.AccessibleDatabase{
			{ConnectionID: targetID, Name: "tujuan", DBMS: domain.DBMSPostgres, Level: domain.AccessViewEdit},
			{ConnectionID: sourceID, Name: "sumber <asli>", DBMS: domain.DBMSPostgres, Level: domain.AccessViewOnly},
			{ConnectionID: otherID, Name: "lain", DBMS: domain.DBMSPostgres, Level: domain.AccessViewEdit},
			{ConnectionID: mysqlID, Name: "mysql", DBMS: domain.DBMSMySQL, Level: domain.AccessViewOnly},
			{ConnectionID: 50, Name: "tidak-berhak", DBMS: domain.DBMSPostgres, Level: domain.AccessNone},
		},
	}
	provider := &svcProvider{
		drivers: map[int64]*svcDriver{
			targetID: {dialect: domain.DBMSPostgres, schema: schema(customers())},
			sourceID: {dialect: domain.DBMSPostgres, schema: schema(customers(), orders())},
			mysqlID:  {dialect: domain.DBMSMySQL, schema: schema()},
		},
		errs: map[int64]error{},
	}
	migrations := &svcMigrations{}
	return svcFixture{svc: NewService(authz, provider, migrations), authz: authz, provider: provider, migrations: migrations}
}

func TestService_Preview_DirectionIsSourceIntoTarget(t *testing.T) {
	f := newSvcFixture()
	r, err := f.svc.Preview(context.Background(), editorUser, targetID, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) == 0 || r.Changes[0].Kind != KindCreateTable || !strings.Contains(r.Changes[0].SQL, `"orders"`) {
		t.Fatalf("sumber punya tabel orders yang tidak ada di target, harus menghasilkan create_table: %v", kinds(r.Changes))
	}
	if r.TargetName != "tujuan" || r.SourceName != "sumber <asli>" || r.TargetID != targetID || r.SourceID != sourceID {
		t.Errorf("hasil = %+v", r)
	}

	f.provider.drivers[sourceID].schema, f.provider.drivers[targetID].schema = schema(customers()), schema(customers(), orders())
	r, err = f.svc.Preview(context.Background(), editorUser, targetID, sourceID)
	if err != nil || len(r.Changes) != 1 || r.Changes[0].Kind != KindDropTable || !r.Changes[0].Destructive {
		t.Errorf("tabel hanya di target harus menjadi drop_table destruktif: %v, %v", kinds(r.Changes), err)
	}
}

func TestService_Preview_RequiresEditOnTargetAndViewOnSource(t *testing.T) {
	tests := []struct {
		name  string
		actor domain.Principal
	}{
		{"view_only di target", readerUser},
		{"tanpa akses ke sumber", noSourceUser},
		{"Platform Admin tanpa group", rootUser},
		{"user tak dikenal", domain.Principal{UserID: 999}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newSvcFixture()
			if _, err := f.svc.Preview(context.Background(), tt.actor, targetID, sourceID); !errors.Is(err, domain.ErrForbidden) {
				t.Fatalf("err = %v, mau ErrForbidden", err)
			}
			if f.provider.calls != 0 {
				t.Error("driver tidak boleh disentuh saat akses ditolak")
			}
		})
	}
}

func TestService_Preview_SourceMustDifferFromTarget(t *testing.T) {
	f := newSvcFixture()
	_, err := f.svc.Preview(context.Background(), editorUser, targetID, targetID)
	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Fields["source_id"] == "" {
		t.Errorf("err = %v, mau error validasi pada source_id", err)
	}
	if f.provider.calls != 0 {
		t.Error("driver tidak boleh disentuh")
	}
}

func TestService_Preview_OnlyPostgresIsSupported(t *testing.T) {
	f := newSvcFixture()
	f.authz.levels[[2]int64{editorUser.UserID, mysqlID}] = domain.AccessViewEdit

	if _, err := f.svc.Preview(context.Background(), editorUser, targetID, mysqlID); !errors.Is(err, domain.ErrUnsupported) {
		t.Errorf("sumber MySQL: err = %v, mau ErrUnsupported", err)
	}
	if _, err := f.svc.Preview(context.Background(), editorUser, mysqlID, sourceID); !errors.Is(err, domain.ErrUnsupported) {
		t.Errorf("target MySQL: err = %v, mau ErrUnsupported", err)
	}
}

func TestService_Preview_DriverAndIntrospectionFailuresPropagate(t *testing.T) {
	f := newSvcFixture()
	f.provider.errs[sourceID] = fmt.Errorf("%w: host tidak terjangkau", domain.ErrConnectionFailed)
	if _, err := f.svc.Preview(context.Background(), editorUser, targetID, sourceID); !errors.Is(err, domain.ErrConnectionFailed) {
		t.Errorf("koneksi sumber gagal: err = %v", err)
	}

	f = newSvcFixture()
	f.provider.errs[targetID] = domain.ErrConnectionInactive
	if _, err := f.svc.Preview(context.Background(), editorUser, targetID, sourceID); !errors.Is(err, domain.ErrConnectionInactive) {
		t.Errorf("target nonaktif: err = %v", err)
	}

	f = newSvcFixture()
	boom := errors.New("katalog tidak terbaca")
	f.provider.drivers[sourceID].err = boom
	if _, err := f.svc.Preview(context.Background(), editorUser, targetID, sourceID); !errors.Is(err, boom) {
		t.Errorf("introspeksi gagal: err = %v", err)
	}
}

func TestService_AuthorizerFailureIsNotTurnedIntoForbidden(t *testing.T) {
	f := newSvcFixture()
	boom := errors.New("database platform tidak terjangkau")
	f.authz.err = boom

	if _, err := f.svc.Preview(context.Background(), editorUser, targetID, sourceID); !errors.Is(err, boom) || errors.Is(err, domain.ErrForbidden) {
		t.Errorf("Preview: err = %v", err)
	}
	if _, err := f.svc.Sources(context.Background(), editorUser, targetID); !errors.Is(err, boom) {
		t.Errorf("Sources: err = %v", err)
	}
}

func TestService_Sources(t *testing.T) {
	f := newSvcFixture()
	list, err := f.svc.Sources(context.Background(), editorUser, targetID)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, d := range list {
		ids = append(ids, d.ConnectionID)
	}
	if len(ids) != 2 || ids[0] != sourceID || ids[1] != otherID {
		t.Errorf("sumber = %v, mau [%d %d]: bukan target, bukan MySQL, bukan yang tanpa akses", ids, sourceID, otherID)
	}

	for name, actor := range map[string]domain.Principal{"view_only di target": readerUser, "Platform Admin": rootUser} {
		if _, err := f.svc.Sources(context.Background(), actor, targetID); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("%s: err = %v, mau ErrForbidden", name, err)
		}
	}
}

func TestService_CreateDraft_UsesChosenChangesInDiffOrder(t *testing.T) {
	f := newSvcFixture()
	f.provider.drivers[sourceID].schema = schema(customers(), orders(), table("public", "extra",
		[]domain.Column{col(1, "id", "integer", false, nil)}, []domain.Constraint{pk("extra_pkey", "id")}, nil))
	preview, err := f.svc.Preview(context.Background(), editorUser, targetID, sourceID)
	if err != nil || len(preview.Changes) < 4 {
		t.Fatalf("preview = %v, %v", kinds(preview.Changes), err)
	}

	picked := []string{preview.Changes[3].ID, preview.Changes[0].ID}
	m, err := f.svc.CreateDraft(context.Background(), editorUser, targetID, sourceID, picked)
	if err != nil {
		t.Fatal(err)
	}
	want := preview.Changes[0].SQL + "\n" + preview.Changes[3].SQL
	if len(f.migrations.calls) != 1 || f.migrations.calls[0].SQL != want {
		t.Errorf("SQL draft = %q, mau urutan diff: %q", f.migrations.calls[0].SQL, want)
	}
	if f.migrations.conns[0] != targetID || m.ConnectionID != targetID {
		t.Errorf("draft harus dibuat di database tujuan: %v", f.migrations.conns)
	}
	if f.migrations.calls[0].Name != "Schema diff dari sumber <asli>" {
		t.Errorf("nama = %q", f.migrations.calls[0].Name)
	}
}

func TestService_CreateDraft_LongSourceNameIsTruncated(t *testing.T) {
	f := newSvcFixture()
	f.authz.accessible[1].Name = strings.Repeat("é", 200)
	preview, _ := f.svc.Preview(context.Background(), editorUser, targetID, sourceID)

	if _, err := f.svc.CreateDraft(context.Background(), editorUser, targetID, sourceID, []string{preview.Changes[0].ID}); err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(f.migrations.calls[0].Name)); n != 120 {
		t.Errorf("panjang nama = %d, mau 120", n)
	}
}

func TestService_CreateDraft_RejectsEmptyAndStaleSelections(t *testing.T) {
	f := newSvcFixture()
	preview, _ := f.svc.Preview(context.Background(), editorUser, targetID, sourceID)

	for name, ids := range map[string][]string{"kosong": nil, "hanya spasi": {"  ", ""}} {
		_, err := f.svc.CreateDraft(context.Background(), editorUser, targetID, sourceID, ids)
		var verr *domain.ValidationError
		if !errors.As(err, &verr) || verr.Fields["changes"] != msgNothingChosen {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	_, err := f.svc.CreateDraft(context.Background(), editorUser, targetID, sourceID, []string{preview.Changes[0].ID, "palsu123456"})
	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Fields["changes"] != msgStaleChoice {
		t.Errorf("ID tak dikenal: err = %v", err)
	}
	if len(f.migrations.calls) != 0 {
		t.Error("tidak boleh ada draft untuk pilihan yang tidak valid")
	}
}

func TestService_CreateDraft_ChoiceFromOldPreviewIsRejectedAfterSchemaChanges(t *testing.T) {
	f := newSvcFixture()
	preview, _ := f.svc.Preview(context.Background(), editorUser, targetID, sourceID)
	oldID := preview.Changes[0].ID

	f.provider.drivers[sourceID].schema = schema(customers())
	_, err := f.svc.CreateDraft(context.Background(), editorUser, targetID, sourceID, []string{oldID})
	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Fields["changes"] != msgStaleChoice {
		t.Errorf("err = %v, mau pilihan kedaluwarsa", err)
	}
	if len(f.migrations.calls) != 0 {
		t.Error("tidak boleh ada draft")
	}
}

func TestService_CreateDraft_AccessAndUpstreamErrors(t *testing.T) {
	f := newSvcFixture()
	preview, _ := f.svc.Preview(context.Background(), editorUser, targetID, sourceID)
	ids := []string{preview.Changes[0].ID}

	for name, actor := range map[string]domain.Principal{"view_only di target": readerUser, "tanpa akses ke sumber": noSourceUser, "Platform Admin": rootUser} {
		if _, err := f.svc.CreateDraft(context.Background(), actor, targetID, sourceID, ids); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("%s: err = %v, mau ErrForbidden", name, err)
		}
	}
	if len(f.migrations.calls) != 0 {
		t.Error("tidak boleh ada draft untuk yang tidak berhak")
	}

	verr := &domain.ValidationError{Fields: map[string]string{"sql": "SQL maksimal 256 KB."}}
	f.migrations.err = verr
	if _, err := f.svc.CreateDraft(context.Background(), editorUser, targetID, sourceID, ids); !errors.Is(err, verr) {
		t.Errorf("error validasi dari migration harus diteruskan: %v", err)
	}
}

func TestService_CreateDraft_DestructiveChangesOnlyIfChosen(t *testing.T) {
	f := newSvcFixture()
	f.provider.drivers[targetID].schema = schema(customers(), orders())
	f.provider.drivers[sourceID].schema = schema(customers())
	preview, _ := f.svc.Preview(context.Background(), editorUser, targetID, sourceID)
	if len(preview.Changes) != 1 || !preview.Changes[0].Destructive {
		t.Fatalf("preview = %v", kinds(preview.Changes))
	}

	if _, err := f.svc.CreateDraft(context.Background(), editorUser, targetID, sourceID, []string{preview.Changes[0].ID}); err != nil {
		t.Fatalf("pengguna yang memilihnya secara sadar boleh: %v", err)
	}
	if !strings.Contains(f.migrations.calls[0].SQL, "DROP TABLE") {
		t.Errorf("sql = %s", f.migrations.calls[0].SQL)
	}
}
