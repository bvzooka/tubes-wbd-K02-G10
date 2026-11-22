package viewer

import (
	"dbplatform/internal/domain"
	"net/url"
	"strconv"
	"strings"
)

// DatabaseItem adalah satu baris di daftar database, label sudah berupa teks siap tampil
type DatabaseItem struct {
	ID         int64
	Name       string
	DBMS       string
	DBMSLabel  string
	Level      domain.AccessLevel
	LevelLabel string
	CanEdit    bool
}

func newDatabaseItem(a domain.AccessibleDatabase) DatabaseItem {
	return DatabaseItem{
		ID: a.ConnectionID, Name: a.Name, DBMS: string(a.DBMS), DBMSLabel: dbmsLabel(a.DBMS),
		Level: a.Level, LevelLabel: LevelLabel(a.Level), CanEdit: a.Level.AtLeast(domain.AccessViewEdit),
	}
}

// dbmsLabel mengubah kode DBMS jadi nama yang enak dibaca
func dbmsLabel(d domain.DBMS) string {
	switch d {
	case domain.DBMSPostgres:
		return "PostgreSQL"
	case domain.DBMSMySQL:
		return "MySQL"
	case domain.DBMSMariaDB:
		return "MariaDB"
	}
	return string(d)
}

// LevelLabel mengubah level akses jadi teks untuk ditampilkan
func LevelLabel(l domain.AccessLevel) string {
	switch l {
	case domain.AccessViewEdit:
		return "View + Edit"
	case domain.AccessViewOnly:
		return "View Only"
	}
	return "Tidak ada"
}

// TableItem, SchemaGroup, dan Overview membentuk halaman ringkasan: tabel dikelompokkan per schema
type TableItem struct {
	Schema    string
	Name      string
	KindLabel string
	RowsText  string
	Href      string
	DataHref  string
}

type SchemaGroup struct {
	Name   string
	Tables []TableItem
}

type Overview struct {
	Database DatabaseItem
	Schemas  []SchemaGroup
}

// ColumnRow, ConstraintRow, IndexRow, dan TableDetail membentuk halaman detail satu tabel
type ColumnRow struct {
	Position   int
	Name       string
	DataType   string
	Nullable   bool
	Default    string
	HasDefault bool
}

type ConstraintRow struct {
	Name      string
	TypeLabel string
	Columns   string
	Detail    string
}

type IndexRow struct {
	Name       string
	Method     string
	Columns    string
	Unique     bool
	Primary    bool
	Definition string
}

type TableDetail struct {
	Database    DatabaseItem
	Schema      string
	Name        string
	KindLabel   string
	RowsText    string
	Columns     []ColumnRow
	Constraints []ConstraintRow
	Indexes     []IndexRow
	DataHref    string
	BackHref    string
}

// data halaman isi tabel
type DataView struct {
	Database  DatabaseItem
	Schema    string
	Name      string
	Result    *domain.TableData
	TableHref string
}

// dbHref dan tableHref membuat alamat halaman, nama schema dan tabel di-escape karena bisa berisi karakter khusus
func dbHref(id int64) string { return "/databases/" + strconv.FormatInt(id, 10) }

func tableHref(id int64, schema, table string) string {
	return dbHref(id) + "/tables/" + url.PathEscape(schema) + "/" + url.PathEscape(table)
}

// rowsText menampilkan perkiraan jumlah baris, negatif berarti database tidak tahu
func rowsText(n int64) string {
	if n < 0 {
		return "tidak diketahui"
	}
	return strconv.FormatInt(n, 10)
}

func kindLabel(k domain.TableKind) string {
	if k == domain.TableKindView {
		return "View"
	}
	return "Tabel"
}

// constraintRow menyusun label dan detail constraint, foreign key menampilkan tabel tujuan dan aturan ON UPDATE/DELETE
func constraintRow(c domain.Constraint) ConstraintRow {
	row := ConstraintRow{Name: c.Name, Columns: strings.Join(c.Columns, ", ")}
	switch c.Type {
	case domain.ConstraintPrimaryKey:
		row.TypeLabel = "Primary key"
	case domain.ConstraintUnique:
		row.TypeLabel = "Unique"
	case domain.ConstraintForeignKey:
		row.TypeLabel = "Foreign key"
		row.Detail = "→ " + c.RefSchema + "." + c.RefTable + " (" + strings.Join(c.RefColumns, ", ") + ")" +
			" · ON UPDATE " + c.OnUpdate + " · ON DELETE " + c.OnDelete
	case domain.ConstraintCheck:
		row.TypeLabel = "Check"
		row.Detail = c.CheckExpr
	default:
		row.TypeLabel = string(c.Type)
	}
	return row
}
