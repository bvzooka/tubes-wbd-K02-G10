package postgres

import (
	"context"
	"time"
	"github.com/jackc/pgx/v5"
	"dbplatform/internal/domain"
)

// membatasi pg_class dan pg_namespace ke objek pengguna
const userRel = `c.relkind IN ('r', 'p', 'v', 'm')
	AND n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
	AND n.nspname NOT LIKE 'pg\_temp\_%'
	AND n.nspname NOT LIKE 'pg\_toast\_temp\_%'`

const tablesSQL = `
SELECT n.nspname, c.relname, c.relkind::text, c.reltuples::bigint
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE ` + userRel + `
ORDER BY n.nspname, c.relname`

const columnsSQL = `
SELECT n.nspname, c.relname, a.attname, a.attnum::int,
       format_type(a.atttypid, a.atttypmod), NOT a.attnotnull,
       pg_get_expr(d.adbin, d.adrelid)
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE ` + userRel + `
ORDER BY n.nspname, c.relname, a.attnum`

const constraintsSQL = `
SELECT n.nspname, c.relname, con.conname, con.contype::text,
       ARRAY(SELECT a.attname::text
             FROM unnest(con.conkey) WITH ORDINALITY k(attnum, ord)
             JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum
             ORDER BY k.ord),
       COALESCE(fn.nspname, ''), COALESCE(fc.relname, ''),
       ARRAY(SELECT a.attname::text
             FROM unnest(con.confkey) WITH ORDINALITY k(attnum, ord)
             JOIN pg_attribute a ON a.attrelid = con.confrelid AND a.attnum = k.attnum
             ORDER BY k.ord),
       con.confupdtype::text, con.confdeltype::text,
       CASE WHEN con.contype = 'c' THEN pg_get_constraintdef(con.oid) ELSE '' END
FROM pg_constraint con
JOIN pg_class c ON c.oid = con.conrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_class fc ON fc.oid = con.confrelid
LEFT JOIN pg_namespace fn ON fn.oid = fc.relnamespace
WHERE con.contype IN ('p', 'f', 'u', 'c') AND ` + userRel + `
ORDER BY n.nspname, c.relname, con.conname`

const indexesSQL = `
SELECT n.nspname, c.relname, ic.relname, i.indisunique, i.indisprimary, am.amname,
       pg_get_indexdef(i.indexrelid),
       ARRAY(SELECT pg_get_indexdef(i.indexrelid, k, true)
             FROM generate_series(1, i.indnkeyatts::int) k ORDER BY k)
FROM pg_index i
JOIN pg_class c ON c.oid = i.indrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_class ic ON ic.oid = i.indexrelid
JOIN pg_am am ON am.oid = ic.relam
WHERE ` + userRel + `
ORDER BY n.nspname, c.relname, ic.relname`

type relKey struct{ schema, name string }

// baca schema database dan tabel/view, kolom, constraint, dan index
// hasil deterministik tabel menurut (schema, nama), kolom menurut posisi
func (d *Driver) Introspect(ctx context.Context) (*domain.Schema, error) {
	out := &domain.Schema{}
	spec := txSpec{readOnly: true, iso: pgx.RepeatableRead, timeout: 20 * time.Second}
	err := d.runTx(ctx, spec, func(tx pgx.Tx) error {
		pos := map[relKey]int{}
		rows, err := tx.Query(ctx, tablesSQL)
		if err != nil {
			return err
		}
		for rows.Next() {
			var t domain.Table
			var kind string
			if err := rows.Scan(&t.Schema, &t.Name, &kind, &t.EstimatedRows); err != nil {
				rows.Close()
				return err
			}
			t.Kind = domain.TableKindTable
			if kind == "v" || kind == "m" {
				t.Kind = domain.TableKindView
			}
			if kind == "v" || t.EstimatedRows < 0 {
				t.EstimatedRows = -1 // ga diketahui
			}
			pos[relKey{t.Schema, t.Name}] = len(out.Tables)
			out.Tables = append(out.Tables, t)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		rows, err = tx.Query(ctx, columnsSQL)
		if err != nil {
			return err
		}
		for rows.Next() {
			var s, tn string
			var c domain.Column
			if err := rows.Scan(&s, &tn, &c.Name, &c.Position, &c.DataType, &c.Nullable, &c.Default); err != nil {
				rows.Close()
				return err
			}
			if i, ok := pos[relKey{s, tn}]; ok {
				out.Tables[i].Columns = append(out.Tables[i].Columns, c)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		rows, err = tx.Query(ctx, constraintsSQL)
		if err != nil {
			return err
		}
		for rows.Next() {
			var s, tn, ctype, upd, del string
			var c domain.Constraint
			if err := rows.Scan(&s, &tn, &c.Name, &ctype, &c.Columns,
				&c.RefSchema, &c.RefTable, &c.RefColumns, &upd, &del, &c.CheckExpr); err != nil {
				rows.Close()
				return err
			}
			switch ctype {
			case "p":
				c.Type = domain.ConstraintPrimaryKey
			case "f":
				c.Type = domain.ConstraintForeignKey
				c.OnUpdate, c.OnDelete = fkAction(upd), fkAction(del)
			case "u":
				c.Type = domain.ConstraintUnique
			case "c":
				c.Type = domain.ConstraintCheck
			}
			if c.Type != domain.ConstraintForeignKey {
				c.RefSchema, c.RefTable, c.RefColumns = "", "", nil
			}
			if i, ok := pos[relKey{s, tn}]; ok {
				out.Tables[i].Constraints = append(out.Tables[i].Constraints, c)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		rows, err = tx.Query(ctx, indexesSQL)
		if err != nil {
			return err
		}
		for rows.Next() {
			var s, tn string
			var ix domain.Index
			if err := rows.Scan(&s, &tn, &ix.Name, &ix.Unique, &ix.Primary, &ix.Method,
				&ix.Definition, &ix.Columns); err != nil {
				rows.Close()
				return err
			}
			if i, ok := pos[relKey{s, tn}]; ok {
				out.Tables[i].Indexes = append(out.Tables[i].Indexes, ix)
			}
		}
		rows.Close()
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// mengubah kode aksi FK pg_constraint jd teks SQL
func fkAction(code string) string {
	switch code {
	case "a":
		return "NO ACTION"
	case "r":
		return "RESTRICT"
	case "c":
		return "CASCADE"
	case "n":
		return "SET NULL"
	case "d":
		return "SET DEFAULT"
	}
	return ""
}
