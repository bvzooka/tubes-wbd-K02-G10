package mysql

import (
	"context"
	"database/sql"
	"dbplatform/internal/domain"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type fixture struct {
	cust, ord, item, view string
	caps serverInfo
}

func newFixture(t *testing.T, d *Driver) fixture {
	t.Helper()
	p := uniqueName("fx")
	f := fixture{cust: p + "_cust", ord: p + "_ord", item: p + "_item", view: p + "_v", caps: serverCaps(t, d)}
	lowerIx := ""
	if f.caps.hasExpression {
		lowerIx = "KEY " + quoteIdent(p+"_lower_ix") + " ((LOWER(full_name))),"
	}
	db := rawDB(t, d)
	q := quoteIdent
	stmts := []string{
		"CREATE TABLE " + q(f.cust) + ` (
			id INT NOT NULL AUTO_INCREMENT,
			full_name VARCHAR(100) NOT NULL,
			email VARCHAR(190) NOT NULL,
			balance DECIMAL(12,2) NOT NULL DEFAULT 0,
			status VARCHAR(10) NOT NULL DEFAULT 'new',
			note TEXT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			CONSTRAINT ` + q(p+"_email_key") + ` UNIQUE (email),
			CONSTRAINT ` + q(p+"_balance_chk") + ` CHECK (balance >= 0),
			KEY ` + q(p+"_name_ix") + ` (full_name(20)),
			` + lowerIx + `
			FULLTEXT KEY ` + q(p+"_note_ft") + ` (note)
		) ENGINE=InnoDB`,
		"CREATE TABLE " + q(f.ord) + ` (
			id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
			customer_id INT NOT NULL,
			ordered_at DATETIME NOT NULL,
			KEY ` + q(p+"_cust_ix") + ` (customer_id, ordered_at DESC),
			CONSTRAINT ` + q(p+"_cust_fk") + ` FOREIGN KEY (customer_id) REFERENCES ` + q(f.cust) + ` (id)
				ON UPDATE CASCADE ON DELETE RESTRICT
		) ENGINE=InnoDB`,
		"CREATE TABLE " + q(f.item) + ` (
			order_id BIGINT NOT NULL,
			sku VARCHAR(20) NOT NULL,
			qty INT NOT NULL DEFAULT 1,
			PRIMARY KEY (order_id, sku),
			CONSTRAINT ` + q(p+"_ord_fk") + ` FOREIGN KEY (order_id) REFERENCES ` + q(f.ord) + ` (id) ON DELETE CASCADE
		) ENGINE=InnoDB`,
		"CREATE VIEW " + q(f.view) + " AS SELECT id, full_name FROM " + q(f.cust),
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DROP VIEW IF EXISTS " + q(f.view))
		_, _ = db.Exec("DROP TABLE IF EXISTS " + q(f.item))
		_, _ = db.Exec("DROP TABLE IF EXISTS " + q(f.ord))
		_, _ = db.Exec("DROP TABLE IF EXISTS " + q(f.cust))
	})
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("buat fixture: %v\n%s", err, s)
		}
	}
	return f
}

func normType(s string) string {
	return intWidth.ReplaceAllString(s, "$1")
}

var intWidth = regexp.MustCompile(`^((?:tiny|small|medium|big)?int)\(\d+\)`)

func findTable(t *testing.T, s *domain.Schema, name string) domain.Table {
	t.Helper()
	for _, tb := range s.Tables {
		if tb.Name == name {
			return tb
		}
	}
	t.Fatalf("tabel %q tidak ada di hasil introspeksi", name)
	return domain.Table{}
}

func findConstraint(t *testing.T, tb domain.Table, name string) domain.Constraint {
	t.Helper()
	for _, c := range tb.Constraints {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("constraint %q tidak ada di %s: %+v", name, tb.Name, tb.Constraints)
	return domain.Constraint{}
}

func findIndex(t *testing.T, tb domain.Table, name string) domain.Index {
	t.Helper()
	for _, ix := range tb.Indexes {
		if ix.Name == name {
			return ix
		}
	}
	t.Fatalf("index %q tidak ada di %s: %+v", name, tb.Name, tb.Indexes)
	return domain.Index{}
}

func TestIntrospect_Integration_TablesColumnsAndKinds(t *testing.T) {
	d := newTestDriver(t)
	f := newFixture(t, d)
	s, err := d.Introspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	for _, tb := range s.Tables {
		if tb.Schema != testDB {
			t.Errorf("hanya database terpilih yang boleh muncul, dapat %q.%q", tb.Schema, tb.Name)
		}
	}

	cust := findTable(t, s, f.cust)
	if cust.Kind != domain.TableKindTable || cust.Schema != testDB {
		t.Errorf("cust = %+v", cust)
	}
	v := findTable(t, s, f.view)
	if v.Kind != domain.TableKindView || v.EstimatedRows != -1 {
		t.Errorf("view: kind=%v rows=%d, want view dan -1", v.Kind, v.EstimatedRows)
	}
	if len(v.Columns) != 2 || len(v.Constraints) != 0 {
		t.Errorf("view: %+v", v)
	}

	type col struct {
		name, typ string
		nullable  bool
		def       string
	}
	want := []col{
		{"id", "int", false, "AUTO_INCREMENT"},
		{"full_name", "varchar(100)", false, "<nil>"},
		{"email", "varchar(190)", false, "<nil>"},
		{"balance", "decimal(12,2)", false, "0.00"},
		{"status", "varchar(10)", false, "'new'"},
		{"note", "text", true, "<nil>"},
		{"created_at", "timestamp", false, "CURRENT_TIMESTAMP"},
	}
	if len(cust.Columns) != len(want) {
		t.Fatalf("jumlah kolom = %d, want %d: %+v", len(cust.Columns), len(want), cust.Columns)
	}
	for i, w := range want {
		c := cust.Columns[i]
		def := "<nil>"
		if c.Default != nil {
			def = *c.Default
		}
		if def != "<nil>" {
			def = strings.ToUpper(strings.TrimSuffix(def, "()"))
			w.def = strings.ToUpper(w.def)
		}
		if c.Name != w.name || normType(c.DataType) != w.typ || c.Nullable != w.nullable || def != w.def || c.Position != i+1 {
			t.Errorf("kolom %d = {%s %s null=%v def=%s pos=%d}, want %+v", i, c.Name, c.DataType, c.Nullable, def, c.Position, w)
		}
	}
}

func TestIntrospect_Integration_Constraints(t *testing.T) {
	d := newTestDriver(t)
	f := newFixture(t, d)
	s, err := d.Introspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p := strings.TrimSuffix(f.cust, "_cust")

	cust := findTable(t, s, f.cust)
	pk := findConstraint(t, cust, "PRIMARY")
	if pk.Type != domain.ConstraintPrimaryKey || !reflect.DeepEqual(pk.Columns, []string{"id"}) {
		t.Errorf("pk = %+v", pk)
	}
	uq := findConstraint(t, cust, p+"_email_key")
	if uq.Type != domain.ConstraintUnique || !reflect.DeepEqual(uq.Columns, []string{"email"}) {
		t.Errorf("unique = %+v", uq)
	}
	if f.caps.hasChecks {
		ck := findConstraint(t, cust, p+"_balance_chk")
		if ck.Type != domain.ConstraintCheck || !strings.HasPrefix(ck.CheckExpr, "CHECK (") || !strings.Contains(ck.CheckExpr, "balance") {
			t.Errorf("check = %+v", ck)
		}
		if ck.RefTable != "" || ck.RefColumns != nil || ck.OnDelete != "" {
			t.Errorf("check tidak boleh memuat data FK: %+v", ck)
		}
	} else {
		for _, c := range cust.Constraints {
			if c.Type == domain.ConstraintCheck {
				t.Errorf("server tanpa CHECK_CONSTRAINTS tidak boleh melaporkan check: %+v", c)
			}
		}
	}

	ord := findTable(t, s, f.ord)
	fk := findConstraint(t, ord, p+"_cust_fk")
	if fk.Type != domain.ConstraintForeignKey || !reflect.DeepEqual(fk.Columns, []string{"customer_id"}) ||
		fk.RefSchema != testDB || fk.RefTable != f.cust || !reflect.DeepEqual(fk.RefColumns, []string{"id"}) ||
		fk.OnUpdate != "CASCADE" || fk.OnDelete != "RESTRICT" {
		t.Errorf("fk = %+v", fk)
	}

	item := findTable(t, s, f.item)
	cpk := findConstraint(t, item, "PRIMARY")
	if !reflect.DeepEqual(cpk.Columns, []string{"order_id", "sku"}) {
		t.Errorf("PK komposit harus berurutan, dapat %v", cpk.Columns)
	}
	ifk := findConstraint(t, item, p+"_ord_fk")
	if ifk.OnDelete != "CASCADE" || (ifk.OnUpdate != "NO ACTION" && ifk.OnUpdate != "RESTRICT") {
		t.Errorf("aksi FK = update %q delete %q", ifk.OnUpdate, ifk.OnDelete)
	}

	if !sort.SliceIsSorted(cust.Constraints, func(i, j int) bool { return cust.Constraints[i].Name < cust.Constraints[j].Name }) {
		t.Errorf("constraint tidak terurut: %+v", cust.Constraints)
	}
}

func TestIntrospect_Integration_Indexes(t *testing.T) {
	d := newTestDriver(t)
	f := newFixture(t, d)
	s, err := d.Introspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p := strings.TrimSuffix(f.cust, "_cust")
	cust := findTable(t, s, f.cust)

	pk := findIndex(t, cust, "PRIMARY")
	if !pk.Primary || !pk.Unique || pk.Method != "btree" || !strings.Contains(pk.Definition, "ADD PRIMARY KEY (`id`)") {
		t.Errorf("pk = %+v", pk)
	}
	uq := findIndex(t, cust, p+"_email_key")
	if !uq.Unique || uq.Primary || !strings.HasPrefix(uq.Definition, "CREATE UNIQUE INDEX ") {
		t.Errorf("unique = %+v", uq)
	}
	pre := findIndex(t, cust, p+"_name_ix")
	if pre.Unique || !strings.Contains(pre.Definition, "`full_name`(20)") {
		t.Errorf("prefix = %+v", pre)
	}
	if f.caps.hasExpression {
		fn := findIndex(t, cust, p+"_lower_ix")
		if len(fn.Columns) != 1 || !strings.Contains(strings.ToLower(fn.Columns[0]), "lower") || !strings.Contains(strings.ToLower(fn.Definition), "lower") {
			t.Errorf("fungsional = %+v", fn)
		}
	}
	ft := findIndex(t, cust, p+"_note_ft")
	if ft.Method != "fulltext" || !strings.HasPrefix(ft.Definition, "CREATE FULLTEXT INDEX ") {
		t.Errorf("fulltext = %+v", ft)
	}

	ord := findTable(t, s, f.ord)
	cix := findIndex(t, ord, p+"_cust_ix")
	if !reflect.DeepEqual(cix.Columns, []string{"customer_id", "ordered_at"}) {
		t.Errorf("index komposit = %+v", cix)
	}
	if (f.caps.hasExpression || f.caps.mariadb) && !strings.Contains(cix.Definition, "`ordered_at` DESC") {
		t.Errorf("urutan DESC hilang: %+v", cix)
	}

	if !sort.SliceIsSorted(cust.Indexes, func(i, j int) bool { return cust.Indexes[i].Name < cust.Indexes[j].Name }) {
		t.Errorf("index tidak terurut: %+v", cust.Indexes)
	}
}

func TestIntrospect_Integration_DeterministicAndSorted(t *testing.T) {
	d := newTestDriver(t)
	_ = newFixture(t, d)
	a, err := d.Introspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := d.Introspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("dua kali introspeksi menghasilkan hasil berbeda")
	}
	if !sort.SliceIsSorted(a.Tables, func(i, j int) bool {
		if a.Tables[i].Schema != a.Tables[j].Schema {
			return a.Tables[i].Schema < a.Tables[j].Schema
		}
		return a.Tables[i].Name < a.Tables[j].Name
	}) {
		t.Error("tabel tidak terurut (schema, nama)")
	}
	for _, tb := range a.Tables {
		for i := 1; i < len(tb.Columns); i++ {
			if tb.Columns[i].Position <= tb.Columns[i-1].Position {
				t.Errorf("kolom %s.%s tidak terurut menurut posisi", tb.Name, tb.Columns[i].Name)
			}
		}
	}
}

func TestIntrospect_Integration_ServerWithoutOptionalCatalogs(t *testing.T) {
	d := newTestDriver(t)
	f := newFixture(t, d)
	ctx := context.Background()

	err := d.runTx(ctx, d.ro, txSpec{readOnly: true}, func(ctx context.Context, tx *sql.Tx) error {
		info, err := detectServer(ctx, tx)
		if err != nil {
			return err
		}
		if !info.hasChecks || !info.hasExpression || info.mariadb {
			t.Skipf("butuh MySQL 8.x, server ini: %+v", info)
		}

		out := &domain.Schema{Tables: []domain.Table{{Schema: testDB, Name: f.cust}}}
		pos := map[string]int{f.cust: 0}
		info.hasChecks = false
		if err := readConstraints(ctx, tx, testDB, info, out, pos); err != nil {
			t.Fatalf("tanpa CHECK_CONSTRAINTS: %v", err)
		}
		for _, c := range out.Tables[0].Constraints {
			if c.Type == domain.ConstraintCheck {
				t.Errorf("tidak boleh ada check: %+v", c)
			}
		}
		if len(out.Tables[0].Constraints) < 2 {
			t.Errorf("PK dan unique tetap harus terbaca: %+v", out.Tables[0].Constraints)
		}

		info.hasExpression = false
		if err := readIndexes(ctx, tx, testDB, info, out, pos); err != nil {
			t.Fatalf("tanpa kolom EXPRESSION: %v", err)
		}
		if len(out.Tables[0].Indexes) == 0 {
			t.Error("index tetap harus terbaca")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestIntrospect_Integration_ReleasesConnections(t *testing.T) {
	d := newTestDriver(t)
	_ = newFixture(t, d)
	for i := 0; i < 3; i++ {
		if _, err := d.Introspect(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if n := d.ro.Stats().InUse; n != 0 {
		t.Errorf("koneksi masih terpakai setelah Introspect: %d", n)
	}
}
