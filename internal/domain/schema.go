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

// Schema harus berurutan deterministik: tabel menurut (Schema, Name), kolom menurut Position, constraint dan index menurut Name.
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

// RefSchema sampai OnDelete hanya terisi untuk foreign key, CheckExpr hanya untuk check.
type Constraint struct {
	Name       string
	Type       ConstraintType
	Columns    []string
	RefSchema  string
	RefTable   string
	RefColumns []string
	OnUpdate   string
	OnDelete   string
	CheckExpr  string
}

// Definition adalah pernyataan CREATE INDEX lengkap.
type Index struct {
	Name       string
	Columns    []string
	Unique     bool
	Primary    bool
	Method     string
	Definition string
}

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
