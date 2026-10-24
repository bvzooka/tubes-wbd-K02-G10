package schemadiff

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"dbplatform/internal/domain"
)

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

const defaultSchema = "public"

type Change struct {
	ID          string
	Kind        string
	Object      string
	SQL         string
	Destructive bool
}

type tableKey struct{ schema, name string }

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

func sameDefault(a, b *string) bool {
	a, b = usableDefault(a), usableDefault(b)
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return strings.TrimSpace(*a) == strings.TrimSpace(*b)
}

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

type pendingConstraint struct {
	table domain.Table
	c     domain.Constraint
}

func Diff(current, desired *domain.Schema) []Change {
	cur, des := tablesOf(current), tablesOf(desired)
	curSchemas := relationSchemas(current)
	curKeys, desKeys := sortedKeys(cur), sortedKeys(des)

	var createSchemas, createTables, addColumns, alterColumns []Change
	var dropForeignKeys, dropOtherConstraints, addConstraints, dropIndexes, createIndexes, dropColumns, dropTables []Change
	var pendingAdds []pendingConstraint

	seenSchemas := map[string]bool{}
	for _, k := range desKeys {
		if k.schema != defaultSchema && !curSchemas[k.schema] && !seenSchemas[k.schema] {
			seenSchemas[k.schema] = true
			createSchemas = append(createSchemas, Change{Kind: KindCreateSchema, Object: k.schema, SQL: createSchemaSQL(k.schema)})
		}
	}

	for _, k := range desKeys {
		dt := des[k]
		ct, exists := cur[k]
		object := k.schema + "." + k.name

		if !exists {
			createTables = append(createTables, Change{Kind: KindCreateTable, Object: object, SQL: createTableSQL(dt)})
			for _, c := range sortedConstraints(dt) {
				pendingAdds = append(pendingAdds, pendingConstraint{dt, c})
			}
			idx := standaloneIndexes(dt)
			for _, ix := range sortedIndexes(dt) {
				if _, ok := idx[ix.Name]; ok {
					createIndexes = append(createIndexes, Change{Kind: KindCreateIndex, Object: object + "." + ix.Name, SQL: createIndexSQL(ix)})
				}
			}
			continue
		}

		curCols := columnIndex(ct)
		desCols := columnIndex(dt)
		for _, dc := range sortedColumns(dt) {
			cc, ok := curCols[dc.Name]
			colObject := object + "." + dc.Name
			if !ok {
				addColumns = append(addColumns, Change{Kind: KindAddColumn, Object: colObject, SQL: addColumnSQL(dt, dc)})
				continue
			}
			if cc.DataType != dc.DataType {
				alterColumns = append(alterColumns, Change{Kind: KindAlterColumnType, Object: colObject, SQL: alterTypeSQL(dt, dc)})
			}
			if cc.Nullable != dc.Nullable {
				alterColumns = append(alterColumns, Change{Kind: KindAlterColumnNull, Object: colObject, SQL: alterNullSQL(dt, dc)})
			}
			if !sameDefault(cc.Default, dc.Default) {
				alterColumns = append(alterColumns, Change{Kind: KindAlterColumnDefault, Object: colObject, SQL: alterDefaultSQL(dt, dc)})
			}
		}
		for _, cc := range sortedColumns(ct) {
			if _, ok := desCols[cc.Name]; !ok {
				dropColumns = append(dropColumns, Change{Kind: KindDropColumn, Object: object + "." + cc.Name, SQL: dropColumnSQL(ct, cc), Destructive: true})
			}
		}

		curCons, desCons := constraintIndex(ct), constraintIndex(dt)
		for _, c := range sortedConstraints(ct) {
			if d, ok := desCons[c.Name]; !ok || !sameConstraint(c, d) {
				drop := Change{Kind: KindDropConstraint, Object: object + "." + c.Name, SQL: dropConstraintSQL(ct, c)}
				if c.Type == domain.ConstraintForeignKey {
					dropForeignKeys = append(dropForeignKeys, drop)
				} else {
					dropOtherConstraints = append(dropOtherConstraints, drop)
				}
			}
		}
		for _, c := range sortedConstraints(dt) {
			if cc, ok := curCons[c.Name]; !ok || !sameConstraint(cc, c) {
				pendingAdds = append(pendingAdds, pendingConstraint{dt, c})
			}
		}

		curIdx, desIdx := standaloneIndexes(ct), standaloneIndexes(dt)
		for _, ix := range sortedIndexes(ct) {
			if _, tracked := curIdx[ix.Name]; !tracked {
				continue
			}
			if d, ok := desIdx[ix.Name]; !ok || strings.TrimSpace(d.Definition) != strings.TrimSpace(ix.Definition) {
				dropIndexes = append(dropIndexes, Change{Kind: KindDropIndex, Object: object + "." + ix.Name, SQL: dropIndexSQL(ct, ix)})
			}
		}
		for _, ix := range sortedIndexes(dt) {
			if _, tracked := desIdx[ix.Name]; !tracked {
				continue
			}
			if c, ok := curIdx[ix.Name]; !ok || strings.TrimSpace(c.Definition) != strings.TrimSpace(ix.Definition) {
				createIndexes = append(createIndexes, Change{Kind: KindCreateIndex, Object: object + "." + ix.Name, SQL: createIndexSQL(ix)})
			}
		}
	}

	for _, k := range curKeys {
		if _, ok := des[k]; !ok {
			dropTables = append(dropTables, Change{Kind: KindDropTable, Object: k.schema + "." + k.name, SQL: dropTableSQL(cur[k]), Destructive: true})
		}
	}

	sort.SliceStable(pendingAdds, func(i, j int) bool {
		return constraintRank(pendingAdds[i].c.Type) < constraintRank(pendingAdds[j].c.Type)
	})
	for _, p := range pendingAdds {
		addConstraints = append(addConstraints, Change{
			Kind: KindAddConstraint, Object: p.table.Schema + "." + p.table.Name + "." + p.c.Name, SQL: addConstraintSQL(p.table, p.c),
		})
	}

	var all []Change
	for _, group := range [][]Change{
		createSchemas, createTables, addColumns, alterColumns, dropForeignKeys, dropOtherConstraints, addConstraints,
		dropIndexes, createIndexes, dropColumns, dropTables,
	} {
		all = append(all, group...)
	}
	for i := range all {
		all[i].ID = changeID(all[i])
	}
	return all
}

func changeID(c Change) string {
	sum := sha256.Sum256([]byte(c.Kind + "\x00" + c.Object + "\x00" + c.SQL))
	return hex.EncodeToString(sum[:])[:12]
}

func SQL(changes []Change) string {
	parts := make([]string, len(changes))
	for i, c := range changes {
		parts[i] = c.SQL
	}
	return strings.Join(parts, "\n")
}

func Select(changes []Change, ids map[string]bool) []Change {
	var out []Change
	for _, c := range changes {
		if ids[c.ID] {
			out = append(out, c)
		}
	}
	return out
}
