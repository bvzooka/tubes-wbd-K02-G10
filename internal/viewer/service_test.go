package viewer

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
	"dbplatform/internal/testutil"
)

type fakeAuthz struct {
	list []domain.AccessibleDatabase
	err  error
}

func (f fakeAuthz) EffectiveLevel(context.Context, int64, int64) (domain.AccessLevel, error) {
	return domain.AccessNone, nil
}
func (f fakeAuthz) Can(context.Context, int64, int64, domain.AccessLevel) (bool, error) {
	return false, nil
}
func (f fakeAuthz) ListAccessible(context.Context, int64) ([]domain.AccessibleDatabase, error) {
	return f.list, f.err
}

type fakeDriver struct {
	dbdriver.Driver
	schema *domain.Schema
	data *domain.TableData
	gotRef domain.TableRef
	gotPage domain.PageRequest
	introCall int
	err error
}

func (d *fakeDriver) Introspect(context.Context) (*domain.Schema, error) {
	d.introCall++
	return d.schema, d.err
}
func (d *fakeDriver) TableData(_ context.Context, ref domain.TableRef, p domain.PageRequest) (*domain.TableData, error) {
	d.gotRef, d.gotPage = ref, p
	return d.data, d.err
}

func def(s string) *string { return &s }

func sampleSchema() *domain.Schema {
	return &domain.Schema{Tables: []domain.Table{
		{Schema: "hr", Name: "employees", Kind: domain.TableKindTable, EstimatedRows: 3,
			Columns: []domain.Column{{Name: "id", Position: 1, DataType: "integer"}}},
		{Schema: "public", Name: "Mixed Case", Kind: domain.TableKindTable, EstimatedRows: -1},
		{Schema: "public", Name: "orders", Kind: domain.TableKindTable, EstimatedRows: 10,
			Columns: []domain.Column{
				{Name: "id", Position: 1, DataType: "bigint"},
				{Name: "status", Position: 2, DataType: "text", Nullable: true, Default: def("'new'::text")},
			},
			Constraints: []domain.Constraint{
				{Name: "orders_pkey", Type: domain.ConstraintPrimaryKey, Columns: []string{"id"}},
				{Name: "orders_fk", Type: domain.ConstraintForeignKey, Columns: []string{"cid"},
					RefSchema: "public", RefTable: "customers", RefColumns: []string{"id"}, OnUpdate: "CASCADE", OnDelete: "RESTRICT"},
				{Name: "orders_chk", Type: domain.ConstraintCheck, CheckExpr: "CHECK ((id > 0))"},
			},
			Indexes: []domain.Index{{Name: "orders_pkey", Method: "btree", Columns: []string{"id"}, Unique: true, Primary: true, Definition: "CREATE UNIQUE INDEX ..."}}},
		{Schema: "public", Name: "v", Kind: domain.TableKindView, EstimatedRows: -1},
	}}
}

func newSvc(level domain.AccessLevel, drv *fakeDriver) *Service {
	authz := fakeAuthz{list: []domain.AccessibleDatabase{{ConnectionID: 7, Name: "Sales", DBMS: domain.DBMSPostgres, Level: level}}}
	return NewService(authz, testutil.NewFakeDriverProvider().Set(7, drv))
}

var user = domain.Principal{UserID: 1, Username: "u"}

func TestService_Databases(t *testing.T) {
	svc := newSvc(domain.AccessViewEdit, &fakeDriver{})
	got, err := svc.Databases(context.Background(), user)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v err %v", got, err)
	}
	if got[0].LevelLabel != "View + Edit" || !got[0].CanEdit || got[0].Name != "Sales" || got[0].DBMS != "postgres" {
		t.Errorf("item = %+v", got[0])
	}
	svc = newSvc(domain.AccessViewOnly, &fakeDriver{})
	got, _ = svc.Databases(context.Background(), user)
	if got[0].LevelLabel != "View Only" || got[0].CanEdit {
		t.Errorf("view only = %+v", got[0])
	}
}

func TestService_DatabasesPropagatesError(t *testing.T) {
	boom := errors.New("boom")
	svc := NewService(fakeAuthz{err: boom}, testutil.NewFakeDriverProvider())
	if _, err := svc.Databases(context.Background(), user); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}

func TestService_AccessDeniedForEveryMethod(t *testing.T) {
	drv := &fakeDriver{schema: sampleSchema(), data: &domain.TableData{}}
	cases := map[string]*Service{
		"tanpa akses":  NewService(fakeAuthz{}, testutil.NewFakeDriverProvider().Set(7, drv)),
		"koneksi lain": NewService(fakeAuthz{list: []domain.AccessibleDatabase{{ConnectionID: 8, Level: domain.AccessViewEdit}}}, testutil.NewFakeDriverProvider().Set(7, drv)),
		"level none":   newSvc(domain.AccessNone, drv),
	}
	ref := domain.TableRef{Schema: "public", Table: "orders"}
	for name, svc := range cases {
		ctx := context.Background()
		_, e1 := svc.Overview(ctx, user, 7)
		_, e2 := svc.Table(ctx, user, 7, ref)
		_, e3 := svc.Data(ctx, user, 7, ref, domain.PageRequest{})
		for i, err := range []error{e1, e2, e3} {
			if !errors.Is(err, domain.ErrForbidden) {
				t.Errorf("%s #%d: mau ErrForbidden, dapat %v", name, i, err)
			}
		}
	}
	if drv.introCall != 0 || drv.gotRef != (domain.TableRef{}) {
		t.Error("driver tidak boleh disentuh saat akses ditolak")
	}
}

func TestService_Overview(t *testing.T) {
	svc := newSvc(domain.AccessViewOnly, &fakeDriver{schema: sampleSchema()})
	ov, err := svc.Overview(context.Background(), user, 7)
	if err != nil {
		t.Fatal(err)
	}
	if ov.Database.Name != "Sales" || len(ov.Schemas) != 2 || ov.Schemas[0].Name != "hr" || ov.Schemas[1].Name != "public" {
		t.Fatalf("schemas = %+v", ov.Schemas)
	}
	pub := ov.Schemas[1].Tables
	if len(pub) != 3 {
		t.Fatalf("public punya %d tabel", len(pub))
	}
	mixed := pub[0]
	if mixed.Name != "Mixed Case" || mixed.RowsText != "tidak diketahui" ||
		mixed.Href != "/databases/7/tables/public/Mixed%20Case" || mixed.DataHref != mixed.Href+"/data" {
		t.Errorf("Mixed Case = %+v", mixed)
	}
	if pub[2].KindLabel != "View" || pub[1].KindLabel != "Tabel" || pub[1].RowsText != "10" {
		t.Errorf("label = %+v %+v", pub[1], pub[2])
	}
}

func TestService_TableDetail(t *testing.T) {
	svc := newSvc(domain.AccessViewOnly, &fakeDriver{schema: sampleSchema()})
	d, err := svc.Table(context.Background(), user, 7, domain.TableRef{Schema: "public", Table: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Schema != "public" || d.Name != "orders" || d.DataHref != "/databases/7/tables/public/orders/data" || d.BackHref != "/databases/7" {
		t.Errorf("detail = %+v", d)
	}
	if len(d.Columns) != 2 || d.Columns[0].HasDefault || !d.Columns[1].HasDefault || d.Columns[1].Default != "'new'::text" || !d.Columns[1].Nullable {
		t.Errorf("kolom = %+v", d.Columns)
	}
	want := []ConstraintRow{
		{Name: "orders_pkey", TypeLabel: "Primary key", Columns: "id"},
		{Name: "orders_fk", TypeLabel: "Foreign key", Columns: "cid",
			Detail: "→ public.customers (id) · ON UPDATE CASCADE · ON DELETE RESTRICT"},
		{Name: "orders_chk", TypeLabel: "Check", Detail: "CHECK ((id > 0))"},
	}
	if !reflect.DeepEqual(d.Constraints, want) {
		t.Errorf("constraint = %+v", d.Constraints)
	}
	if len(d.Indexes) != 1 || !d.Indexes[0].Primary || d.Indexes[0].Columns != "id" {
		t.Errorf("index = %+v", d.Indexes)
	}
}

func TestService_TableNotFound(t *testing.T) {
	svc := newSvc(domain.AccessViewOnly, &fakeDriver{schema: sampleSchema()})
	for _, ref := range []domain.TableRef{{Schema: "public", Table: "nope"}, {Schema: "x", Table: "orders"}, {}} {
		if _, err := svc.Table(context.Background(), user, 7, ref); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%+v: mau ErrNotFound, dapat %v", ref, err)
		}
	}
}

func TestService_DataPassesThrough(t *testing.T) {
	td := &domain.TableData{Page: 2, PageSize: 10, HasNext: true}
	drv := &fakeDriver{data: td}
	svc := newSvc(domain.AccessViewOnly, drv)
	ref := domain.TableRef{Schema: "public", Table: "Mixed Case"}
	v, err := svc.Data(context.Background(), user, 7, ref, domain.PageRequest{Page: 2, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if v.Result != td || drv.gotRef != ref || drv.gotPage != (domain.PageRequest{Page: 2, PageSize: 10}) {
		t.Errorf("view = %+v ref=%+v page=%+v", v, drv.gotRef, drv.gotPage)
	}
	if v.TableHref != "/databases/7/tables/public/Mixed%20Case" {
		t.Errorf("TableHref = %q", v.TableHref)
	}
}

func TestService_DriverErrorsPropagate(t *testing.T) {
	ctx := context.Background()
	// Provider gagal (mis. koneksi nonaktif / tidak terjangkau).
	for _, perr := range []error{domain.ErrConnectionInactive, domain.ErrConnectionFailed, domain.ErrNotFound} {
		authz := fakeAuthz{list: []domain.AccessibleDatabase{{ConnectionID: 7, Level: domain.AccessViewOnly}}}
		svc := NewService(authz, errProvider{perr})
		if _, err := svc.Overview(ctx, user, 7); !errors.Is(err, perr) {
			t.Errorf("Overview: mau %v, dapat %v", perr, err)
		}
		if _, err := svc.Data(ctx, user, 7, domain.TableRef{}, domain.PageRequest{}); !errors.Is(err, perr) {
			t.Errorf("Data: mau %v, dapat %v", perr, err)
		}
	}
	// Driver gagal.
	drv := &fakeDriver{err: domain.ErrConnectionFailed}
	svc := newSvc(domain.AccessViewOnly, drv)
	if _, err := svc.Overview(ctx, user, 7); !errors.Is(err, domain.ErrConnectionFailed) {
		t.Errorf("Introspect error tidak diteruskan: %v", err)
	}
	drv.data = nil
	if _, err := svc.Data(ctx, user, 7, domain.TableRef{}, domain.PageRequest{}); !errors.Is(err, domain.ErrConnectionFailed) {
		t.Errorf("TableData error tidak diteruskan: %v", err)
	}
}

type errProvider struct{ err error }

func (p errProvider) Get(context.Context, int64) (dbdriver.Driver, error) { return nil, p.err }

func TestHelpers(t *testing.T) {
	if rowsText(-1) != "tidak diketahui" || rowsText(0) != "0" || rowsText(42) != "42" {
		t.Error("rowsText")
	}
	if got := tableHref(1, "a b", "c/d?e#f"); got != "/databases/1/tables/a%20b/c%2Fd%3Fe%23f" {
		t.Errorf("tableHref = %q", got)
	}
	if LevelLabel(domain.AccessNone) != "Tidak ada" {
		t.Error("LevelLabel none")
	}
}
