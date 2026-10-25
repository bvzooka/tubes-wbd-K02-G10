package viewer

import (
	"context"
	"strings"
	"dbplatform/internal/contract"
	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
)

type Service struct {
	authz contract.Authorizer
	drivers contract.DriverProvider
}

func NewService(authz contract.Authorizer, drivers contract.DriverProvider) *Service {
	return &Service{authz: authz, drivers: drivers}
}

// mengembalikan database yg boleh diakses user 
func (s *Service) Databases(ctx context.Context, p domain.Principal) ([]DatabaseItem, error) {
	list, err := s.authz.ListAccessible(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	out := make([]DatabaseItem, 0, len(list))
	for _, a := range list {
		out = append(out, newDatabaseItem(a))
	}
	return out, nil
}

// memastikan user minimal View Only pada koneksi, selain itu ErrForbidden
func (s *Service) access(ctx context.Context, p domain.Principal, dbID int64) (DatabaseItem, error) {
	list, err := s.authz.ListAccessible(ctx, p.UserID)
	if err != nil {
		return DatabaseItem{}, err
	}
	for _, a := range list {
		if a.ConnectionID == dbID && a.Level.AtLeast(domain.AccessViewOnly) {
			return newDatabaseItem(a), nil
		}
	}
	return DatabaseItem{}, domain.ErrForbidden
}

func (s *Service) open(ctx context.Context, p domain.Principal, dbID int64) (DatabaseItem, dbdriver.Driver, error) {
	item, err := s.access(ctx, p, dbID)
	if err != nil {
		return DatabaseItem{}, nil, err
	}
	drv, err := s.drivers.Get(ctx, dbID)
	if err != nil {
		return DatabaseItem{}, nil, err
	}
	return item, drv, nil
}

// menampilkan tabel dan view per schema
func (s *Service) Overview(ctx context.Context, p domain.Principal, dbID int64) (*Overview, error) {
	item, drv, err := s.open(ctx, p, dbID)
	if err != nil {
		return nil, err
	}
	schema, err := drv.Introspect(ctx)
	if err != nil {
		return nil, err
	}
	ov := &Overview{Database: item}
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

// menampilkan kolom, constraint, dan index satu tabel
func (s *Service) Table(ctx context.Context, p domain.Principal, dbID int64, ref domain.TableRef) (*TableDetail, error) {
	item, drv, err := s.open(ctx, p, dbID)
	if err != nil {
		return nil, err
	}
	schema, err := drv.Introspect(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range schema.Tables {
		if t.Schema != ref.Schema || t.Name != ref.Table {
			continue
		}
		d := &TableDetail{
			Database: item, Schema: t.Schema, Name: t.Name, KindLabel: kindLabel(t.Kind),
			RowsText: rowsText(t.EstimatedRows),
			DataHref: tableHref(dbID, t.Schema, t.Name) + "/data", BackHref: dbHref(dbID),
		}
		for _, c := range t.Columns {
			row := ColumnRow{Position: c.Position, Name: c.Name, DataType: c.DataType, Nullable: c.Nullable}
			if c.Default != nil {
				row.Default, row.HasDefault = *c.Default, true
			}
			d.Columns = append(d.Columns, row)
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
		return d, nil
	}
	return nil, domain.ErrNotFound
}

// menampilkan satu halaman isi tabel 
func (s *Service) Data(ctx context.Context, p domain.Principal, dbID int64, ref domain.TableRef, page domain.PageRequest) (*DataView, error) {
	item, drv, err := s.open(ctx, p, dbID)
	if err != nil {
		return nil, err
	}
	res, err := drv.TableData(ctx, ref, page)
	if err != nil {
		return nil, err
	}
	return &DataView{
		Database: item, Schema: ref.Schema, Name: ref.Table, Result: res,
		TableHref: tableHref(dbID, ref.Schema, ref.Table),
	}, nil
}
