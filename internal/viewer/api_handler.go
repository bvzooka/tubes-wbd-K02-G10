package viewer

import (
	"net/http"
	"strconv"

	"dbplatform/internal/apiv1"
	"dbplatform/internal/domain"
)

// melayani /api/v1/databases: daftar database, schema, detail tabel, dan isi tabel
// route schema, tabel, dan data dijaga DBView di router, service mengecek aksesnya sekali lagi.
type APIHandler struct {
	svc *Service
}

// dibuat dengan service viewer
func NewAPIHandler(svc *Service) *APIHandler { return &APIHandler{svc: svc} }

// databaseJSON adalah satu database yang boleh diakses user
// CanEdit hanya untuk menggerakkan UI, pengamanan sebenarnya tetap di backend.
type databaseJSON struct {
	ID          int64              `json:"id"`
	Name        string             `json:"name"`
	DBMS        string             `json:"dbms"`
	AccessLevel domain.AccessLevel `json:"access_level"`
	CanEdit     bool               `json:"can_edit"`
}

//  halaman ringkasan: tabel dan view dikelompokkan per schema
type schemaJSON struct {
	Database databaseJSON      `json:"database"`
	Schemas  []schemaGroupJSON `json:"schemas"`
}

type schemaGroupJSON struct {
	Name   string          `json:"name"`
	Tables []tableItemJSON `json:"tables"`
}

// satu tabel atau view di ringkasan. EstimatedRows null bila database tidak tahu.
type tableItemJSON struct {
	Schema        string           `json:"schema"`
	Name          string           `json:"name"`
	Kind          domain.TableKind `json:"kind"`
	EstimatedRows *int64           `json:"estimated_rows"`
}

// detail satu tabel
type tableJSON struct {
	Database      databaseJSON     `json:"database"`
	Schema        string           `json:"schema"`
	Name          string           `json:"name"`
	Kind          domain.TableKind `json:"kind"`
	EstimatedRows *int64           `json:"estimated_rows"`
	Columns       []columnJSON     `json:"columns"`
	Constraints   []constraintJSON `json:"constraints"`
	Indexes       []indexJSON      `json:"indexes"`
}

// default null berarti kolom tanpa default, berbeda dengan default string kosong
type columnJSON struct {
	Name     string  `json:"name"`
	Position int     `json:"position"`
	DataType string  `json:"data_type"`
	Nullable bool    `json:"nullable"`
	Default  *string `json:"default"`
}

// kolom rujukan dan aturan hanya terisi untuk foreign key, check_expr hanya untuk check
type constraintJSON struct {
	Name       string                `json:"name"`
	Type       domain.ConstraintType `json:"type"`
	Columns    []string              `json:"columns"`
	RefSchema  string                `json:"ref_schema,omitempty"`
	RefTable   string                `json:"ref_table,omitempty"`
	RefColumns []string              `json:"ref_columns,omitempty"`
	OnUpdate   string                `json:"on_update,omitempty"`
	OnDelete   string                `json:"on_delete,omitempty"`
	CheckExpr  string                `json:"check_expr,omitempty"`
}

type indexJSON struct {
	Name       string   `json:"name"`
	Method     string   `json:"method"`
	Columns    []string `json:"columns"`
	Unique     bool     `json:"unique"`
	Primary    bool     `json:"primary"`
	Definition string   `json:"definition"`
}

// satu halaman isi tabel. Sel NULL dikirim sebagai null, bukan string kosong, supaya UI bisa membedakannya.
type dataJSON struct {
	Database databaseJSON `json:"database"`
	Schema   string       `json:"schema"`
	Name     string       `json:"name"`
	Columns  []string     `json:"columns"`
	Rows     [][]*string  `json:"rows"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
	HasNext  bool         `json:"has_next"`
}

// menangani GET /databases
func (h *APIHandler) ListDatabases(w http.ResponseWriter, r *http.Request) error {
	items, err := h.svc.Databases(r.Context(), apiv1.Actor(r))
	if err != nil {
		return err
	}
	out := make([]databaseJSON, 0, len(items))
	for _, it := range items {
		out = append(out, toDatabaseJSON(it))
	}
	return apiv1.OK(w, out)
}

// menangani GET /databases/{dbID}/schema
func (h *APIHandler) Schema(w http.ResponseWriter, r *http.Request) error {
	dbID, err := apiv1.PathID(r, "dbID")
	if err != nil {
		return err
	}
	item, schema, err := h.svc.Schema(r.Context(), apiv1.Actor(r), dbID)
	if err != nil {
		return err
	}
	return apiv1.OK(w, schemaJSON{Database: toDatabaseJSON(item), Schemas: groupBySchema(schema.Tables)})
}

// menangani GET /databases/{dbID}/tables/{schema}/{table}
func (h *APIHandler) Table(w http.ResponseWriter, r *http.Request) error {
	dbID, err := apiv1.PathID(r, "dbID")
	if err != nil {
		return err
	}
	item, t, err := h.svc.Describe(r.Context(), apiv1.Actor(r), dbID, pathTableRef(r))
	if err != nil {
		return err
	}
	return apiv1.OK(w, toTableJSON(item, t))
}

// menangani GET /databases/{dbID}/tables/{schema}/{table}/data?page=&size=
func (h *APIHandler) Data(w http.ResponseWriter, r *http.Request) error {
	dbID, err := apiv1.PathID(r, "dbID")
	if err != nil {
		return err
	}
	ref := pathTableRef(r)
	item, res, err := h.svc.Rows(r.Context(), apiv1.Actor(r), dbID, ref, parsePage(r))
	if err != nil {
		return err
	}
	return apiv1.OK(w, dataJSON{
		Database: toDatabaseJSON(item), Schema: ref.Schema, Name: ref.Table,
		Columns: res.Columns, Rows: cellsToJSON(res.Rows),
		Page: res.Page, PageSize: res.PageSize, HasNext: res.HasNext,
	})
}

// membaca nama schema dan tabel dari path, mux sudah membuang escape-nya
func pathTableRef(r *http.Request) domain.TableRef {
	return domain.TableRef{Schema: r.PathValue("schema"), Table: r.PathValue("table")}
}

// membaca ?page= dan ?size=. Nilai kosong atau rusak jatuh ke default, dan Normalize
// membatasi ukuran halaman supaya satu request tidak menarik terlalu banyak baris.
func parsePage(r *http.Request) domain.PageRequest {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	size, _ := strconv.Atoi(q.Get("size"))
	return domain.PageRequest{Page: page, PageSize: size}.Normalize()
}

// mengubah DatabaseItem menjadi bentuk respons
func toDatabaseJSON(it DatabaseItem) databaseJSON {
	return databaseJSON{ID: it.ID, Name: it.Name, DBMS: it.DBMS, AccessLevel: it.Level, CanEdit: it.CanEdit}
}

// mengelompokkan tabel per schema. Introspect sudah mengurutkan menurut schema,
// jadi grup baru dibuat tiap kali schema berganti.
func groupBySchema(tables []domain.Table) []schemaGroupJSON {
	groups := []schemaGroupJSON{}
	for _, t := range tables {
		if n := len(groups); n == 0 || groups[n-1].Name != t.Schema {
			groups = append(groups, schemaGroupJSON{Name: t.Schema, Tables: []tableItemJSON{}})
		}
		g := &groups[len(groups)-1]
		g.Tables = append(g.Tables, tableItemJSON{
			Schema: t.Schema, Name: t.Name, Kind: t.Kind, EstimatedRows: estimatedRows(t.EstimatedRows),
		})
	}
	return groups
}

// mengubah -1 (tidak diketahui) menjadi null
func estimatedRows(n int64) *int64 {
	if n < 0 {
		return nil
	}
	return &n
}

// toTableJSON menyusun detail tabel. Semua daftar dibuat non-nil supaya JSON-nya [] dan bukan null.
func toTableJSON(item DatabaseItem, t domain.Table) tableJSON {
	out := tableJSON{
		Database: toDatabaseJSON(item), Schema: t.Schema, Name: t.Name, Kind: t.Kind,
		EstimatedRows: estimatedRows(t.EstimatedRows),
		Columns:       make([]columnJSON, 0, len(t.Columns)),
		Constraints:   make([]constraintJSON, 0, len(t.Constraints)),
		Indexes:       make([]indexJSON, 0, len(t.Indexes)),
	}
	for _, c := range t.Columns {
		out.Columns = append(out.Columns, columnJSON{
			Name: c.Name, Position: c.Position, DataType: c.DataType, Nullable: c.Nullable, Default: c.Default,
		})
	}
	for _, c := range t.Constraints {
		out.Constraints = append(out.Constraints, constraintJSON{
			Name: c.Name, Type: c.Type, Columns: nonNil(c.Columns),
			RefSchema: c.RefSchema, RefTable: c.RefTable, RefColumns: c.RefColumns,
			OnUpdate: c.OnUpdate, OnDelete: c.OnDelete, CheckExpr: c.CheckExpr,
		})
	}
	for _, ix := range t.Indexes {
		out.Indexes = append(out.Indexes, indexJSON{
			Name: ix.Name, Method: ix.Method, Columns: nonNil(ix.Columns),
			Unique: ix.Unique, Primary: ix.Primary, Definition: ix.Definition,
		})
	}
	return out
}

// mengubah sel hasil query menjadi string, dan NULL menjadi nil
func cellsToJSON(rows [][]domain.Cell) [][]*string {
	out := make([][]*string, len(rows))
	for i, row := range rows {
		out[i] = make([]*string, len(row))
		for j, c := range row {
			if !c.Null {
				v := c.Value
				out[i][j] = &v
			}
		}
	}
	return out
}

// mengganti slice nil dengan slice kosong
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
