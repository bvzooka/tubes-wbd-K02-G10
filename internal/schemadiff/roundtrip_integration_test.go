package schemadiff_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/dbdriver/postgres"
	"dbplatform/internal/domain"
	"dbplatform/internal/schemadiff"
	"dbplatform/internal/testutil"
)

func introspect(t *testing.T, pool *pgxpool.Pool) *domain.Schema {
	t.Helper()
	s, err := postgres.NewFromPool(pool).Introspect(context.Background())
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	return s
}

func apply(t *testing.T, pool *pgxpool.Pool, changes []schemadiff.Change) {
	t.Helper()
	testutil.MustExec(t, pool, schemadiff.SQL(changes))
}

func kindsOf(changes []schemadiff.Change) []string {
	out := make([]string, len(changes))
	for i, c := range changes {
		out[i] = c.Kind + " " + c.Object
	}
	return out
}

func assertConverged(t *testing.T, target *pgxpool.Pool, source *domain.Schema) {
	t.Helper()
	if left := schemadiff.Diff(introspect(t, target), source); len(left) != 0 {
		t.Fatalf("setelah diff diterapkan, masih ada %d perbedaan:\n%s\nSQL sisa:\n%s",
			len(left), strings.Join(kindsOf(left), "\n"), schemadiff.SQL(left))
	}
}

func nonDestructive(changes []schemadiff.Change) []schemadiff.Change {
	var out []schemadiff.Change
	for _, c := range changes {
		if !c.Destructive {
			out = append(out, c)
		}
	}
	return out
}

func rowCount(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func sampleInitSQL(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../docker/targetdb/init.sql")
	if err != nil {
		t.Skipf("init.sql tidak ditemukan: %v", err)
	}
	return string(b)
}

func TestRoundTrip_Integration_SameDDLInTwoDatabasesHasNoDifferences(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	init := sampleInitSQL(t)
	a := testutil.NewTargetDB(t, init)
	b := testutil.NewTargetDB(t, init)

	if got := schemadiff.Diff(introspect(t, a), introspect(t, b)); len(got) != 0 {
		t.Fatalf("DDL yang sama tidak boleh menghasilkan perbedaan:\n%s", strings.Join(kindsOf(got), "\n"))
	}
}

func TestRoundTrip_Integration_EmptyTargetBecomesCopyOfSampleDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	source := testutil.NewTargetDB(t, sampleInitSQL(t))
	target := testutil.NewTargetDB(t)
	sourceSchema := introspect(t, source)

	changes := schemadiff.Diff(introspect(t, target), sourceSchema)
	if len(changes) == 0 {
		t.Fatal("target kosong harus menghasilkan perubahan")
	}
	counts := map[string]int{}
	for _, c := range changes {
		counts[c.Kind]++
		if c.Destructive {
			t.Errorf("membangun dari kosong tidak boleh destruktif: %s %s", c.Kind, c.Object)
		}
	}
	if counts[schemadiff.KindCreateTable] != 7 || counts[schemadiff.KindCreateSchema] != 1 {
		t.Errorf("create_table = %d (mau 7), create_schema = %d (mau 1: hr)", counts[schemadiff.KindCreateTable], counts[schemadiff.KindCreateSchema])
	}

	apply(t, target, changes)
	assertConverged(t, target, sourceSchema)

	for _, table := range []string{"customers", "orders", "order_items", "products", "event_log", `"Mixed Case"`, "hr.employees"} {
		var ok bool
		if err := target.QueryRow(context.Background(), `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&ok); err != nil || !ok {
			t.Errorf("tabel %s tidak ada di target (err=%v)", table, err)
		}
	}
	if n := rowCount(t, target, "customers"); n != 0 {
		t.Errorf("schema diff tidak boleh menyalin data, customers = %d baris", n)
	}
}

const oldSchemaSQL = `
CREATE TABLE people (id int PRIMARY KEY, name varchar(10) NOT NULL, age int, nickname text, legacy text);
CREATE TABLE pets (id int PRIMARY KEY, owner_id int, name text);
ALTER TABLE pets ADD CONSTRAINT pets_owner_fk FOREIGN KEY (owner_id) REFERENCES people (id);
CREATE INDEX people_name_idx ON people (name);
CREATE INDEX pets_name_idx ON pets (name);
ALTER TABLE people ADD CONSTRAINT people_age_check CHECK (age > 0);
CREATE TABLE obsolete (id int PRIMARY KEY, person_id int REFERENCES people (id));
INSERT INTO people VALUES (1, 'ani', 30, 'a', 'x'), (2, 'budi', 40, NULL, 'y');
INSERT INTO pets VALUES (1, 1, 'kiki');
`

const newSchemaSQL = `
CREATE TABLE people (
  id int PRIMARY KEY,
  name varchar(50) NOT NULL,
  age int NOT NULL DEFAULT 18,
  nickname text,
  email text,
  created timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT people_age_check CHECK (age >= 18),
  CONSTRAINT people_email_key UNIQUE (email)
);
CREATE TABLE pets (
  id int PRIMARY KEY,
  owner_id int,
  name text,
  species text NOT NULL DEFAULT 'dog',
  CONSTRAINT pets_owner_fk FOREIGN KEY (owner_id) REFERENCES people (id) ON DELETE CASCADE
);
CREATE INDEX people_name_idx ON people (lower(name));
CREATE INDEX pets_owner_idx ON pets (owner_id);
CREATE TABLE visits (
  id int PRIMARY KEY,
  pet_id int NOT NULL REFERENCES pets (id),
  at timestamptz NOT NULL DEFAULT now()
);
`

func TestRoundTrip_Integration_EvolvedSchemaKeepsExistingData(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	target := testutil.NewTargetDB(t, oldSchemaSQL)
	source := testutil.NewTargetDB(t, newSchemaSQL)
	sourceSchema := introspect(t, source)

	changes := schemadiff.Diff(introspect(t, target), sourceSchema)
	got := map[string]bool{}
	for _, c := range changes {
		got[c.Kind+" "+c.Object] = true
	}
	for _, want := range []string{
		"create_table public.visits",
		"add_column public.people.email",
		"add_column public.people.created",
		"add_column public.pets.species",
		"alter_column_type public.people.name",
		"alter_column_null public.people.age",
		"alter_column_default public.people.age",
		"drop_constraint public.people.people_age_check",
		"add_constraint public.people.people_age_check",
		"add_constraint public.people.people_email_key",
		"drop_constraint public.pets.pets_owner_fk",
		"add_constraint public.pets.pets_owner_fk",
		"drop_index public.people.people_name_idx",
		"create_index public.people.people_name_idx",
		"drop_index public.pets.pets_name_idx",
		"create_index public.pets.pets_owner_idx",
		"drop_column public.people.legacy",
		"drop_table public.obsolete",
	} {
		if !got[want] {
			t.Errorf("perubahan %q tidak ada; yang ada:\n%s", want, strings.Join(kindsOf(changes), "\n"))
		}
	}

	apply(t, target, changes)
	assertConverged(t, target, sourceSchema)

	if n := rowCount(t, target, "people"); n != 2 {
		t.Errorf("people = %d baris, mau 2 (data harus tetap utuh)", n)
	}
	if n := rowCount(t, target, "pets"); n != 1 {
		t.Errorf("pets = %d baris, mau 1", n)
	}
	var species string
	if err := target.QueryRow(context.Background(), `SELECT species FROM pets WHERE id = 1`).Scan(&species); err != nil || species != "dog" {
		t.Errorf("kolom baru NOT NULL dengan default harus terisi untuk baris lama: %q, %v", species, err)
	}
	var exists bool
	if err := target.QueryRow(context.Background(), `SELECT to_regclass('obsolete') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Errorf("tabel obsolete harus terhapus (ada=%v, err=%v)", exists, err)
	}
}

func TestRoundTrip_Integration_NonDestructiveSubsetRunsAloneAndLeavesOnlyDestructiveWork(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	target := testutil.NewTargetDB(t, oldSchemaSQL)
	source := testutil.NewTargetDB(t, newSchemaSQL)
	sourceSchema := introspect(t, source)

	changes := schemadiff.Diff(introspect(t, target), sourceSchema)
	apply(t, target, nonDestructive(changes))

	left := schemadiff.Diff(introspect(t, target), sourceSchema)
	if len(left) == 0 {
		t.Fatal("masih harus tersisa pekerjaan destruktif")
	}
	for _, c := range left {
		if !c.Destructive {
			t.Errorf("sisa non-destruktif tidak diharapkan: %s %s\n%s", c.Kind, c.Object, c.SQL)
		}
	}
	wantLeft := map[string]bool{"drop_column public.people.legacy": true, "drop_table public.obsolete": true}
	for _, c := range left {
		if !wantLeft[c.Kind+" "+c.Object] {
			t.Errorf("sisa tak terduga: %s %s", c.Kind, c.Object)
		}
	}
	if len(left) != len(wantLeft) {
		t.Errorf("sisa = %v", kindsOf(left))
	}

	apply(t, target, left)
	assertConverged(t, target, sourceSchema)
}

func TestRoundTrip_Integration_ApplyingTwiceIsANoOp(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	target := testutil.NewTargetDB(t, oldSchemaSQL)
	source := testutil.NewTargetDB(t, newSchemaSQL)
	sourceSchema := introspect(t, source)

	apply(t, target, schemadiff.Diff(introspect(t, target), sourceSchema))
	if again := schemadiff.Diff(introspect(t, target), sourceSchema); len(again) != 0 {
		t.Fatalf("diff kedua harus kosong: %v", kindsOf(again))
	}
}

func TestRoundTrip_Integration_ReverseDirectionAlsoConverges(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	a := testutil.NewTargetDB(t, oldSchemaSQL)
	b := testutil.NewTargetDB(t, newSchemaSQL)
	aSchema, bSchema := introspect(t, a), introspect(t, b)

	apply(t, b, schemadiff.Diff(bSchema, aSchema))
	assertConverged(t, b, aSchema)
}

func TestRoundTrip_Integration_CompositeKeysAndUnusualTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	source := testutil.NewTargetDB(t, `
CREATE TABLE parent (a int, b int, PRIMARY KEY (a, b));
CREATE TABLE child (
  id int PRIMARY KEY,
  pa int, pb int,
  tags text[],
  data jsonb DEFAULT '{}'::jsonb,
  price numeric(10, 2) CHECK (price IS NULL OR price > 0),
  blob bytea,
  born date,
  ratio real,
  CONSTRAINT child_parent_fk FOREIGN KEY (pa, pb) REFERENCES parent (a, b) ON UPDATE CASCADE ON DELETE SET NULL,
  CONSTRAINT child_pair_key UNIQUE (pa, pb)
);
CREATE INDEX child_tags_gin ON child USING gin (tags);
CREATE INDEX child_born_partial ON child (born) WHERE born IS NOT NULL;
CREATE TABLE "Weird ""Name"" Table" ("Col A" int PRIMARY KEY, "col b" text);
`)
	target := testutil.NewTargetDB(t)
	sourceSchema := introspect(t, source)

	apply(t, target, schemadiff.Diff(introspect(t, target), sourceSchema))
	assertConverged(t, target, sourceSchema)
}

func TestRoundTrip_Integration_ViewsAndSequenceDefaultsDoNotBreakConvergence(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	source := testutil.NewTargetDB(t, `
CREATE TABLE serial_table (id serial PRIMARY KEY, label text);
CREATE VIEW labels AS SELECT label FROM serial_table;
CREATE MATERIALIZED VIEW label_counts AS SELECT label, count(*) AS n FROM serial_table GROUP BY label;
CREATE INDEX label_counts_idx ON label_counts (label);
`)
	target := testutil.NewTargetDB(t)
	sourceSchema := introspect(t, source)

	changes := schemadiff.Diff(introspect(t, target), sourceSchema)
	for _, c := range changes {
		if strings.Contains(c.SQL, "nextval") || strings.Contains(c.SQL, "label_counts") || strings.Contains(c.SQL, `"labels"`) {
			t.Errorf("view dan sequence tidak boleh ikut dalam SQL: %s", c.SQL)
		}
	}
	apply(t, target, changes)
	assertConverged(t, target, sourceSchema)
}
