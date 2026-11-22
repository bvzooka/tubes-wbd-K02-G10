package schemadiff

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"dbplatform/internal/domain"
)

// jenis perubahan, urutannya mengikuti urutan SQL yang aman dijalankan
const (
	KindCreateSchema       = "create_schema"
	KindCreateTable        = "create_table"
	KindAddColumn          = "add_column"
	KindAlterColumnType    = "alter_column_type"
	KindAlterColumnNull    = "alter_column_null"
	KindAlterColumnDefault = "alter_column_default"
	KindDropConstraint     = "drop_constraint"
	KindAddConstraint      = "add_constraint"
	KindDropIndex          = "drop_index"
	KindCreateIndex        = "create_index"
	KindDropColumn         = "drop_column"
	KindDropTable          = "drop_table"
)

// schema public selalu ada di PostgreSQL jadi tidak perlu dibuat
const defaultSchema = "public"

// Change adalah satu perubahan yang bisa dipilih user, ID dihitung dari isinya supaya stabil antar perhitungan
type Change struct {
	ID          string
	Kind        string
	Object      string
	SQL         string
	Destructive bool
}

// tableKey menunjuk satu tabel lewat schema dan nama
type tableKey struct{ schema, name string }

// View diabaikan, cuma tabel yang dibandingin
func tablesOf(s *domain.Schema) map[tableKey]domain.Table {
	out := map[tableKey]domain.Table{}
	if s == nil {
		return out
	}
	for _, t := range s.Tables {
		if t.Kind == domain.TableKindView {
			continue
		}
		out[tableKey{t.Schema, t.Name}] = t
	}
	return out
}

// relationSchemas mengumpulkan nama schema yang sudah dipakai
func relationSchemas(s *domain.Schema) map[string]bool {
	out := map[string]bool{}
	if s == nil {
		return out
	}
	for _, t := range s.Tables {
		out[t.Schema] = true
	}
	return out
}

// sortedKeys, sortedColumns, sortedConstraints, dan sortedIndexes mengurutkan hasil supaya SQL yang dibuat selalu sama
func sortedKeys(m map[tableKey]domain.Table) []tableKey {
	keys := make([]tableKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].schema != keys[j].schema {
			return keys[i].schema < keys[j].schema
		}
		return keys[i].name < keys[j].name
	})
	return keys
}

func sortedColumns(t domain.Table) []domain.Column {
	cols := append([]domain.Column(nil), t.Columns...)
	sort.SliceStable(cols, func(i, j int) bool { return cols[i].Position < cols[j].Position })
	return cols
}

// columnIndex dan constraintIndex membuat peta nama ke objek supaya pencarian cepat
func columnIndex(t domain.Table) map[string]domain.Column {
	out := make(map[string]domain.Column, len(t.Columns))
	for _, c := range t.Columns {
		out[c.Name] = c
	}
	return out
}

func constraintIndex(t domain.Table) map[string]domain.Constraint {
	out := make(map[string]domain.Constraint, len(t.Constraints))
	for _, c := range t.Constraints {
		out[c.Name] = c
	}
	return out
}

func sortedConstraints(t domain.Table) []domain.Constraint {
	list := append([]domain.Constraint(nil), t.Constraints...)
	sort.SliceStable(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list
}

func sortedIndexes(t domain.Table) []domain.Index {
	list := append([]domain.Index(nil), t.Indexes...)
	sort.SliceStable(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list
}

// sameStrings membandingkan dua daftar nama dengan urutan yang sama
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// sameConstraint membandingkan jenis, kolom, dan detail sesuai jenisnya (rujukan untuk foreign key, ekspresi untuk check)
func sameConstraint(a, b domain.Constraint) bool {
	if a.Type != b.Type || !sameStrings(a.Columns, b.Columns) {
		return false
	}
	switch a.Type {
	case domain.ConstraintForeignKey:
		return a.RefSchema == b.RefSchema && a.RefTable == b.RefTable && sameStrings(a.RefColumns, b.RefColumns) &&
			a.OnUpdate == b.OnUpdate && a.OnDelete == b.OnDelete
	case domain.ConstraintCheck:
		return strings.TrimSpace(a.CheckExpr) == strings.TrimSpace(b.CheckExpr)
	}
	return true
}

// Index otomatis dari primary key atau constraint dilewatin
func standaloneIndexes(t domain.Table) map[string]domain.Index {
	backed := constraintIndex(t)
	out := map[string]domain.Index{}
	for _, ix := range t.Indexes {
		if ix.Primary {
			continue
		}
		if _, ok := backed[ix.Name]; ok {
			continue
		}
		out[ix.Name] = ix
	}
	return out
}

// sameDefault membandingkan default tanpa memedulikan spasi dan mengabaikan default sequence
func sameDefault(a, b *string) bool {
	a, b = usableDefault(a), usableDefault(b)
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return strings.TrimSpace(*a) == strings.TrimSpace(*b)
}

// PK dulu baru FK biar tabel rujukannya udah ada
func constraintRank(t domain.ConstraintType) int {
	switch t {
	case domain.ConstraintPrimaryKey:
		return 0
	case domain.ConstraintUnique:
		return 1
	case domain.ConstraintCheck:
		return 2
	}
	return 3
}

// pendingConstraint menahan constraint yang akan ditambah sampai semua tabel dan kolom ada, lalu diurutkan
type pendingConstraint struct {
	table domain.Table
	c     domain.Constraint
}

// diffBuilder mengumpulkan perubahan dalam daftar terpisah per jenis, supaya urutan akhirnya bisa diatur di ordered
type diffBuilder struct {
	cur, des map[tableKey]domain.Table

	createSchemas, createTables, addColumns, alterColumns                                                      []Change
	dropForeignKeys, dropOtherConstraints, addConstraints, dropIndexes, createIndexes, dropColumns, dropTables []Change
	pendingAdds                                                                                                []pendingConstraint
}

// current = tujuan, desired = sumber
func Diff(current, desired *domain.Schema) []Change {
	b := &diffBuilder{cur: tablesOf(current), des: tablesOf(desired)}
	desKeys := sortedKeys(b.des)

	b.collectNewSchemas(relationSchemas(current), desKeys)
	for _, k := range desKeys {
		if ct, exists := b.cur[k]; exists {
			b.diffExistingTable(k, ct, b.des[k])
		} else {
			b.addNewTable(k, b.des[k])
		}
	}
	b.collectDroppedTables(sortedKeys(b.cur))
	b.collectConstraintAdds()
	return b.ordered()
}

// collectNewSchemas membuat schema yang dipakai sumber tapi belum ada di tujuan, sekali saja walau dipakai banyak tabel
func (b *diffBuilder) collectNewSchemas(curSchemas map[string]bool, desKeys []tableKey) {
	seen := map[string]bool{}
	for _, k := range desKeys {
		if k.schema != defaultSchema && !curSchemas[k.schema] && !seen[k.schema] {
			seen[k.schema] = true
			b.createSchemas = append(b.createSchemas, Change{Kind: KindCreateSchema, Object: k.schema, SQL: createSchemaSQL(k.schema)})
		}
	}
}

// addNewTable membuat tabel yang belum ada di tujuan, constraint ditahan dulu dan index langsung dibuat
func (b *diffBuilder) addNewTable(k tableKey, dt domain.Table) {
	object := k.schema + "." + k.name
	b.createTables = append(b.createTables, Change{Kind: KindCreateTable, Object: object, SQL: createTableSQL(dt)})
	for _, c := range sortedConstraints(dt) {
		b.pendingAdds = append(b.pendingAdds, pendingConstraint{dt, c})
	}
	idx := standaloneIndexes(dt)
	for _, ix := range sortedIndexes(dt) {
		if _, ok := idx[ix.Name]; ok {
			b.createIndexes = append(b.createIndexes, Change{Kind: KindCreateIndex, Object: object + "." + ix.Name, SQL: createIndexSQL(ix)})
		}
	}
}

// diffExistingTable membandingkan tabel yang ada di keduanya: kolom, constraint, lalu index
func (b *diffBuilder) diffExistingTable(k tableKey, ct, dt domain.Table) {
	object := k.schema + "." + k.name
	b.diffColumns(object, ct, dt)
	b.diffConstraints(object, ct, dt)
	b.diffIndexes(object, ct, dt)
}

// diffColumns menambah kolom baru, mengubah tipe, null, dan default yang berbeda, dan menghapus kolom yang tidak ada di sumber
func (b *diffBuilder) diffColumns(object string, ct, dt domain.Table) {
	curCols := columnIndex(ct)
	desCols := columnIndex(dt)
	for _, dc := range sortedColumns(dt) {
		cc, ok := curCols[dc.Name]
		colObject := object + "." + dc.Name
		if !ok {
			b.addColumns = append(b.addColumns, Change{Kind: KindAddColumn, Object: colObject, SQL: addColumnSQL(dt, dc)})
			continue
		}
		if cc.DataType != dc.DataType {
			b.alterColumns = append(b.alterColumns, Change{Kind: KindAlterColumnType, Object: colObject, SQL: alterTypeSQL(dt, dc)})
		}
		if cc.Nullable != dc.Nullable {
			b.alterColumns = append(b.alterColumns, Change{Kind: KindAlterColumnNull, Object: colObject, SQL: alterNullSQL(dt, dc)})
		}
		if !sameDefault(cc.Default, dc.Default) {
			b.alterColumns = append(b.alterColumns, Change{Kind: KindAlterColumnDefault, Object: colObject, SQL: alterDefaultSQL(dt, dc)})
		}
	}
	for _, cc := range sortedColumns(ct) {
		if _, ok := desCols[cc.Name]; !ok {
			// Desctructive: true --> ngapus kolom atau tabel bisa ngilangin data
			b.dropColumns = append(b.dropColumns, Change{Kind: KindDropColumn, Object: object + "." + cc.Name, SQL: dropColumnSQL(ct, cc), Destructive: true})
		}
	}
}

// diffConstraints menghapus constraint yang berbeda lalu menahannya untuk ditambah lagi, foreign key dipisah karena harus lepas lebih awal
func (b *diffBuilder) diffConstraints(object string, ct, dt domain.Table) {
	curCons, desCons := constraintIndex(ct), constraintIndex(dt)
	for _, c := range sortedConstraints(ct) {
		if d, ok := desCons[c.Name]; !ok || !sameConstraint(c, d) {
			drop := Change{Kind: KindDropConstraint, Object: object + "." + c.Name, SQL: dropConstraintSQL(ct, c)}
			if c.Type == domain.ConstraintForeignKey {
				b.dropForeignKeys = append(b.dropForeignKeys, drop)
			} else {
				b.dropOtherConstraints = append(b.dropOtherConstraints, drop)
			}
		}
	}
	for _, c := range sortedConstraints(dt) {
		if cc, ok := curCons[c.Name]; !ok || !sameConstraint(cc, c) {
			b.pendingAdds = append(b.pendingAdds, pendingConstraint{dt, c})
		}
	}
}

// diffIndexes menghapus index yang berubah definisinya lalu membuatnya ulang, index dari primary key atau constraint dilewati
func (b *diffBuilder) diffIndexes(object string, ct, dt domain.Table) {
	curIdx, desIdx := standaloneIndexes(ct), standaloneIndexes(dt)
	for _, ix := range sortedIndexes(ct) {
		if _, tracked := curIdx[ix.Name]; !tracked {
			continue
		}
		if d, ok := desIdx[ix.Name]; !ok || strings.TrimSpace(d.Definition) != strings.TrimSpace(ix.Definition) {
			b.dropIndexes = append(b.dropIndexes, Change{Kind: KindDropIndex, Object: object + "." + ix.Name, SQL: dropIndexSQL(ct, ix)})
		}
	}
	for _, ix := range sortedIndexes(dt) {
		if _, tracked := desIdx[ix.Name]; !tracked {
			continue
		}
		if c, ok := curIdx[ix.Name]; !ok || strings.TrimSpace(c.Definition) != strings.TrimSpace(ix.Definition) {
			b.createIndexes = append(b.createIndexes, Change{Kind: KindCreateIndex, Object: object + "." + ix.Name, SQL: createIndexSQL(ix)})
		}
	}
}

// collectDroppedTables menghapus tabel yang hanya ada di tujuan, ditandai destruktif karena datanya hilang
func (b *diffBuilder) collectDroppedTables(curKeys []tableKey) {
	for _, k := range curKeys {
		if _, ok := b.des[k]; !ok {
			b.dropTables = append(b.dropTables, Change{Kind: KindDropTable, Object: k.schema + "." + k.name, SQL: dropTableSQL(b.cur[k]), Destructive: true})
		}
	}
}

// collectConstraintAdds mengurutkan constraint yang ditahan lalu menjadikannya perubahan
func (b *diffBuilder) collectConstraintAdds() {
	// primary key dan unique ditambah lebih dulu supaya foreign key punya yang dirujuk
	sort.SliceStable(b.pendingAdds, func(i, j int) bool {
		return constraintRank(b.pendingAdds[i].c.Type) < constraintRank(b.pendingAdds[j].c.Type)
	})
	for _, p := range b.pendingAdds {
		b.addConstraints = append(b.addConstraints, Change{
			Kind: KindAddConstraint, Object: p.table.Schema + "." + p.table.Name + "." + p.c.Name, SQL: addConstraintSQL(p.table, p.c),
		})
	}
}

// ordered menggabungkan semua daftar dengan urutan yang aman dijalankan lalu memberi ID tiap perubahan
func (b *diffBuilder) ordered() []Change {
	var all []Change
	// Urutan gabungan: buat, ubah, lepas FK lama, tambah baru, hapus
	for _, group := range [][]Change{
		b.createSchemas, b.createTables, b.addColumns, b.alterColumns, b.dropForeignKeys, b.dropOtherConstraints, b.addConstraints,
		b.dropIndexes, b.createIndexes, b.dropColumns, b.dropTables,
	} {
		all = append(all, group...)
	}
	for i := range all {
		all[i].ID = changeID(all[i])
	}
	return all
}

// changeID membuat ID pendek dari isi perubahan, jadi perubahan yang sama selalu punya ID yang sama
func changeID(c Change) string {
	sum := sha256.Sum256([]byte(c.Kind + "\x00" + c.Object + "\x00" + c.SQL))
	return hex.EncodeToString(sum[:])[:12]
}

// SQL menggabungkan SQL semua perubahan jadi satu skrip migration
func SQL(changes []Change) string {
	parts := make([]string, len(changes))
	for i, c := range changes {
		parts[i] = c.SQL
	}
	return strings.Join(parts, "\n")
}

// Select memilih perubahan yang ID-nya ada di ids, urutan aslinya dipertahankan
func Select(changes []Change, ids map[string]bool) []Change {
	var out []Change
	for _, c := range changes {
		if ids[c.ID] {
			out = append(out, c)
		}
	}
	return out
}
