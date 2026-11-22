// Package schemadiff membandingkan schema dua database PostgreSQL dan menyusun SQL untuk menyamakan tujuan dengan sumber
package schemadiff

import (
	"strings"

	"dbplatform/internal/domain"
)

// Kutip double biar nama aneh aman dan ga bisa nyisipin SQL
func quote(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// qualified menulis nama lengkap schema.tabel dengan kutip
func qualified(schema, name string) string {
	return quote(schema) + "." + quote(name)
}

// quoteList mengutip tiap nama lalu menggabungkannya dengan koma
func quoteList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = quote(n)
	}
	return strings.Join(quoted, ", ")
}

// usableDefault membuang default dari sequence (nextval) karena sequence di database lain tidak ikut tersalin
func usableDefault(def *string) *string {
	if def == nil || strings.HasPrefix(strings.TrimSpace(*def), "nextval(") {
		return nil
	}
	return def
}

// columnDefinition menulis definisi satu kolom: nama, tipe, NOT NULL, dan default
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

// createTableSQL membuat tabel dengan semua kolomnya, constraint ditambahkan terpisah belakangan
func createTableSQL(t domain.Table) string {
	lines := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		lines[i] = "  " + columnDefinition(c)
	}
	return "CREATE TABLE " + qualified(t.Schema, t.Name) + " (\n" + strings.Join(lines, ",\n") + "\n);"
}

// createSchemaSQL memakai IF NOT EXISTS supaya aman dijalankan ulang
func createSchemaSQL(schema string) string {
	return "CREATE SCHEMA IF NOT EXISTS " + quote(schema) + ";"
}

func addColumnSQL(t domain.Table, c domain.Column) string {
	return "ALTER TABLE " + qualified(t.Schema, t.Name) + " ADD COLUMN " + columnDefinition(c) + ";"
}

// Pakai USING buat konversi data, bisa gagal kalau data ga cocok
func alterTypeSQL(t domain.Table, c domain.Column) string {
	return "ALTER TABLE " + qualified(t.Schema, t.Name) + " ALTER COLUMN " + quote(c.Name) +
		" TYPE " + c.DataType + " USING " + quote(c.Name) + "::" + c.DataType + ";"
}

// alterNullSQL mengikuti keadaan sumber: kolom nullable berarti DROP NOT NULL, sebaliknya SET NOT NULL
func alterNullSQL(t domain.Table, c domain.Column) string {
	action := "SET NOT NULL"
	if c.Nullable {
		action = "DROP NOT NULL"
	}
	return "ALTER TABLE " + qualified(t.Schema, t.Name) + " ALTER COLUMN " + quote(c.Name) + " " + action + ";"
}

// alterDefaultSQL mengikuti default sumber, tanpa default berarti DROP DEFAULT
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

// dropTableSQL memakai CASCADE supaya objek yang bergantung ikut terhapus, makanya perubahan ini destruktif
func dropTableSQL(t domain.Table) string {
	return "DROP TABLE " + qualified(t.Schema, t.Name) + " CASCADE;"
}

func dropConstraintSQL(t domain.Table, c domain.Constraint) string {
	return "ALTER TABLE " + qualified(t.Schema, t.Name) + " DROP CONSTRAINT " + quote(c.Name) + ";"
}

// constraintBody menulis isi constraint sesuai jenisnya, foreign key lengkap dengan aturan ON UPDATE dan ON DELETE
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

// createIndexSQL memakai definisi index apa adanya dari database sumber
func createIndexSQL(ix domain.Index) string {
	return strings.TrimRight(strings.TrimSpace(ix.Definition), ";") + ";"
}
