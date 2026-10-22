package domain

type TableKind string

const (
	TableKindTable TableKind = "table"
	TableKindView  TableKind = "view"
)

type ConstraintType string

const (
	ConstraintPrimaryKey ConstraintType = "primary_key"
	ConstraintForeignKey ConstraintType = "foreign_key"
	ConstraintUnique     ConstraintType = "unique"
	ConstraintCheck      ConstraintType = "check"
)

// Schema adalah hasil introspeksi satu database. Urutan harus deterministik:
// tabel menurut (Schema, Name), kolom menurut Position, constraint dan index menurut Name.
type Schema struct {
	Tables []Table
}

type Table struct {
	Schema        string
	Name          string
	Kind          TableKind
	EstimatedRows int64 // -1 jika tidak diketahui
	Columns       []Column
	Constraints   []Constraint
	Indexes       []Index
}

type Column struct {
	Name     string
	Position int
	DataType string
	Nullable bool
	Default  *string // nil jika tidak ada default
}

type Constraint struct {
	Name       string
	Type       ConstraintType
	Columns    []string
	RefSchema  string   // hanya foreign key
	RefTable   string   // hanya foreign key
	RefColumns []string // hanya foreign key
	OnUpdate   string   // hanya foreign key, contoh "NO ACTION", "CASCADE"
	OnDelete   string   // hanya foreign key
	CheckExpr  string   // hanya check
}

type Index struct {
	Name       string
	Columns    []string // best effort; ekspresi boleh berupa teks ekspresi
	Unique     bool
	Primary    bool
	Method     string // btree, hash, gin, ...
	Definition string // pernyataan CREATE INDEX lengkap
}

// TableRef menunjuk satu tabel (schema dan nama).
type TableRef struct {
	Schema string
	Table  string
}

const (
	DefaultPageSize = 50
	MaxPageSize     = 200
)

type PageRequest struct {
	Page     int // mulai dari 1
	PageSize int
}

func (p PageRequest) Normalize() PageRequest {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PageSize < 1 {
		p.PageSize = DefaultPageSize
	}
	if p.PageSize > MaxPageSize {
		p.PageSize = MaxPageSize
	}
	return p
}

func (p PageRequest) Offset() int {
	n := p.Normalize()
	return (n.Page - 1) * n.PageSize
}

// Cell adalah satu nilai sel hasil query dalam bentuk teks.
type Cell struct {
	Value string
	Null  bool
}

type ResultSet struct {
	Columns []string
	Rows    [][]Cell
}

type TableData struct {
	ResultSet
	Page     int
	PageSize int
	HasNext  bool
}
