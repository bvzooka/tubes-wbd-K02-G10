package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
	"dbplatform/internal/domain"
)

const tablesSQL = `
SELECT TABLE_NAME, TABLE_TYPE, TABLE_ROWS
FROM information_schema.TABLES
WHERE TABLE_SCHEMA = ? AND TABLE_TYPE IN ('BASE TABLE', 'VIEW', 'SYSTEM VERSIONED')`

const columnsSQL = `
SELECT TABLE_NAME, COLUMN_NAME, ORDINAL_POSITION, COLUMN_TYPE, DATA_TYPE, IS_NULLABLE, COLUMN_DEFAULT, EXTRA
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = ?`

const constraintsSQL = `
SELECT TABLE_NAME, CONSTRAINT_NAME, CONSTRAINT_TYPE
FROM information_schema.TABLE_CONSTRAINTS
WHERE TABLE_SCHEMA = ? AND CONSTRAINT_TYPE IN ('PRIMARY KEY', 'UNIQUE', 'FOREIGN KEY'%s)`

const keyColumnsSQL = `
SELECT TABLE_NAME, CONSTRAINT_NAME, COLUMN_NAME, ORDINAL_POSITION,
       REFERENCED_TABLE_SCHEMA, REFERENCED_TABLE_NAME, REFERENCED_COLUMN_NAME
FROM information_schema.KEY_COLUMN_USAGE
WHERE TABLE_SCHEMA = ?`

const referentialSQL = `
SELECT TABLE_NAME, CONSTRAINT_NAME, UPDATE_RULE, DELETE_RULE
FROM information_schema.REFERENTIAL_CONSTRAINTS
WHERE CONSTRAINT_SCHEMA = ?`

const checksMySQLSQL = `
SELECT tc.TABLE_NAME, cc.CONSTRAINT_NAME, cc.CHECK_CLAUSE
FROM information_schema.CHECK_CONSTRAINTS cc
JOIN information_schema.TABLE_CONSTRAINTS tc
  ON tc.CONSTRAINT_SCHEMA = cc.CONSTRAINT_SCHEMA
 AND tc.CONSTRAINT_NAME = cc.CONSTRAINT_NAME
 AND tc.CONSTRAINT_TYPE = 'CHECK'
WHERE cc.CONSTRAINT_SCHEMA = ?`

const checksMariaDBSQL = `
SELECT TABLE_NAME, CONSTRAINT_NAME, CHECK_CLAUSE
FROM information_schema.CHECK_CONSTRAINTS
WHERE CONSTRAINT_SCHEMA = ?`

const indexesSQL = `
SELECT TABLE_NAME, INDEX_NAME, NON_UNIQUE, SEQ_IN_INDEX, COLUMN_NAME, SUB_PART, COLLATION, INDEX_TYPE, %s
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = ?`

type serverInfo struct {
	mariadb bool
	hasChecks bool 
	hasExpression bool 
}

type conKey struct{ table, name string }

func (d *Driver) Introspect(ctx context.Context) (*domain.Schema, error) {
	out := &domain.Schema{}
	spec := txSpec{readOnly: true, iso: sql.LevelRepeatableRead, timeout: 20 * time.Second}
	err := d.runTx(ctx, d.ro, spec, func(ctx context.Context, tx *sql.Tx) error {
		info, err := detectServer(ctx, tx)
		if err != nil {
			return err
		}
		schema := d.dbName

		pos := map[string]int{}
		rows, err := tx.QueryContext(ctx, tablesSQL, schema)
		if err != nil {
			return err
		}
		for rows.Next() {
			var name, ttype string
			var est sql.NullInt64
			if err := rows.Scan(&name, &ttype, &est); err != nil {
				rows.Close()
				return err
			}
			t := domain.Table{Schema: schema, Name: name, Kind: domain.TableKindTable, EstimatedRows: -1}
			if ttype == "VIEW" {
				t.Kind = domain.TableKindView
			} else if est.Valid && est.Int64 >= 0 {
				t.EstimatedRows = est.Int64
			}
			pos[name] = len(out.Tables)
			out.Tables = append(out.Tables, t)
		}
		if err := closeRows(rows); err != nil {
			return err
		}

		if err := readColumns(ctx, tx, schema, info, out, pos); err != nil {
			return err
		}
		if err := readConstraints(ctx, tx, schema, info, out, pos); err != nil {
			return err
		}
		return readIndexes(ctx, tx, schema, info, out, pos)
	})
	if err != nil {
		return nil, err
	}
	sortSchema(out)
	return out, nil
}

func closeRows(rows *sql.Rows) error {
	cerr := rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	return cerr
}

func detectServer(ctx context.Context, tx *sql.Tx) (serverInfo, error) {
	var info serverInfo
	var version string
	if err := tx.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		return info, err
	}
	info.mariadb = isMariaDB(version)

	var n int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = 'information_schema' AND TABLE_NAME = 'CHECK_CONSTRAINTS'`).Scan(&n)
	if err != nil {
		return info, err
	}
	info.hasChecks = n > 0

	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = 'information_schema' AND TABLE_NAME = 'STATISTICS' AND COLUMN_NAME = 'EXPRESSION'`).Scan(&n)
	if err != nil {
		return info, err
	}
	info.hasExpression = n > 0
	return info, nil
}

func isMariaDB(version string) bool {
	return strings.Contains(strings.ToLower(version), "mariadb")
}

func readColumns(ctx context.Context, tx *sql.Tx, schema string, info serverInfo, out *domain.Schema, pos map[string]int) error {
	rows, err := tx.QueryContext(ctx, columnsSQL, schema)
	if err != nil {
		return err
	}
	for rows.Next() {
		var tn, dataType, nullable, extra string
		var c domain.Column
		var def sql.NullString
		if err := rows.Scan(&tn, &c.Name, &c.Position, &c.DataType, &dataType, &nullable, &def, &extra); err != nil {
			rows.Close()
			return err
		}
		c.Nullable = nullable == "YES"
		c.Default = columnDefault(def, dataType, extra, info.mariadb)
		if i, ok := pos[tn]; ok {
			out.Tables[i].Columns = append(out.Tables[i].Columns, c)
		}
	}
	return closeRows(rows)
}

func columnDefault(def sql.NullString, dataType, extra string, mariadb bool) *string {
	lextra := strings.ToLower(extra)
	if !def.Valid {
		if strings.Contains(lextra, "auto_increment") {
			s := "AUTO_INCREMENT"
			return &s
		}
		return nil
	}
	v := def.String
	switch {
	case mariadb:
		if strings.EqualFold(v, "NULL") {
			return nil
		}
	case strings.Contains(lextra, "default_generated") || isNowDefault(v, dataType):
	case isQuotedType(dataType):
		v = "'" + strings.ReplaceAll(v, "'", "''") + "'"
	}
	return &v
}

func isNowDefault(v, dataType string) bool {
	switch strings.ToLower(dataType) {
	case "timestamp", "datetime":
		return strings.HasPrefix(strings.ToUpper(v), "CURRENT_TIMESTAMP")
	}
	return false
}

func isQuotedType(dataType string) bool {
	switch strings.ToLower(dataType) {
	case "char", "varchar", "tinytext", "text", "mediumtext", "longtext",
		"enum", "set", "date", "datetime", "timestamp", "time", "year", "json",
		"binary", "varbinary", "tinyblob", "blob", "mediumblob", "longblob":
		return true
	}
	return false
}

type keyUsage struct {
	cols []string
	refSchema string
	refTable string
	refCols []string
}

func readConstraints(ctx context.Context, tx *sql.Tx, schema string, info serverInfo, out *domain.Schema, pos map[string]int) error {
	extra := ""
	if info.hasChecks {
		extra = ", 'CHECK'"
	}
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(constraintsSQL, extra), schema)
	if err != nil {
		return err
	}
	type decl struct {
		key   conKey
		ctype string
	}
	var decls []decl
	for rows.Next() {
		var d decl
		if err := rows.Scan(&d.key.table, &d.key.name, &d.ctype); err != nil {
			rows.Close()
			return err
		}
		decls = append(decls, d)
	}
	if err := closeRows(rows); err != nil {
		return err
	}

	usage := map[conKey]*keyUsage{}
	type part struct {
		ord int
		col, rcol string
		rschema, rtable sql.NullString
	}
	parts := map[conKey][]part{}
	rows, err = tx.QueryContext(ctx, keyColumnsSQL, schema)
	if err != nil {
		return err
	}
	for rows.Next() {
		var k conKey
		var p part
		var rcol sql.NullString
		if err := rows.Scan(&k.table, &k.name, &p.col, &p.ord, &p.rschema, &p.rtable, &rcol); err != nil {
			rows.Close()
			return err
		}
		p.rcol = rcol.String
		parts[k] = append(parts[k], p)
	}
	if err := closeRows(rows); err != nil {
		return err
	}
	for k, ps := range parts {
		sort.SliceStable(ps, func(i, j int) bool { return ps[i].ord < ps[j].ord })
		u := &keyUsage{}
		for _, p := range ps {
			u.cols = append(u.cols, p.col)
			if p.rtable.Valid {
				u.refSchema, u.refTable = p.rschema.String, p.rtable.String
				u.refCols = append(u.refCols, p.rcol)
			}
		}
		usage[k] = u
	}

	type rule struct{ upd, del string }
	rules := map[conKey]rule{}
	rows, err = tx.QueryContext(ctx, referentialSQL, schema)
	if err != nil {
		return err
	}
	for rows.Next() {
		var k conKey
		var r rule
		if err := rows.Scan(&k.table, &k.name, &r.upd, &r.del); err != nil {
			rows.Close()
			return err
		}
		rules[k] = r
	}
	if err := closeRows(rows); err != nil {
		return err
	}

	checks := map[conKey]string{}
	if info.hasChecks {
		q := checksMySQLSQL
		if info.mariadb {
			q = checksMariaDBSQL
		}
		rows, err = tx.QueryContext(ctx, q, schema)
		if err != nil {
			return err
		}
		for rows.Next() {
			var k conKey
			var clause string
			if err := rows.Scan(&k.table, &k.name, &clause); err != nil {
				rows.Close()
				return err
			}
			checks[k] = clause
		}
		if err := closeRows(rows); err != nil {
			return err
		}
	}

	for _, dc := range decls {
		i, ok := pos[dc.key.table]
		if !ok {
			continue
		}
		c := domain.Constraint{Name: dc.key.name}
		u := usage[dc.key]
		switch dc.ctype {
		case "PRIMARY KEY":
			c.Type = domain.ConstraintPrimaryKey
			if u != nil {
				c.Columns = u.cols
			}
		case "UNIQUE":
			c.Type = domain.ConstraintUnique
			if u != nil {
				c.Columns = u.cols
			}
		case "FOREIGN KEY":
			c.Type = domain.ConstraintForeignKey
			if u != nil {
				c.Columns = u.cols
				c.RefSchema, c.RefTable, c.RefColumns = u.refSchema, u.refTable, u.refCols
			}
			r := rules[dc.key]
			c.OnUpdate, c.OnDelete = r.upd, r.del
		case "CHECK":
			clause, ok := checks[dc.key]
			if !ok {
				continue
			}
			c.Type = domain.ConstraintCheck
			c.CheckExpr = checkExpr(clause)
		default:
			continue
		}
		out.Tables[i].Constraints = append(out.Tables[i].Constraints, c)
	}
	return nil
}

// checkExpr menyamakan bentuk dengan pg_get_constraintdef: "CHECK (ekspresi)".
func checkExpr(clause string) string {
	clause = strings.TrimSpace(clause)
	if !strings.HasPrefix(clause, "(") {
		clause = "(" + clause + ")"
	}
	return "CHECK " + clause
}

type indexPart struct {
	seq int
	text string 
	quoted string 
}

func readIndexes(ctx context.Context, tx *sql.Tx, schema string, info serverInfo, out *domain.Schema, pos map[string]int) error {
	expr := "NULL"
	if info.hasExpression {
		expr = "EXPRESSION"
	}
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(indexesSQL, expr), schema)
	if err != nil {
		return err
	}
	type ikey struct{ table, name string }
	type idx struct {
		unique bool
		method string
		parts  []indexPart
	}
	got := map[ikey]*idx{}
	for rows.Next() {
		var k ikey
		var nonUnique, seq int
		var col, collation, expression sql.NullString
		var sub sql.NullInt64
		var itype string
		if err := rows.Scan(&k.table, &k.name, &nonUnique, &seq, &col, &sub, &collation, &itype, &expression); err != nil {
			rows.Close()
			return err
		}
		ix := got[k]
		if ix == nil {
			ix = &idx{unique: nonUnique == 0, method: strings.ToLower(itype)}
			got[k] = ix
		}
		ix.parts = append(ix.parts, newIndexPart(seq, col, sub, collation, expression))
	}
	if err := closeRows(rows); err != nil {
		return err
	}

	for k, ix := range got {
		i, ok := pos[k.table]
		if !ok {
			continue
		}
		sort.SliceStable(ix.parts, func(a, b int) bool { return ix.parts[a].seq < ix.parts[b].seq })
		in := domain.Index{
			Name: k.name,
			Unique: ix.unique,
			Primary: k.name == "PRIMARY",
			Method: ix.method,
		}
		quoted := make([]string, len(ix.parts))
		for j, p := range ix.parts {
			in.Columns = append(in.Columns, p.text)
			quoted[j] = p.quoted
		}
		in.Definition = indexDefinition(schema, k.table, in, quoted)
		out.Tables[i].Indexes = append(out.Tables[i].Indexes, in)
	}
	return nil
}

func newIndexPart(seq int, col sql.NullString, sub sql.NullInt64, collation, expression sql.NullString) indexPart {
	p := indexPart{seq: seq}
	switch {
	case col.Valid:
		p.text = col.String
		p.quoted = quoteIdent(col.String)
		if sub.Valid && sub.Int64 > 0 {
			p.quoted += fmt.Sprintf("(%d)", sub.Int64)
		}
	case expression.Valid:
		p.text = expression.String
		p.quoted = "(" + expression.String + ")"
	default:
		p.text, p.quoted = "?", "?"
	}
	if collation.Valid && collation.String == "D" {
		p.quoted += " DESC"
	}
	return p
}

func indexDefinition(schema, table string, ix domain.Index, quotedParts []string) string {
	target := quoteIdent(schema) + "." + quoteIdent(table)
	cols := strings.Join(quotedParts, ", ")
	if ix.Primary {
		return "ALTER TABLE " + target + " ADD PRIMARY KEY (" + cols + ")"
	}
	kind := ""
	switch {
	case ix.Method == "fulltext":
		kind = "FULLTEXT "
	case ix.Method == "spatial":
		kind = "SPATIAL "
	case ix.Unique:
		kind = "UNIQUE "
	}
	def := "CREATE " + kind + "INDEX " + quoteIdent(ix.Name) + " ON " + target + " (" + cols + ")"
	if ix.Method == "btree" || ix.Method == "hash" {
		def += " USING " + strings.ToUpper(ix.Method)
	}
	return def
}

func sortSchema(s *domain.Schema) {
	sort.SliceStable(s.Tables, func(i, j int) bool {
		if s.Tables[i].Schema != s.Tables[j].Schema {
			return s.Tables[i].Schema < s.Tables[j].Schema
		}
		return s.Tables[i].Name < s.Tables[j].Name
	})
	for i := range s.Tables {
		t := &s.Tables[i]
		sort.SliceStable(t.Columns, func(a, b int) bool { return t.Columns[a].Position < t.Columns[b].Position })
		sort.SliceStable(t.Constraints, func(a, b int) bool { return t.Constraints[a].Name < t.Constraints[b].Name })
		sort.SliceStable(t.Indexes, func(a, b int) bool { return t.Indexes[a].Name < t.Indexes[b].Name })
	}
}
