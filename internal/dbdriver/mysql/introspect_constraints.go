package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"dbplatform/internal/domain"
)

// MySQL memecah informasi constraint ke beberapa tabel information_schema: daftar constraint,
// kolom tiap key, aturan foreign key, dan ekspresi check. readConstraints menggabungkannya kembali.
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

// nilai kolom CONSTRAINT_TYPE yang dikenali
const (
	ctypePrimaryKey = "PRIMARY KEY"
	ctypeUnique     = "UNIQUE"
	ctypeForeignKey = "FOREIGN KEY"
	ctypeCheck      = "CHECK"
)

// menunjuk satu constraint lewat nama tabel dan nama constraint-nya
type conKey struct{ table, name string }

// adalah satu baris TABLE_CONSTRAINTS, sebelum dilengkapi kolom dan aturannya
type constraintDecl struct {
	key   conKey
	ctype string
}

// adalah kolom yang dipakai satu constraint, dan tabel rujukannya untuk foreign key
type keyUsage struct {
	cols      []string
	refSchema string
	refTable  string
	refCols   []string
}

// aturan ON UPDATE dan ON DELETE sebuah foreign key
type fkRule struct{ onUpdate, onDelete string }

// membaca keempat sumber constraint, lalu menggabungkannya per tabel
func readConstraints(ctx context.Context, tx *sql.Tx, schema string, info serverInfo, out *domain.Schema, pos map[string]int) error {
	decls, err := readConstraintDecls(ctx, tx, schema, info)
	if err != nil {
		return fmt.Errorf("read constraint list: %w", err)
	}
	usage, err := readKeyUsage(ctx, tx, schema)
	if err != nil {
		return fmt.Errorf("read key usage: %w", err)
	}
	rules, err := readFKRules(ctx, tx, schema)
	if err != nil {
		return fmt.Errorf("read foreign key rules: %w", err)
	}
	checks, err := readCheckClauses(ctx, tx, schema, info)
	if err != nil {
		return fmt.Errorf("read check clauses: %w", err)
	}

	for _, dc := range decls {
		i, ok := pos[dc.key.table]
		if !ok {
			continue
		}
		c, ok := buildConstraint(dc, usage[dc.key], rules[dc.key], checks)
		if !ok {
			continue
		}
		out.Tables[i].Constraints = append(out.Tables[i].Constraints, c)
	}
	return nil
}

// membaca daftar constraint, CHECK ikut dibaca hanya bila server mendukungnya
func readConstraintDecls(ctx context.Context, tx *sql.Tx, schema string, info serverInfo) ([]constraintDecl, error) {
	extra := ""
	if info.hasChecks {
		extra = ", 'CHECK'"
	}
	var decls []constraintDecl
	err := eachRow(ctx, tx, fmt.Sprintf(constraintsSQL, extra), []any{schema}, func(rows *sql.Rows) error {
		var d constraintDecl
		if err := rows.Scan(&d.key.table, &d.key.name, &d.ctype); err != nil {
			return err
		}
		decls = append(decls, d)
		return nil
	})
	return decls, err
}

// membaca kolom tiap constraint, diurutkan menurut posisinya di key
func readKeyUsage(ctx context.Context, tx *sql.Tx, schema string) (map[conKey]*keyUsage, error) {
	type part struct {
		ord             int
		col, rcol       string
		rschema, rtable sql.NullString
	}
	parts := map[conKey][]part{}
	err := eachRow(ctx, tx, keyColumnsSQL, []any{schema}, func(rows *sql.Rows) error {
		var k conKey
		var p part
		var rcol sql.NullString
		if err := rows.Scan(&k.table, &k.name, &p.col, &p.ord, &p.rschema, &p.rtable, &rcol); err != nil {
			return err
		}
		p.rcol = rcol.String
		parts[k] = append(parts[k], p)
		return nil
	})
	if err != nil {
		return nil, err
	}

	usage := make(map[conKey]*keyUsage, len(parts))
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
	return usage, nil
}

// membaca aturan ON UPDATE dan ON DELETE khusus foreign key
func readFKRules(ctx context.Context, tx *sql.Tx, schema string) (map[conKey]fkRule, error) {
	rules := map[conKey]fkRule{}
	err := eachRow(ctx, tx, referentialSQL, []any{schema}, func(rows *sql.Rows) error {
		var k conKey
		var r fkRule
		if err := rows.Scan(&k.table, &k.name, &r.onUpdate, &r.onDelete); err != nil {
			return err
		}
		rules[k] = r
		return nil
	})
	return rules, err
}

// membaca ekspresi check, tabelnya hanya ada di server yang mendukung CHECK
func readCheckClauses(ctx context.Context, tx *sql.Tx, schema string, info serverInfo) (map[conKey]string, error) {
	checks := map[conKey]string{}
	if !info.hasChecks {
		return checks, nil
	}
	q := checksMySQLSQL
	if info.mariadb {
		q = checksMariaDBSQL
	}
	err := eachRow(ctx, tx, q, []any{schema}, func(rows *sql.Rows) error {
		var k conKey
		var clause string
		if err := rows.Scan(&k.table, &k.name, &clause); err != nil {
			return err
		}
		checks[k] = clause
		return nil
	})
	return checks, err
}

// buildConstraint menyusun satu domain.Constraint dari deklarasi, kolom, aturan, dan ekspresi check
// ok bernilai false bila constraint harus dilewati: jenisnya tidak dikenal, or CHECK tanpa ekspresi
func buildConstraint(dc constraintDecl, u *keyUsage, r fkRule, checks map[conKey]string) (c domain.Constraint, ok bool) {
	c = domain.Constraint{Name: dc.key.name}
	switch dc.ctype {
	case ctypePrimaryKey:
		c.Type = domain.ConstraintPrimaryKey
		if u != nil {
			c.Columns = u.cols
		}
	case ctypeUnique:
		c.Type = domain.ConstraintUnique
		if u != nil {
			c.Columns = u.cols
		}
	case ctypeForeignKey:
		c.Type = domain.ConstraintForeignKey
		if u != nil {
			c.Columns = u.cols
			c.RefSchema, c.RefTable, c.RefColumns = u.refSchema, u.refTable, u.refCols
		}
		c.OnUpdate, c.OnDelete = r.onUpdate, r.onDelete
	case ctypeCheck:
		clause, found := checks[dc.key]
		if !found {
			return c, false
		}
		c.Type = domain.ConstraintCheck
		c.CheckExpr = checkExpr(clause)
	default:
		return c, false
	}
	return c, true
}

// menyamakan bentuk dengan pg_get_constraintdef: "CHECK (ekspresi)".
func checkExpr(clause string) string {
	clause = strings.TrimSpace(clause)
	if !strings.HasPrefix(clause, "(") {
		clause = "(" + clause + ")"
	}
	return "CHECK " + clause
}
