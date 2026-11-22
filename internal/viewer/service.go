package viewer

import (
	"context"
	"fmt"
	"strings"

	"dbplatform/internal/contract"
	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
)

// menyiapkan data tampilan setelah memeriksa akses user ke database
type Service struct {
	authz   contract.Authorizer
	drivers contract.DriverProvider
}

// dibuat dengan authorizer dan penyedia driver database target
func NewService(authz contract.Authorizer, drivers contract.DriverProvider) *Service {
	return &Service{authz: authz, drivers: drivers}
}

// mengembalikan database yang boleh diakses user beserta level aksesnya
func (s *Service) Databases(ctx context.Context, p domain.Principal) ([]DatabaseItem, error) {
	list, err := s.authz.ListAccessible(ctx, p.UserID)
	if err != nil {
		return nil, fmt.Errorf("list accessible databases: %w", err)
	}
	out := make([]DatabaseItem, 0, len(list))
	for _, a := range list {
		out = append(out, newDatabaseItem(a))
	}
	return out, nil
}

// Schema membaca semua tabel dan view database setelah memeriksa akses user.
// Mengembalikan data mentah dari driver, Overview dan API JSON menyusunnya sendiri sesuai kebutuhan.
func (s *Service) Schema(ctx context.Context, p domain.Principal, dbID int64) (DatabaseItem, *domain.Schema, error) {
	item, drv, err := s.open(ctx, p, dbID)
	if err != nil {
		return DatabaseItem{}, nil, err
	}
	schema, err := drv.Introspect(ctx)
	if err != nil {
		return DatabaseItem{}, nil, fmt.Errorf("introspect database %d: %w", dbID, err)
	}
	return item, schema, nil
}

// Describe mengembalikan satu tabel lengkap dengan kolom, constraint, dan index, ErrNotFound bila tidak ada
func (s *Service) Describe(ctx context.Context, p domain.Principal, dbID int64, ref domain.TableRef) (DatabaseItem, domain.Table, error) {
	item, schema, err := s.Schema(ctx, p, dbID)
	if err != nil {
		return DatabaseItem{}, domain.Table{}, err
	}
	for _, t := range schema.Tables {
		if t.Schema == ref.Schema && t.Name == ref.Table {
			return item, t, nil
		}
	}
	return DatabaseItem{}, domain.Table{}, domain.ErrNotFound
}

// Rows membaca satu halaman isi tabel setelah memeriksa akses user
func (s *Service) Rows(ctx context.Context, p domain.Principal, dbID int64, ref domain.TableRef, page domain.PageRequest) (DatabaseItem, *domain.TableData, error) {
	item, drv, err := s.open(ctx, p, dbID)
	if err != nil {
		return DatabaseItem{}, nil, err
	}
	res, err := drv.TableData(ctx, ref, page)
	if err != nil {
		return DatabaseItem{}, nil, fmt.Errorf("read data of %s.%s: %w", ref.Schema, ref.Table, err)
	}
	return item, res, nil
}

// Overview menampilkan tabel dan view per schema
func (s *Service) Overview(ctx context.Context, p domain.Principal, dbID int64) (*Overview, error) {
	item, schema, err := s.Schema(ctx, p, dbID)
	if err != nil {
		return nil, err
	}
	ov := &Overview{Database: item}
	// tabel dari Introspect sudah berurutan per schema, jadi grup baru dibuat tiap schema berganti
	for _, t := range schema.Tables {
		if n := len(ov.Schemas); n == 0 || ov.Schemas[n-1].Name != t.Schema {
			ov.Schemas = append(ov.Schemas, SchemaGroup{Name: t.Schema})
		}
		g := &ov.Schemas[len(ov.Schemas)-1]
		href := tableHref(dbID, t.Schema, t.Name)
		g.Tables = append(g.Tables, TableItem{
			Schema: t.Schema, Name: t.Name, KindLabel: kindLabel(t.Kind),
			RowsText: rowsText(t.EstimatedRows), Href: href, DataHref: href + "/data",
		})
	}
	return ov, nil
}

// menampilkan kolom, constraint, dan index satu tabel, ErrNotFound bila tabelnya tidak ada
func (s *Service) Table(ctx context.Context, p domain.Principal, dbID int64, ref domain.TableRef) (*TableDetail, error) {
	item, t, err := s.Describe(ctx, p, dbID, ref)
	if err != nil {
		return nil, err
	}
	return newTableDetail(dbID, item, t), nil
}

// menampilkan satu halaman isi tabel
func (s *Service) Data(ctx context.Context, p domain.Principal, dbID int64, ref domain.TableRef, page domain.PageRequest) (*DataView, error) {
	item, res, err := s.Rows(ctx, p, dbID, ref, page)
	if err != nil {
		return nil, err
	}
	return &DataView{
		Database: item, Schema: ref.Schema, Name: ref.Table, Result: res,
		TableHref: tableHref(dbID, ref.Schema, ref.Table),
	}, nil
}

// memeriksa akses lalu mengambil driver database, semua halaman memakainya sebagai pintu masuk
func (s *Service) open(ctx context.Context, p domain.Principal, dbID int64) (DatabaseItem, dbdriver.Driver, error) {
	item, err := s.access(ctx, p, dbID)
	if err != nil {
		return DatabaseItem{}, nil, err
	}
	drv, err := s.drivers.Get(ctx, dbID)
	if err != nil {
		return DatabaseItem{}, nil, fmt.Errorf("get driver for database %d: %w", dbID, err)
	}
	return item, drv, nil
}

// memastikan user minimal View Only pada koneksi, selain itu ErrForbidden
func (s *Service) access(ctx context.Context, p domain.Principal, dbID int64) (DatabaseItem, error) {
	list, err := s.authz.ListAccessible(ctx, p.UserID)
	if err != nil {
		return DatabaseItem{}, fmt.Errorf("list accessible databases: %w", err)
	}
	for _, a := range list {
		if a.ConnectionID == dbID && a.Level.AtLeast(domain.AccessViewOnly) {
			return newDatabaseItem(a), nil
		}
	}
	return DatabaseItem{}, domain.ErrForbidden
}

// menyusun halaman detail dari satu tabel hasil Introspect
func newTableDetail(dbID int64, item DatabaseItem, t domain.Table) *TableDetail {
	d := &TableDetail{
		Database: item, Schema: t.Schema, Name: t.Name, KindLabel: kindLabel(t.Kind),
		RowsText: rowsText(t.EstimatedRows),
		DataHref: tableHref(dbID, t.Schema, t.Name) + "/data", BackHref: dbHref(dbID),
	}
	for _, c := range t.Columns {
		d.Columns = append(d.Columns, columnRow(c))
	}
	for _, c := range t.Constraints {
		d.Constraints = append(d.Constraints, constraintRow(c))
	}
	for _, ix := range t.Indexes {
		d.Indexes = append(d.Indexes, IndexRow{
			Name: ix.Name, Method: ix.Method, Columns: strings.Join(ix.Columns, ", "),
			Unique: ix.Unique, Primary: ix.Primary, Definition: ix.Definition,
		})
	}
	return d
}
