package schemadiff

import (
	"strings"
	"testing"

	"dbplatform/internal/domain"
)

func str(s string) *string { return &s }

func col(pos int, name, typ string, nullable bool, def *string) domain.Column {
	return domain.Column{Name: name, Position: pos, DataType: typ, Nullable: nullable, Default: def}
}

func table(schema, name string, cols []domain.Column, cons []domain.Constraint, idx []domain.Index) domain.Table {
	return domain.Table{Schema: schema, Name: name, Kind: domain.TableKindTable, EstimatedRows: -1, Columns: cols, Constraints: cons, Indexes: idx}
}

func schema(tables ...domain.Table) *domain.Schema { return &domain.Schema{Tables: tables} }

func pk(name string, cols ...string) domain.Constraint {
	return domain.Constraint{Name: name, Type: domain.ConstraintPrimaryKey, Columns: cols}
}

func uniq(name string, cols ...string) domain.Constraint {
	return domain.Constraint{Name: name, Type: domain.ConstraintUnique, Columns: cols}
}

func check(name, expr string) domain.Constraint {
	return domain.Constraint{Name: name, Type: domain.ConstraintCheck, CheckExpr: expr}
}

func fk(name, refSchema, refTable string, cols, refCols []string, onUpdate, onDelete string) domain.Constraint {
	return domain.Constraint{
		Name: name, Type: domain.ConstraintForeignKey, Columns: cols,
		RefSchema: refSchema, RefTable: refTable, RefColumns: refCols, OnUpdate: onUpdate, OnDelete: onDelete,
	}
}

func kinds(changes []Change) []string {
	out := make([]string, len(changes))
	for i, c := range changes {
		out[i] = c.Kind
	}
	return out
}

func findKind(changes []Change, kind string) []Change {
	var out []Change
	for _, c := range changes {
		if c.Kind == kind {
			out = append(out, c)
		}
	}
	return out
}

func sqlOf(changes []Change) []string {
	out := make([]string, len(changes))
	for i, c := range changes {
		out[i] = c.SQL
	}
	return out
}

func equalStrings(a, b []string) bool {
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

func customers() domain.Table {
	return table("public", "customers",
		[]domain.Column{col(1, "id", "integer", false, nil), col(2, "email", "text", false, nil), col(3, "created_at", "timestamp with time zone", false, str("now()"))},
		[]domain.Constraint{pk("customers_pkey", "id"), uniq("customers_email_key", "email")},
		[]domain.Index{
			{Name: "customers_pkey", Unique: true, Primary: true, Definition: "CREATE UNIQUE INDEX customers_pkey ON public.customers USING btree (id)"},
			{Name: "customers_email_key", Unique: true, Definition: "CREATE UNIQUE INDEX customers_email_key ON public.customers USING btree (email)"},
			{Name: "customers_created_idx", Definition: "CREATE INDEX customers_created_idx ON public.customers USING btree (created_at)"},
		})
}

func TestDiff_IdenticalSchemasProduceNothing(t *testing.T) {
	if got := Diff(schema(customers()), schema(customers())); len(got) != 0 {
		t.Errorf("Diff = %+v, mau kosong", got)
	}
	if got := Diff(nil, nil); len(got) != 0 {
		t.Errorf("Diff(nil, nil) = %+v", got)
	}
	if got := Diff(&domain.Schema{}, &domain.Schema{}); len(got) != 0 {
		t.Errorf("schema kosong = %+v", got)
	}
}

func TestDiff_NewTable(t *testing.T) {
	orders := table("public", "orders",
		[]domain.Column{col(1, "id", "bigint", false, nil), col(2, "customer_id", "integer", false, nil), col(3, "status", "text", true, str("'new'::text")), col(4, "total", "numeric(12,2)", false, nil)},
		[]domain.Constraint{
			fk("orders_customer_fk", "public", "customers", []string{"customer_id"}, []string{"id"}, "NO ACTION", "CASCADE"),
			check("orders_total_check", "CHECK ((total >= (0)::numeric))"),
			pk("orders_pkey", "id"),
		},
		[]domain.Index{
			{Name: "orders_pkey", Unique: true, Primary: true, Definition: "CREATE UNIQUE INDEX orders_pkey ON public.orders USING btree (id)"},
			{Name: "orders_customer_idx", Definition: "CREATE INDEX orders_customer_idx ON public.orders USING btree (customer_id)"},
		})

	got := Diff(schema(customers()), schema(customers(), orders))

	wantKinds := []string{KindCreateTable, KindAddConstraint, KindAddConstraint, KindAddConstraint, KindCreateIndex}
	if !equalStrings(kinds(got), wantKinds) {
		t.Fatalf("kinds = %v, mau %v", kinds(got), wantKinds)
	}
	wantCreate := "CREATE TABLE \"public\".\"orders\" (\n" +
		"  \"id\" bigint NOT NULL,\n" +
		"  \"customer_id\" integer NOT NULL,\n" +
		"  \"status\" text DEFAULT 'new'::text,\n" +
		"  \"total\" numeric(12,2) NOT NULL\n);"
	if got[0].SQL != wantCreate {
		t.Errorf("create table:\n%s\nmau:\n%s", got[0].SQL, wantCreate)
	}
	wantConstraints := []string{
		`ALTER TABLE "public"."orders" ADD CONSTRAINT "orders_pkey" PRIMARY KEY ("id");`,
		`ALTER TABLE "public"."orders" ADD CONSTRAINT "orders_total_check" CHECK ((total >= (0)::numeric));`,
		`ALTER TABLE "public"."orders" ADD CONSTRAINT "orders_customer_fk" FOREIGN KEY ("customer_id") REFERENCES "public"."customers" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;`,
	}
	if !equalStrings(sqlOf(got[1:4]), wantConstraints) {
		t.Errorf("urutan constraint (PK, check, FK terakhir):\n%s", strings.Join(sqlOf(got[1:4]), "\n"))
	}
	if got[4].SQL != "CREATE INDEX orders_customer_idx ON public.orders USING btree (customer_id);" {
		t.Errorf("create index = %s", got[4].SQL)
	}
	for _, c := range got {
		if c.Destructive {
			t.Errorf("%s tidak boleh destruktif", c.Kind)
		}
	}
}

func TestDiff_NewTableIgnoresPrimaryAndConstraintBackedIndexes(t *testing.T) {
	got := Diff(schema(), schema(customers()))
	idx := findKind(got, KindCreateIndex)
	if len(idx) != 1 || !strings.Contains(idx[0].SQL, "customers_created_idx") {
		t.Errorf("index = %v, mau hanya customers_created_idx (index PK dan unique ikut constraint)", sqlOf(idx))
	}
	if n := len(findKind(got, KindAddConstraint)); n != 2 {
		t.Errorf("constraint = %d, mau 2 (PK dan unique)", n)
	}
}

func TestDiff_NewSchemaIsCreatedOnlyWhenMissing(t *testing.T) {
	hr := table("hr", "employees", []domain.Column{col(1, "id", "integer", false, nil)}, nil, nil)
	hr2 := table("hr", "managers", []domain.Column{col(1, "id", "integer", false, nil)}, nil, nil)

	got := Diff(schema(customers()), schema(customers(), hr, hr2))
	if s := findKind(got, KindCreateSchema); len(s) != 1 || s[0].SQL != `CREATE SCHEMA IF NOT EXISTS "hr";` {
		t.Errorf("create schema = %v, mau satu kali", sqlOf(s))
	}
	if got[0].Kind != KindCreateSchema {
		t.Errorf("schema harus dibuat sebelum tabel: %v", kinds(got))
	}

	existing := table("hr", "old", []domain.Column{col(1, "id", "integer", false, nil)}, nil, nil)
	got = Diff(schema(existing), schema(existing, hr))
	if len(findKind(got, KindCreateSchema)) != 0 {
		t.Error("schema yang sudah ada di target tidak boleh dibuat lagi")
	}
}

func TestDiff_PublicSchemaIsNeverCreated(t *testing.T) {
	got := Diff(schema(), schema(customers()))
	if len(findKind(got, KindCreateSchema)) != 0 {
		t.Errorf("schema public selalu ada, tidak perlu dibuat: %v", kinds(got))
	}
	if got[0].Kind != KindCreateTable {
		t.Errorf("perubahan pertama = %s, mau create_table", got[0].Kind)
	}
}

func TestDiff_DropTableIsDestructiveAndCascades(t *testing.T) {
	got := Diff(schema(customers()), schema())
	if len(got) != 1 || got[0].Kind != KindDropTable || !got[0].Destructive {
		t.Fatalf("Diff = %+v", got)
	}
	if got[0].SQL != `DROP TABLE "public"."customers" CASCADE;` {
		t.Errorf("sql = %s", got[0].SQL)
	}
}

func TestDiff_ViewsAreIgnored(t *testing.T) {
	view := domain.Table{Schema: "public", Name: "active_customers", Kind: domain.TableKindView, Columns: []domain.Column{col(1, "id", "integer", true, nil)}}
	if got := Diff(schema(customers(), view), schema(customers())); len(got) != 0 {
		t.Errorf("view hanya di target: %+v", got)
	}
	if got := Diff(schema(customers()), schema(customers(), view)); len(got) != 0 {
		t.Errorf("view hanya di sumber: %+v", got)
	}
}

func TestDiff_ColumnChanges(t *testing.T) {
	current := table("public", "t",
		[]domain.Column{
			col(1, "id", "integer", false, nil),
			col(2, "name", "character varying(10)", true, nil),
			col(3, "qty", "integer", true, nil),
			col(4, "note", "text", false, str("'x'::text")),
			col(5, "legacy", "text", true, nil),
			col(6, "same", "text", true, str("'a'::text")),
		}, nil, nil)
	desired := table("public", "t",
		[]domain.Column{
			col(1, "id", "integer", false, nil),
			col(2, "name", "character varying(50)", true, nil),
			col(3, "qty", "integer", false, str("0")),
			col(4, "note", "text", true, nil),
			col(6, "same", "text", true, str("'a'::text")),
			col(7, "created", "timestamp with time zone", false, str("now()")),
		}, nil, nil)

	got := Diff(schema(current), schema(desired))
	want := []string{
		`ALTER TABLE "public"."t" ADD COLUMN "created" timestamp with time zone NOT NULL DEFAULT now();`,
		`ALTER TABLE "public"."t" ALTER COLUMN "name" TYPE character varying(50) USING "name"::character varying(50);`,
		`ALTER TABLE "public"."t" ALTER COLUMN "qty" SET NOT NULL;`,
		`ALTER TABLE "public"."t" ALTER COLUMN "qty" SET DEFAULT 0;`,
		`ALTER TABLE "public"."t" ALTER COLUMN "note" DROP NOT NULL;`,
		`ALTER TABLE "public"."t" ALTER COLUMN "note" DROP DEFAULT;`,
		`ALTER TABLE "public"."t" DROP COLUMN "legacy";`,
	}
	if !equalStrings(sqlOf(got), want) {
		t.Errorf("sql:\n%s\nmau:\n%s", strings.Join(sqlOf(got), "\n"), strings.Join(want, "\n"))
	}
	wantKinds := []string{KindAddColumn, KindAlterColumnType, KindAlterColumnNull, KindAlterColumnDefault, KindAlterColumnNull, KindAlterColumnDefault, KindDropColumn}
	if !equalStrings(kinds(got), wantKinds) {
		t.Errorf("kinds = %v", kinds(got))
	}
	for _, c := range got {
		if c.Destructive != (c.Kind == KindDropColumn) {
			t.Errorf("%s: destruktif = %v", c.Kind, c.Destructive)
		}
	}
}

func TestDiff_SequenceDefaultsAreIgnored(t *testing.T) {
	withSeq := table("public", "t", []domain.Column{col(1, "id", "integer", false, str("nextval('t_id_seq'::regclass)"))}, nil, nil)
	plain := table("public", "t", []domain.Column{col(1, "id", "integer", false, nil)}, nil, nil)
	if got := Diff(schema(plain), schema(withSeq)); len(got) != 0 {
		t.Errorf("default nextval tidak boleh menghasilkan perubahan: %+v", got)
	}
	if got := Diff(schema(withSeq), schema(plain)); len(got) != 0 {
		t.Errorf("sebaliknya: %+v", got)
	}

	got := Diff(schema(), schema(withSeq))
	if strings.Contains(got[0].SQL, "nextval") || !strings.Contains(got[0].SQL, `"id" integer NOT NULL`) {
		t.Errorf("create table = %s", got[0].SQL)
	}
}

func TestDiff_ColumnOrderIsIgnored(t *testing.T) {
	a := table("public", "t", []domain.Column{col(1, "a", "integer", true, nil), col(2, "b", "text", true, nil)}, nil, nil)
	b := table("public", "t", []domain.Column{col(1, "b", "text", true, nil), col(2, "a", "integer", true, nil)}, nil, nil)
	if got := Diff(schema(a), schema(b)); len(got) != 0 {
		t.Errorf("urutan kolom berbeda tidak boleh menghasilkan perubahan: %+v", got)
	}
}

func TestDiff_ConstraintChanges(t *testing.T) {
	base := []domain.Column{col(1, "id", "integer", false, nil), col(2, "ref", "integer", true, nil), col(3, "qty", "integer", true, nil)}
	current := table("public", "t", base, []domain.Constraint{
		pk("t_pkey", "id"),
		check("t_qty_check", "CHECK ((qty > 0))"),
		check("t_old_check", "CHECK ((qty < 100))"),
		fk("t_ref_fk", "public", "p", []string{"ref"}, []string{"id"}, "NO ACTION", "NO ACTION"),
		fk("t_changed_fk", "public", "p", []string{"ref"}, []string{"id"}, "NO ACTION", "NO ACTION"),
		uniq("t_same_key", "qty"),
	}, nil)
	desired := table("public", "t", base, []domain.Constraint{
		pk("t_pkey", "id"),
		check("t_qty_check", "CHECK ((qty >= 0))"),
		fk("t_changed_fk", "public", "p", []string{"ref"}, []string{"id"}, "NO ACTION", "CASCADE"),
		uniq("t_same_key", "qty"),
		uniq("t_new_key", "ref"),
	}, nil)

	got := Diff(schema(current), schema(desired))

	drops := sqlOf(findKind(got, KindDropConstraint))
	wantDrops := []string{
		`ALTER TABLE "public"."t" DROP CONSTRAINT "t_changed_fk";`,
		`ALTER TABLE "public"."t" DROP CONSTRAINT "t_ref_fk";`,
		`ALTER TABLE "public"."t" DROP CONSTRAINT "t_old_check";`,
		`ALTER TABLE "public"."t" DROP CONSTRAINT "t_qty_check";`,
	}
	if !equalStrings(drops, wantDrops) {
		t.Errorf("drop constraint (foreign key lebih dulu):\n%s", strings.Join(drops, "\n"))
	}
	adds := sqlOf(findKind(got, KindAddConstraint))
	wantAdds := []string{
		`ALTER TABLE "public"."t" ADD CONSTRAINT "t_new_key" UNIQUE ("ref");`,
		`ALTER TABLE "public"."t" ADD CONSTRAINT "t_qty_check" CHECK ((qty >= 0));`,
		`ALTER TABLE "public"."t" ADD CONSTRAINT "t_changed_fk" FOREIGN KEY ("ref") REFERENCES "public"."p" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;`,
	}
	if !equalStrings(adds, wantAdds) {
		t.Errorf("add constraint:\n%s", strings.Join(adds, "\n"))
	}
	if strings.Contains(strings.Join(sqlOf(got), "\n"), "t_pkey") || strings.Contains(strings.Join(sqlOf(got), "\n"), "t_same_key") {
		t.Error("constraint yang tidak berubah tidak boleh disentuh")
	}
}

func TestDiff_CheckExpressionWithoutKeywordIsWrapped(t *testing.T) {
	desired := table("public", "t", []domain.Column{col(1, "qty", "integer", true, nil)}, []domain.Constraint{check("t_qty_check", "qty > 0")}, nil)
	current := table("public", "t", []domain.Column{col(1, "qty", "integer", true, nil)}, nil, nil)
	got := Diff(schema(current), schema(desired))
	if len(got) != 1 || !strings.HasSuffix(got[0].SQL, `CHECK (qty > 0);`) {
		t.Errorf("Diff = %v", sqlOf(got))
	}
}

func TestDiff_IndexChanges(t *testing.T) {
	cols := []domain.Column{col(1, "a", "integer", true, nil), col(2, "b", "integer", true, nil)}
	current := table("public", "t", cols, nil, []domain.Index{
		{Name: "t_a_idx", Definition: "CREATE INDEX t_a_idx ON public.t USING btree (a)"},
		{Name: "t_b_idx", Definition: "CREATE INDEX t_b_idx ON public.t USING btree (b)"},
		{Name: "t_same_idx", Definition: "CREATE INDEX t_same_idx ON public.t USING btree (a, b)"},
	})
	desired := table("public", "t", cols, nil, []domain.Index{
		{Name: "t_b_idx", Definition: "CREATE INDEX t_b_idx ON public.t USING hash (b)"},
		{Name: "t_same_idx", Definition: "CREATE INDEX t_same_idx ON public.t USING btree (a, b)"},
		{Name: "t_new_idx", Definition: "CREATE UNIQUE INDEX t_new_idx ON public.t USING btree (a)"},
	})

	got := Diff(schema(current), schema(desired))
	wantKinds := []string{KindDropIndex, KindDropIndex, KindCreateIndex, KindCreateIndex}
	if !equalStrings(kinds(got), wantKinds) {
		t.Fatalf("kinds = %v, mau %v", kinds(got), wantKinds)
	}
	want := []string{
		`DROP INDEX "public"."t_a_idx";`,
		`DROP INDEX "public"."t_b_idx";`,
		`CREATE INDEX t_b_idx ON public.t USING hash (b);`,
		`CREATE UNIQUE INDEX t_new_idx ON public.t USING btree (a);`,
	}
	if !equalStrings(sqlOf(got), want) {
		t.Errorf("sql:\n%s", strings.Join(sqlOf(got), "\n"))
	}
}

func TestDiff_StepOrderFollowsDependencies(t *testing.T) {
	current := schema(
		table("public", "old", []domain.Column{col(1, "id", "integer", false, nil)}, nil, nil),
		table("public", "keep",
			[]domain.Column{col(1, "id", "integer", false, nil), col(2, "gone", "text", true, nil)},
			[]domain.Constraint{fk("keep_fk", "public", "old", []string{"id"}, []string{"id"}, "NO ACTION", "NO ACTION")},
			[]domain.Index{{Name: "keep_idx", Definition: "CREATE INDEX keep_idx ON public.keep USING btree (id)"}}),
	)
	desired := schema(
		table("public", "keep",
			[]domain.Column{col(1, "id", "integer", false, nil), col(3, "extra", "text", true, nil)},
			[]domain.Constraint{pk("keep_pkey", "id")}, nil),
		table("public", "fresh", []domain.Column{col(1, "id", "integer", false, nil), col(2, "keep_id", "integer", true, nil)},
			[]domain.Constraint{fk("fresh_fk", "public", "keep", []string{"keep_id"}, []string{"id"}, "NO ACTION", "NO ACTION")}, nil),
	)

	got := Diff(current, desired)
	want := []string{
		KindCreateTable, KindAddColumn, KindDropConstraint, KindAddConstraint, KindAddConstraint,
		KindDropIndex, KindDropColumn, KindDropTable,
	}
	if !equalStrings(kinds(got), want) {
		t.Fatalf("urutan = %v\nmau     = %v", kinds(got), want)
	}
	adds := findKind(got, KindAddConstraint)
	if !strings.Contains(adds[0].SQL, "PRIMARY KEY") || !strings.Contains(adds[1].SQL, "FOREIGN KEY") {
		t.Errorf("PK harus sebelum FK: %v", sqlOf(adds))
	}
	last := got[len(got)-1]
	if last.Kind != KindDropTable || !last.Destructive {
		t.Errorf("DROP TABLE harus paling akhir dan destruktif: %+v", last)
	}
}

func TestDiff_OutputDoesNotDependOnInputOrder(t *testing.T) {
	a := table("public", "a", []domain.Column{col(1, "id", "integer", false, nil)}, []domain.Constraint{pk("a_pkey", "id")}, nil)
	b := table("public", "b", []domain.Column{col(1, "id", "integer", false, nil)}, []domain.Constraint{pk("b_pkey", "id")}, nil)
	z := table("zeta", "z", []domain.Column{col(1, "id", "integer", false, nil)}, nil, nil)

	first := Diff(schema(), schema(a, b, z))
	second := Diff(schema(), schema(z, b, a))
	if !equalStrings(sqlOf(first), sqlOf(second)) {
		t.Errorf("urutan input memengaruhi hasil:\n%v\n%v", sqlOf(first), sqlOf(second))
	}
	for i := range first {
		if first[i].ID != second[i].ID {
			t.Errorf("ID tidak stabil di indeks %d", i)
		}
	}
}

func TestDiff_QuotesUnusualNames(t *testing.T) {
	odd := table(`My "Schema"`, `Mixed Case`, []domain.Column{col(1, `Some "Value"`, "text", true, nil)}, nil, nil)
	got := Diff(schema(), schema(odd))
	if got[0].SQL != "CREATE SCHEMA IF NOT EXISTS \"My \"\"Schema\"\"\";" {
		t.Errorf("create schema = %s", got[0].SQL)
	}
	if !strings.Contains(got[1].SQL, `"My ""Schema"""."Mixed Case"`) || !strings.Contains(got[1].SQL, `"Some ""Value""" text`) {
		t.Errorf("create table = %s", got[1].SQL)
	}
}

func TestChanges_IDsAreUniqueAndSelectable(t *testing.T) {
	got := Diff(schema(customers()), schema(customers(), table("public", "t2", []domain.Column{col(1, "id", "integer", false, nil)}, []domain.Constraint{pk("t2_pkey", "id")}, nil)))
	seen := map[string]bool{}
	for _, c := range got {
		if len(c.ID) != 12 || seen[c.ID] {
			t.Errorf("ID %q tidak unik atau panjangnya salah", c.ID)
		}
		seen[c.ID] = true
	}

	chosen := Select(got, map[string]bool{got[1].ID: true, got[0].ID: true, "tidak-ada": true})
	if len(chosen) != 2 || chosen[0].ID != got[0].ID || chosen[1].ID != got[1].ID {
		t.Errorf("Select harus mempertahankan urutan diff dan mengabaikan ID asing: %+v", chosen)
	}
	if len(Select(got, nil)) != 0 {
		t.Error("tanpa pilihan, hasilnya kosong")
	}

	sql := SQL(chosen)
	if !strings.Contains(sql, "CREATE TABLE") || strings.Count(sql, ";") != 2 {
		t.Errorf("SQL gabungan = %q", sql)
	}
	if SQL(nil) != "" {
		t.Error("SQL tanpa perubahan harus kosong")
	}
}
