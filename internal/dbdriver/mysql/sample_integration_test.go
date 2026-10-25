package mysql

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"dbplatform/internal/domain"
)

func sampleDriver(t *testing.T) *Driver {
	t.Helper()
	d := newTestDriver(t)
	var n int
	err := rawDB(t, d).QueryRow("SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_NAME = 'type_samples'", testDB).Scan(&n)
	if err != nil || n == 0 {
		t.Skip("data sampel targetmysql belum dimuat (lihat docker/targetmysql/init.sql)")
	}
	return d
}

func TestSample_Integration_IntrospectCoversTheCatalog(t *testing.T) {
	d := sampleDriver(t)
	s, err := d.Introspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"customers", "products", "orders", "order_items", "event_log", "Mixed Case", "type_samples"} {
		tb := findTable(t, s, name)
		if tb.Kind != domain.TableKindTable || len(tb.Columns) == 0 {
			t.Errorf("%s: %+v", name, tb)
		}
		if tb.EstimatedRows < 0 {
			t.Errorf("%s: estimasi baris %d, ANALYZE di init.sql seharusnya mengisinya", name, tb.EstimatedRows)
		}
	}
	for _, name := range []string{"active_customers", "order_totals"} {
		v := findTable(t, s, name)
		if v.Kind != domain.TableKindView || v.EstimatedRows != -1 {
			t.Errorf("%s: kind=%v rows=%d", name, v.Kind, v.EstimatedRows)
		}
	}

	cust := findTable(t, s, "customers")
	if cust.EstimatedRows < 100 || cust.EstimatedRows > 140 {
		t.Errorf("estimasi customers = %d, ingin sekitar 120", cust.EstimatedRows)
	}
	types := map[string]string{}
	for _, c := range cust.Columns {
		types[c.Name] = c.DataType
	}
	if types["profile"] != "json" || types["tags"] != "set('vip','new','wholesale','blocked')" || types["balance"] != "decimal(12,2)" {
		t.Errorf("tipe kolom customers: %v", types)
	}
	if c := findConstraint(t, cust, "customers_email_key"); c.Type != domain.ConstraintUnique {
		t.Errorf("%+v", c)
	}
	if c := findConstraint(t, cust, "customers_balance_nonneg"); c.Type != domain.ConstraintCheck || !strings.Contains(c.CheckExpr, "balance") {
		t.Errorf("%+v", c)
	}
	if ft := findIndex(t, cust, "customers_notes_ft"); ft.Method != "fulltext" {
		t.Errorf("%+v", ft)
	}
	if fn := findIndex(t, cust, "customers_lower_name_idx"); !strings.Contains(strings.ToLower(fn.Columns[0]), "lower") {
		t.Errorf("%+v", fn)
	}
	if ix := findIndex(t, cust, "customers_created_idx"); !strings.Contains(ix.Definition, "`created_at` DESC") {
		t.Errorf("%+v", ix)
	}

	orders := findTable(t, s, "orders")
	fk := findConstraint(t, orders, "orders_customer_fk")
	if fk.RefTable != "customers" || fk.OnUpdate != "CASCADE" || fk.OnDelete != "RESTRICT" || !reflect.DeepEqual(fk.RefColumns, []string{"id"}) {
		t.Errorf("fk = %+v", fk)
	}
	if ck := findConstraint(t, orders, "orders_status_check"); ck.Type != domain.ConstraintCheck {
		t.Errorf("%+v", ck)
	}

	items := findTable(t, s, "order_items")
	if pk := findConstraint(t, items, "PRIMARY"); !reflect.DeepEqual(pk.Columns, []string{"order_id", "sku"}) {
		t.Errorf("pk = %+v", pk)
	}
	if fk := findConstraint(t, items, "order_items_product_fk"); fk.RefTable != "products" || fk.OnDelete == "" {
		t.Errorf("fk = %+v", fk)
	}
	if mc := findTable(t, s, "Mixed Case"); mc.Columns[1].Name != "Some Value" {
		t.Errorf("Mixed Case: %+v", mc.Columns)
	}

	for _, tb := range s.Tables {
		for _, bad := range []string{"mysql", "sys", "information_schema", "performance_schema"} {
			if tb.Schema == bad {
				t.Errorf("schema sistem %q tidak boleh muncul", bad)
			}
		}
	}
}

func TestSample_Integration_TableDataPagesThroughEventLog(t *testing.T) {
	d := sampleDriver(t)
	ref := domain.TableRef{Schema: testDB, Table: "event_log"}
	ctx := context.Background()

	seen := 0
	for page := 1; page <= 5; page++ {
		td, err := d.TableData(ctx, ref, domain.PageRequest{Page: page, PageSize: 50})
		if err != nil {
			t.Fatal(err)
		}
		if len(td.Rows) != 50 {
			t.Fatalf("halaman %d: %d baris", page, len(td.Rows))
		}
		if td.Rows[0][0].Value != strconv.Itoa(seen+1) {
			t.Errorf("halaman %d mulai dari id %s, want %d", page, td.Rows[0][0].Value, seen+1)
		}
		if td.HasNext != (page < 5) {
			t.Errorf("halaman %d HasNext = %v", page, td.HasNext)
		}
		seen += 50
	}
	empty, err := d.TableData(ctx, ref, domain.PageRequest{Page: 6, PageSize: 50})
	if err != nil || len(empty.Rows) != 0 || empty.HasNext {
		t.Fatalf("halaman 6: err=%v rows=%d next=%v", err, len(empty.Rows), empty.HasNext)
	}

	td, _ := d.TableData(ctx, ref, domain.PageRequest{Page: 1, PageSize: 5})
	if !td.Rows[4][2].Null || td.Rows[3][2].Value != "detail untuk event 4" {
		t.Errorf("NULL dan teks pada detail: %+v", td.Rows)
	}
}

func itoa(n int) string {
	return strings.TrimSpace(strings.Join([]string{string(rune('0' + 0))}, "")[:0] + formatInt(n))
}

func formatInt(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func TestSample_Integration_TableDataTypesAndAwkwardNames(t *testing.T) {
	d := sampleDriver(t)
	ctx := context.Background()

	mc, err := d.TableData(ctx, domain.TableRef{Schema: testDB, Table: "Mixed Case"}, domain.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(mc.Rows) != 3 || !mc.Rows[1][1].Null || mc.Rows[2][1].Value != `<script>alert("xss")</script> & "kutip" 'tunggal'` {
		t.Errorf("Mixed Case = %+v", mc.Rows)
	}

	long, err := d.TableData(ctx, domain.TableRef{Schema: testDB, Table: "customers"}, domain.PageRequest{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	var sawLong bool
	for _, r := range long.Rows {
		if len(r[7].Value) > 200 {
			sawLong = true
		}
	}
	if !sawLong || !long.HasNext {
		t.Errorf("teks panjang harus utuh dari driver dan HasNext true (long=%v next=%v)", sawLong, long.HasNext)
	}

	ts, err := d.TableData(ctx, domain.TableRef{Schema: testDB, Table: "type_samples"}, domain.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ts.Rows) != 3 {
		t.Fatalf("type_samples rows = %d", len(ts.Rows))
	}
	col := func(name string) int {
		for i, c := range ts.Columns {
			if c == name {
				return i
			}
		}
		t.Fatalf("kolom %s tidak ada", name)
		return -1
	}
	v := func(s string) domain.Cell { return domain.Cell{Value: s} }
	want := map[string]domain.Cell{
		"c_tinyint": v("-128"), "c_smallint": v("32767"), "c_bigint_unsigned": v("18446744073709551615"),
		"c_float": v("1.5"), "c_double": v("3.141592653589793"), "c_decimal": v("1234567890.123456"),
		"c_char": v("abc"), "c_varchar": v("teks biasa"), "c_blob": v("0xdeadbeef"), "c_bit": v("0xaa"),
		"c_date": v("2025-10-24"), "c_time": v("13:45:10"), "c_datetime": v("2025-10-24 13:45:10.123"),
		"c_year": v("2025"), "c_enum": v("besar"), "c_json": v(`{"a": [1, 2, 3]}`),
	}
	for name, w := range want {
		if got := ts.Rows[0][col(name)]; got != w {
			t.Errorf("baris 1 %s = %+v, want %+v", name, got, w)
		}
	}
	if got := ts.Rows[0][col("c_longtext")].Value; len(got) != 300 {
		t.Errorf("c_longtext = %d karakter", len(got))
	}
	for i, c := range ts.Columns {
		if c == "id" || c == "c_enum" {
			continue
		}
		if !ts.Rows[1][i].Null {
			t.Errorf("baris 2 %s harus NULL: %+v", c, ts.Rows[1][i])
		}
	}
	if got := ts.Rows[2][col("c_blob")]; got.Null || got.Value != "0x" {
		t.Errorf("BLOB kosong = %+v", got)
	}
	if got := ts.Rows[2][col("c_varchar")]; got.Null || got.Value != "" {
		t.Errorf("string kosong = %+v", got)
	}

	v1, err := d.TableData(ctx, domain.TableRef{Schema: testDB, Table: "order_totals"}, domain.PageRequest{Page: 1, PageSize: 10})
	if err != nil || len(v1.Rows) != 10 || !v1.HasNext {
		t.Fatalf("view order_totals: err=%v rows=%d next=%v", err, len(v1.Rows), v1.HasNext)
	}
}
