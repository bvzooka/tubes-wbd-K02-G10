package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"dbplatform/internal/domain"
)

// membaca satu baris per kolom index. Kolom EXPRESSION hanya ada di server tertentu, jadi diisi lewat %s
const indexesSQL = `
SELECT TABLE_NAME, INDEX_NAME, NON_UNIQUE, SEQ_IN_INDEX, COLUMN_NAME, SUB_PART, COLLATION, INDEX_TYPE, %s
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = ?`

// nama index primary key di MySQL, dan nilai COLLATION untuk urutan menurun
const (
	primaryIndexName = "PRIMARY"
	collationDesc    = "D"
)

// menunjuk satu index lewat nama tabel dan nama index-nya
type indexKey struct{ table, name string }

// mengumpulkan bagian-bagian satu index selagi baris STATISTICS dibaca
type indexAcc struct {
	unique bool
	method string
	parts  []indexPart
}

// satu kolom atau ekspresi dalam sebuah index, seq menunjukkan urutannya
type indexPart struct {
	seq    int
	text   string
	quoted string
}

// menggabungkan baris per kolom dari STATISTICS menjadi satu index utuh per tabel
func readIndexes(ctx context.Context, tx *sql.Tx, schema string, info serverInfo, out *domain.Schema, pos map[string]int) error {
	got, err := readIndexParts(ctx, tx, schema, info)
	if err != nil {
		return fmt.Errorf("read index parts: %w", err)
	}
	for k, acc := range got {
		i, ok := pos[k.table]
		if !ok {
			continue
		}
		out.Tables[i].Indexes = append(out.Tables[i].Indexes, buildIndex(schema, k, acc))
	}
	return nil
}

// membaca STATISTICS dan mengelompokkan barisnya per index
func readIndexParts(ctx context.Context, tx *sql.Tx, schema string, info serverInfo) (map[indexKey]*indexAcc, error) {
	expr := "NULL"
	if info.hasExpression {
		expr = "EXPRESSION"
	}
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(indexesSQL, expr), schema)
	if err != nil {
		return nil, err
	}
	got := map[indexKey]*indexAcc{}
	for rows.Next() {
		var k indexKey
		var nonUnique, seq int
		var col, collation, expression sql.NullString
		var sub sql.NullInt64
		var itype string
		if err := rows.Scan(&k.table, &k.name, &nonUnique, &seq, &col, &sub, &collation, &itype, &expression); err != nil {
			rows.Close()
			return nil, err
		}
		acc := got[k]
		if acc == nil {
			acc = &indexAcc{unique: nonUnique == 0, method: strings.ToLower(itype)}
			got[k] = acc
		}
		acc.parts = append(acc.parts, newIndexPart(seq, col, sub, collation, expression))
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	return got, nil
}

// menyusun satu domain.Index dari bagian-bagiannya, diurutkan menurut posisi di index
func buildIndex(schema string, k indexKey, acc *indexAcc) domain.Index {
	sort.SliceStable(acc.parts, func(a, b int) bool { return acc.parts[a].seq < acc.parts[b].seq })
	in := domain.Index{
		Name:    k.name,
		Unique:  acc.unique,
		Primary: k.name == primaryIndexName,
		Method:  acc.method,
	}
	quoted := make([]string, len(acc.parts))
	for j, p := range acc.parts {
		in.Columns = append(in.Columns, p.text)
		quoted[j] = p.quoted
	}
	in.Definition = indexDefinition(schema, k.table, in, quoted)
	return in
}

// menyusun satu bagian index: nama kolom atau ekspresi, panjang prefix, dan arah urutnya
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
	if collation.Valid && collation.String == collationDesc {
		p.quoted += " DESC"
	}
	return p
}

// menyusun pernyataan pembuat index, primary key diwakili ALTER TABLE karena tidak bisa lewat CREATE INDEX
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
