package mysql

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"dbplatform/internal/domain"
)

func seededTable(t *testing.T, d *Driver, n int) string {
	t.Helper()
	name := scratchTable(t, d, "CREATE TABLE %s (id INT PRIMARY KEY, label VARCHAR(40) NOT NULL) ENGINE=InnoDB")
	db := rawDB(t, d)
	var values []string
	for i := n; i >= 1; i-- {
		values = append(values, fmt.Sprintf("(%d, 'row %d')", i, i))
	}
	if _, err := db.Exec("INSERT INTO " + quoteIdent(name) + " VALUES " + strings.Join(values, ",")); err != nil {
		t.Fatalf("isi tabel: %v", err)
	}
	return name
}

func ids(td *domain.TableData) []string {
	var out []string
	for _, r := range td.Rows {
		out = append(out, r[0].Value)
	}
	return out
}

func TestTableData_Integration_PaginationAndHasNext(t *testing.T) {
	d := newTestDriver(t)
	tbl := seededTable(t, d, 130)
	ref := domain.TableRef{Schema: testDB, Table: tbl}
	ctx := context.Background()

	p1, err := d.TableData(ctx, ref, domain.PageRequest{Page: 1, PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(p1.Rows) != 50 || !p1.HasNext || p1.Page != 1 || p1.PageSize != 50 {
		t.Fatalf("halaman 1: rows=%d next=%v page=%d size=%d", len(p1.Rows), p1.HasNext, p1.Page, p1.PageSize)
	}
	if got := ids(p1); got[0] != "1" || got[49] != "50" {
		t.Errorf("halaman 1 harus berurutan menurut PK, dapat %s..%s", got[0], got[49])
	}
	if strings.Join(p1.Columns, ",") != "id,label" {
		t.Errorf("kolom = %v", p1.Columns)
	}

	p3, err := d.TableData(ctx, ref, domain.PageRequest{Page: 3, PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(p3.Rows) != 30 || p3.HasNext {
		t.Fatalf("halaman 3: rows=%d next=%v", len(p3.Rows), p3.HasNext)
	}
	if got := ids(p3); got[0] != "101" || got[29] != "130" {
		t.Errorf("halaman 3 = %s..%s", got[0], got[29])
	}

	exact, err := d.TableData(ctx, ref, domain.PageRequest{Page: 1, PageSize: 130})
	if err != nil || len(exact.Rows) != 130 || exact.HasNext {
		t.Fatalf("batas tepat: err=%v rows=%d next=%v", err, len(exact.Rows), exact.HasNext)
	}
	almost, err := d.TableData(ctx, ref, domain.PageRequest{Page: 1, PageSize: 129})
	if err != nil || len(almost.Rows) != 129 || !almost.HasNext {
		t.Fatalf("kurang satu: err=%v rows=%d next=%v", err, len(almost.Rows), almost.HasNext)
	}

	beyond, err := d.TableData(ctx, ref, domain.PageRequest{Page: 99, PageSize: 50})
	if err != nil || len(beyond.Rows) != 0 || beyond.HasNext {
		t.Fatalf("halaman di luar rentang: err=%v rows=%d next=%v", err, len(beyond.Rows), beyond.HasNext)
	}

	big, err := d.TableData(ctx, ref, domain.PageRequest{Page: 1, PageSize: 99999})
	if err != nil || big.PageSize != domain.MaxPageSize || len(big.Rows) != 130 {
		t.Fatalf("size besar: err=%v size=%d rows=%d", err, big.PageSize, len(big.Rows))
	}
	def, err := d.TableData(ctx, ref, domain.PageRequest{Page: -5})
	if err != nil || def.Page != 1 || def.PageSize != domain.DefaultPageSize || len(def.Rows) != domain.DefaultPageSize {
		t.Fatalf("normalisasi: err=%v page=%d size=%d rows=%d", err, def.Page, def.PageSize, len(def.Rows))
	}
}

func TestTableData_Integration_CompositePrimaryKeyOrder(t *testing.T) {
	d := newTestDriver(t)
	tbl := scratchTable(t, d, "CREATE TABLE %s (a INT NOT NULL, b INT NOT NULL, v INT, PRIMARY KEY (b, a)) ENGINE=InnoDB")
	if _, err := rawDB(t, d).Exec("INSERT INTO " + quoteIdent(tbl) + " VALUES (1,2,0),(2,1,0),(1,1,0),(2,2,0)"); err != nil {
		t.Fatal(err)
	}
	td, err := d.TableData(context.Background(), domain.TableRef{Schema: testDB, Table: tbl}, domain.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range td.Rows {
		got = append(got, r[0].Value+r[1].Value)
	}

	if want := "11,21,12,22"; strings.Join(got, ",") != want {
		t.Errorf("urutan = %v, want %s", got, want)
	}
}

func TestTableData_Integration_ValuesAsText(t *testing.T) {
	d := newTestDriver(t)
	tbl := scratchTable(t, d, `CREATE TABLE %s (
		id INT PRIMARY KEY, txt TEXT, blank VARCHAR(10), num DECIMAL(10,2), flag TINYINT(1),
		at DATETIME, bin BLOB, big BIGINT UNSIGNED, js JSON) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`)
	_, err := rawDB(t, d).Exec("INSERT INTO " + quoteIdent(tbl) + ` VALUES
		(1, 'baris "satu"', '', 3.50, 1, '2025-01-02 03:04:05', UNHEX('00FF10'), 18446744073709551615, '{"a": [1, 2]}'),
		(2, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL),
		(3, CONCAT('baris', CHAR(10), 'dua ✓ 😀'), 'x', -0.01, 0, '1999-12-31 23:59:59', '', 0, 'null')`)
	if err != nil {
		t.Fatalf("isi tabel: %v", err)
	}
	td, err := d.TableData(context.Background(), domain.TableRef{Schema: testDB, Table: tbl}, domain.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(td.Rows) != 3 {
		t.Fatalf("rows = %d", len(td.Rows))
	}
	v := func(s string) domain.Cell { return domain.Cell{Value: s} }

	want1 := []domain.Cell{v("1"), v(`baris "satu"`), v(""), v("3.50"), v("1"), v("2025-01-02 03:04:05"), v("0x00ff10"), v("18446744073709551615"), v(`{"a": [1, 2]}`)}
	for i, w := range want1 {
		if td.Rows[0][i] != w {
			t.Errorf("baris 1 kolom %s = %+v, want %+v", td.Columns[i], td.Rows[0][i], w)
		}
	}
	for i := 1; i < len(td.Columns); i++ {
		if !td.Rows[1][i].Null {
			t.Errorf("baris 2 kolom %s harus NULL, dapat %+v", td.Columns[i], td.Rows[1][i])
		}
	}
	want3 := []domain.Cell{v("3"), v("baris\ndua ✓ 😀"), v("x"), v("-0.01"), v("0"), v("1999-12-31 23:59:59"), v("0x"), v("0"), v("null")}
	for i, w := range want3 {
		if td.Rows[2][i] != w {
			t.Errorf("baris 3 kolom %s = %+v, want %+v", td.Columns[i], td.Rows[2][i], w)
		}
	}
}

func TestTableData_Integration_ViewWithoutPrimaryKey(t *testing.T) {
	d := newTestDriver(t)
	f := newFixture(t, d)
	if _, err := rawDB(t, d).Exec("INSERT INTO " + quoteIdent(f.cust) + " (full_name, email) VALUES ('A', 'a@x'), ('B', 'b@x')"); err != nil {
		t.Fatal(err)
	}
	td, err := d.TableData(context.Background(), domain.TableRef{Schema: testDB, Table: f.view}, domain.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(td.Rows) != 2 || strings.Join(td.Columns, ",") != "id,full_name" {
		t.Errorf("view: cols=%v rows=%d", td.Columns, len(td.Rows))
	}
}

func TestTableData_Integration_NotFound(t *testing.T) {
	d := newTestDriver(t)
	tbl := seededTable(t, d, 3)
	ctx := context.Background()

	for name, ref := range map[string]domain.TableRef{
		"tabel tidak ada":           {Schema: testDB, Table: "tidak_ada_" + tbl},
		"schema lain":               {Schema: "mysql", Table: "user"},
		"schema information_schema": {Schema: "information_schema", Table: "TABLES"},
		"schema tidak ada":          {Schema: "tidak_ada", Table: tbl},
		"beda huruf besar":          {Schema: testDB, Table: strings.ToUpper(tbl)},
		"nama kosong":               {Schema: testDB, Table: ""},
	} {
		_, err := d.TableData(ctx, ref, domain.PageRequest{})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
}

func TestTableData_Integration_InjectionNamesExecuteNothing(t *testing.T) {
	d := newTestDriver(t)
	canary := seededTable(t, d, 5)
	ctx := context.Background()

	evil := []string{
		"x`; DROP TABLE " + canary + "; --",
		canary + "`; DROP TABLE " + canary + "; --",
		"\"; DROP TABLE " + canary + ";--",
		"' OR '1'='1",
		canary + "' OR '1'='1",
		canary + "\\",
	}
	for _, name := range evil {
		_, err := d.TableData(ctx, domain.TableRef{Schema: testDB, Table: name}, domain.PageRequest{})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%q: err = %v, want ErrNotFound", name, err)
		}
	}
	_, err := d.TableData(ctx, domain.TableRef{Schema: "x`; DROP TABLE " + canary + ";--", Table: canary}, domain.PageRequest{})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("schema berbahaya: err = %v", err)
	}
	if n := countRows(t, d, canary); n != 5 {
		t.Fatalf("tabel kanari berubah: %d baris", n)
	}
}

func TestTableData_Integration_AwkwardButValidTableName(t *testing.T) {
	d := newTestDriver(t)
	name := uniqueName("we`ird name")
	db := rawDB(t, d)
	if _, err := db.Exec("CREATE TABLE " + quoteIdent(name) + " (`Id` INT PRIMARY KEY, `Some Value` TEXT)"); err != nil {
		t.Fatalf("buat tabel: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec("DROP TABLE IF EXISTS " + quoteIdent(name)) })
	if _, err := db.Exec("INSERT INTO " + quoteIdent(name) + " VALUES (1, 'ok')"); err != nil {
		t.Fatal(err)
	}
	td, err := d.TableData(context.Background(), domain.TableRef{Schema: testDB, Table: name}, domain.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(td.Rows) != 1 || td.Rows[0][1].Value != "ok" || strings.Join(td.Columns, "|") != "Id|Some Value" {
		t.Errorf("hasil = %+v", td)
	}
}

func TestTableData_Integration_DoesNotModifyAndReleasesConnection(t *testing.T) {
	d := newTestDriver(t)
	tbl := seededTable(t, d, 20)
	for i := 0; i < 5; i++ {
		if _, err := d.TableData(context.Background(), domain.TableRef{Schema: testDB, Table: tbl}, domain.PageRequest{Page: 1, PageSize: 7}); err != nil {
			t.Fatal(err)
		}
	}
	if n := countRows(t, d, tbl); n != 20 {
		t.Errorf("baris berubah: %d", n)
	}
	if n := d.ro.Stats().InUse; n != 0 {
		t.Errorf("koneksi masih terpakai: %d", n)
	}
	if d.rw != nil {
		t.Error("jalur baca tidak boleh membuat pool tulis (multiStatements)")
	}
}
