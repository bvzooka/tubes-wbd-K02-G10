package mysql

import (
	"context"
	"database/sql"
	"dbplatform/internal/domain"
	"fmt"
	"sort"
	"strings"
	"time"
)

// query berikut membaca information_schema MySQL: tabel, kolom, constraint, key, aturan foreign key, check, dan index
const tablesSQL = `
SELECT TABLE_NAME, TABLE_TYPE, TABLE_ROWS
FROM information_schema.TABLES
WHERE TABLE_SCHEMA = ? AND TABLE_TYPE IN ('BASE TABLE', 'VIEW', 'SYSTEM VERSIONED')`

const columnsSQL = `
SELECT TABLE_NAME, COLUMN_NAME, ORDINAL_POSITION, COLUMN_TYPE, DATA_TYPE, IS_NULLABLE, COLUMN_DEFAULT, EXTRA
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = ?`

// dua query berikut mengecek fitur yang tidak dimiliki semua versi server
const hasChecksTableSQL = `SELECT COUNT(*) FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = 'information_schema' AND TABLE_NAME = 'CHECK_CONSTRAINTS'`

const hasExpressionColumnSQL = `SELECT COUNT(*) FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = 'information_schema' AND TABLE_NAME = 'STATISTICS' AND COLUMN_NAME = 'EXPRESSION'`

// introspectTimeout membatasi waktu seluruh pembacaan information_schema
const introspectTimeout = 20 * time.Second

// tableTypeView adalah nilai TABLE_TYPE untuk view
const tableTypeView = "VIEW"

// serverInfo mencatat jenis server dan fitur yang tersedia, karena MySQL dan MariaDB berbeda versi dan kolom
type serverInfo struct {
	mariadb       bool
	hasChecks     bool
	hasExpression bool
}

// Introspect membaca schema database: tabel dan view, lalu kolom, constraint, dan index tiap tabel.
// Hasilnya diurutkan supaya sama persis di tiap pembacaan.
func (d *Driver) Introspect(ctx context.Context) (*domain.Schema, error) {
	out := &domain.Schema{}
	spec := txSpec{readOnly: true, iso: sql.LevelRepeatableRead, timeout: introspectTimeout}
	err := d.runTx(ctx, d.ro, spec, func(ctx context.Context, tx *sql.Tx) error {
		info, err := detectServer(ctx, tx)
		if err != nil {
			return fmt.Errorf("detect server: %w", err)
		}
		// di MySQL, schema sama dengan nama database
		schema := d.dbName

		// pos mencatat urutan tiap tabel di out.Tables, bacaan berikutnya memakainya untuk menempelkan hasil
		pos, err := readTables(ctx, tx, schema, out)
		if err != nil {
			return fmt.Errorf("read tables: %w", err)
		}
		if err := readColumns(ctx, tx, schema, info, out, pos); err != nil {
			return fmt.Errorf("read columns: %w", err)
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

// closeRows menutup rows dan mengembalikan error dari iterasi kalau ada, kalau tidak error dari penutupan
func closeRows(rows *sql.Rows) error {
	cerr := rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	return cerr
}

// detectServer mengecek jenis server dan apakah tabel CHECK_CONSTRAINTS dan kolom EXPRESSION ada, tidak semua versi punya
func detectServer(ctx context.Context, tx *sql.Tx) (serverInfo, error) {
	var info serverInfo
	var version string
	if err := tx.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		return info, err
	}
	info.mariadb = isMariaDB(version)

	var n int
	if err := tx.QueryRowContext(ctx, hasChecksTableSQL).Scan(&n); err != nil {
		return info, err
	}
	info.hasChecks = n > 0

	if err := tx.QueryRowContext(ctx, hasExpressionColumnSQL).Scan(&n); err != nil {
		return info, err
	}
	info.hasExpression = n > 0
	return info, nil
}

// isMariaDB mengenali MariaDB dari string VERSION(), misalnya "10.11.6-MariaDB"
func isMariaDB(version string) bool {
	return strings.Contains(strings.ToLower(version), "mariadb")
}

// readTables mengisi out.Tables dan mengembalikan peta posisi tiap tabel di dalamnya
func readTables(ctx context.Context, tx *sql.Tx, schema string, out *domain.Schema) (map[string]int, error) {
	rows, err := tx.QueryContext(ctx, tablesSQL, schema)
	if err != nil {
		return nil, err
	}
	pos := map[string]int{}
	for rows.Next() {
		var name, ttype string
		var est sql.NullInt64
		if err := rows.Scan(&name, &ttype, &est); err != nil {
			rows.Close()
			return nil, err
		}
		t := domain.Table{Schema: schema, Name: name, Kind: domain.TableKindTable, EstimatedRows: -1}
		if ttype == tableTypeView {
			t.Kind = domain.TableKindView
		} else if est.Valid && est.Int64 >= 0 {
			t.EstimatedRows = est.Int64
		}
		pos[name] = len(out.Tables)
		out.Tables = append(out.Tables, t)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	return pos, nil
}

// readColumns menempelkan kolom ke tabelnya
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

// columnDefault menyamakan format default kolom: MariaDB sudah memberi tanda kutip sendiri, MySQL harus ditambahkan
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

// isNowDefault true untuk default CURRENT_TIMESTAMP yang tidak boleh diberi kutip
func isNowDefault(v, dataType string) bool {
	switch strings.ToLower(dataType) {
	case "timestamp", "datetime":
		return strings.HasPrefix(strings.ToUpper(v), "CURRENT_TIMESTAMP")
	}
	return false
}

// isQuotedType true untuk tipe yang nilai defaultnya ditulis dengan kutip
func isQuotedType(dataType string) bool {
	switch strings.ToLower(dataType) {
	case "char", "varchar", "tinytext", "text", "mediumtext", "longtext",
		"enum", "set", "date", "datetime", "timestamp", "time", "year", "json",
		"binary", "varbinary", "tinyblob", "blob", "mediumblob", "longblob":
		return true
	}
	return false
}

// sortSchema mengurutkan hasil karena map di Go tidak berurutan, jadi output sama persis di tiap pembacaan
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
