package viewer

import (
	"net/url"
	"strconv"
	"strings"

	"dbplatform/internal/domain"
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

// TableItem adalah satu tabel atau view di halaman ringkasan
type TableItem struct {
	Schema    string
	Name      string
	KindLabel string
	RowsText  string
	Href      string
	DataHref  string
}

// SchemaGroup mengelompokkan tabel milik satu schema
type SchemaGroup struct {
	Name   string
	Tables []TableItem
}

// Overview adalah halaman ringkasan satu database: tabel dikelompokkan per schema
type Overview struct {
	Database DatabaseItem
	Schemas  []SchemaGroup
}

// ColumnRow adalah satu kolom di halaman detail tabel. HasDefault membedakan "tanpa default" dari default kosong
type ColumnRow struct {
	Position   int
	Name       string
	DataType   string
	Nullable   bool
	Default    string
	HasDefault bool
}

// ConstraintRow adalah satu constraint di halaman detail tabel, Detail berisi tujuan foreign key atau ekspresi check
type ConstraintRow struct {
	Name      string
	TypeLabel string
	Columns   string
	Detail    string
}

// IndexRow adalah satu index di halaman detail tabel
type IndexRow struct {
	Name       string
	Method     string
	Columns    string
	Unique     bool
	Primary    bool
	Definition string
}

// TableDetail adalah halaman detail satu tabel: kolom, constraint, dan index
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

// DataView adalah halaman isi tabel, satu halaman data
type DataView struct {
	Database  DatabaseItem
	Schema    string
	Name      string
	Result    *domain.TableData
	TableHref string
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

// newDatabaseItem menyusun baris daftar database dari hasil Authorizer
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

// dbHref membuat alamat halaman ringkasan satu database
func dbHref(id int64) string { return "/databases/" + strconv.FormatInt(id, 10) }

// tableHref membuat alamat halaman detail tabel, nama schema dan tabel di-escape karena bisa berisi karakter khusus
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

// kindLabel mengubah jenis relasi jadi teks "Tabel" atau "View"
func kindLabel(k domain.TableKind) string {
	if k == domain.TableKindView {
		return "View"
	}
	return "Tabel"
}

// columnRow menyusun satu baris kolom, default nil berarti kolom itu tidak punya default
func columnRow(c domain.Column) ColumnRow {
	row := ColumnRow{Position: c.Position, Name: c.Name, DataType: c.DataType, Nullable: c.Nullable}
	if c.Default != nil {
		row.Default, row.HasDefault = *c.Default, true
	}
	return row
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
