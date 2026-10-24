package schemadiff

import (
	"strings"

	"dbplatform/internal/domain"
)

func quote(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func qualified(schema, name string) string {
	return quote(schema) + "." + quote(name)
}

func quoteList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = quote(n)
	}
	return strings.Join(quoted, ", ")
}

func usableDefault(def *string) *string {
	if def == nil || strings.HasPrefix(strings.TrimSpace(*def), "nextval(") {
		return nil
	}
	return def
}

func columnDefinition(c domain.Column) string {
	var b strings.Builder
	b.WriteString(quote(c.Name))
	b.WriteString(" ")
	b.WriteString(c.DataType)
	if !c.Nullable {
		b.WriteString(" NOT NULL")
	}
	if def := usableDefault(c.Default); def != nil {
		b.WriteString(" DEFAULT ")
		b.WriteString(*def)
	}
	return b.String()
}

func createTableSQL(t domain.Table) string {
	lines := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		lines[i] = "  " + columnDefinition(c)
	}
	return "CREATE TABLE " + qualified(t.Schema, t.Name) + " (\n" + strings.Join(lines, ",\n") + "\n);"
}

func createSchemaSQL(schema string) string {
	return "CREATE SCHEMA IF NOT EXISTS " + quote(schema) + ";"
}

func addColumnSQL(t domain.Table, c domain.Column) string {
	return "ALTER TABLE " + qualified(t.Schema, t.Name) + " ADD COLUMN " + columnDefinition(c) + ";"
}

func alterTypeSQL(t domain.Table, c domain.Column) string {
	return "ALTER TABLE " + qualified(t.Schema, t.Name) + " ALTER COLUMN " + quote(c.Name) +
		" TYPE " + c.DataType + " USING " + quote(c.Name) + "::" + c.DataType + ";"
}

func alterNullSQL(t domain.Table, c domain.Column) string {
	action := "SET NOT NULL"
	if c.Nullable {
		action = "DROP NOT NULL"
	}
	return "ALTER TABLE " + qualified(t.Schema, t.Name) + " ALTER COLUMN " + quote(c.Name) + " " + action + ";"
}

func alterDefaultSQL(t domain.Table, c domain.Column) string {
	action := "DROP DEFAULT"
	if def := usableDefault(c.Default); def != nil {
		action = "SET DEFAULT " + *def
	}
	return "ALTER TABLE " + qualified(t.Schema, t.Name) + " ALTER COLUMN " + quote(c.Name) + " " + action + ";"
}

func dropColumnSQL(t domain.Table, c domain.Column) string {
	return "ALTER TABLE " + qualified(t.Schema, t.Name) + " DROP COLUMN " + quote(c.Name) + ";"
}

func dropTableSQL(t domain.Table) string {
	return "DROP TABLE " + qualified(t.Schema, t.Name) + " CASCADE;"
}

func dropConstraintSQL(t domain.Table, c domain.Constraint) string {
	return "ALTER TABLE " + qualified(t.Schema, t.Name) + " DROP CONSTRAINT " + quote(c.Name) + ";"
}

func constraintBody(c domain.Constraint) string {
	switch c.Type {
	case domain.ConstraintPrimaryKey:
		return "PRIMARY KEY (" + quoteList(c.Columns) + ")"
	case domain.ConstraintUnique:
		return "UNIQUE (" + quoteList(c.Columns) + ")"
	case domain.ConstraintCheck:
		expr := strings.TrimSpace(c.CheckExpr)
		if strings.HasPrefix(strings.ToUpper(expr), "CHECK") {
			return expr
		}
		return "CHECK (" + expr + ")"
	case domain.ConstraintForeignKey:
		body := "FOREIGN KEY (" + quoteList(c.Columns) + ") REFERENCES " + qualified(c.RefSchema, c.RefTable) +
			" (" + quoteList(c.RefColumns) + ")"
		if c.OnUpdate != "" {
			body += " ON UPDATE " + c.OnUpdate
		}
		if c.OnDelete != "" {
			body += " ON DELETE " + c.OnDelete
		}
		return body
	}
	return ""
}

func addConstraintSQL(t domain.Table, c domain.Constraint) string {
	return "ALTER TABLE " + qualified(t.Schema, t.Name) + " ADD CONSTRAINT " + quote(c.Name) + " " + constraintBody(c) + ";"
}

func dropIndexSQL(t domain.Table, ix domain.Index) string {
	return "DROP INDEX " + qualified(t.Schema, ix.Name) + ";"
}

func createIndexSQL(ix domain.Index) string {
	return strings.TrimRight(strings.TrimSpace(ix.Definition), ";") + ";"
}
