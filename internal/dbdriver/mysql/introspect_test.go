package mysql

import (
	"database/sql"
	"dbplatform/internal/domain"
	"reflect"
	"testing"
)

func ns(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

func TestColumnDefault(t *testing.T) {
	cases := []struct {
		name string
		def sql.NullString
		dataType string
		extra string
		mariadb  bool
		want string
	}{
		{"tanpa default", sql.NullString{}, "int", "", false, "<nil>"},
		{"auto increment", sql.NullString{}, "int", "auto_increment", false, "AUTO_INCREMENT"},
		{"angka", ns("0"), "int", "", false, "0"},
		{"desimal", ns("0.00"), "decimal", "", false, "0.00"},
		{"teks diberi kutip", ns("new"), "varchar", "", false, "'new'"},
		{"kutip di dalam teks digandakan", ns("it's"), "varchar", "", false, "'it''s'"},
		{"enum", ns("a"), "enum", "", false, "'a'"},
		{"tanggal", ns("2025-01-01"), "date", "", false, "'2025-01-01'"},
		{"ekspresi MySQL 8", ns("uuid()"), "varchar", "DEFAULT_GENERATED", false, "uuid()"},
		{"now() timestamp", ns("CURRENT_TIMESTAMP"), "timestamp", "DEFAULT_GENERATED", false, "CURRENT_TIMESTAMP"},
		{"now() tanpa penanda (MySQL 5.7)", ns("CURRENT_TIMESTAMP"), "datetime", "", false, "CURRENT_TIMESTAMP"},
		{"MariaDB literal sudah berkutip", ns("'new'"), "varchar", "", true, "'new'"},
		{"MariaDB DEFAULT NULL", ns("NULL"), "varchar", "", true, "<nil>"},
		{"MariaDB ekspresi", ns("current_timestamp()"), "timestamp", "", true, "current_timestamp()"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := columnDefault(tc.def, tc.dataType, tc.extra, tc.mariadb)
			if tc.want == "<nil>" {
				if got != nil {
					t.Fatalf("got %q, want nil", *got)
				}
				return
			}
			if got == nil || *got != tc.want {
				t.Fatalf("got %v, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckExpr(t *testing.T) {
	cases := map[string]string{
		"(`balance` >= 0)": "CHECK (`balance` >= 0)",
		"`balance` >= 0": "CHECK (`balance` >= 0)",
		"  (`a` > 1)  ": "CHECK (`a` > 1)",
	}
	for in, want := range cases {
		if got := checkExpr(in); got != want {
			t.Errorf("checkExpr(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIndexDefinition(t *testing.T) {
	cases := []struct {
		name string
		ix domain.Index
		parts []string
		want string
	}{
		{"primary", domain.Index{Name: "PRIMARY", Primary: true, Unique: true, Method: "btree"}, []string{"`id`"},
			"ALTER TABLE `shop`.`orders` ADD PRIMARY KEY (`id`)"},
		{"unique btree", domain.Index{Name: "uq_email", Unique: true, Method: "btree"}, []string{"`email`"},
			"CREATE UNIQUE INDEX `uq_email` ON `shop`.`orders` (`email`) USING BTREE"},
		{"biasa dengan prefix dan desc", domain.Index{Name: "ix_a", Method: "btree"}, []string{"`name`(20)", "`at` DESC"},
			"CREATE INDEX `ix_a` ON `shop`.`orders` (`name`(20), `at` DESC) USING BTREE"},
		{"hash", domain.Index{Name: "ix_h", Method: "hash"}, []string{"`k`"},
			"CREATE INDEX `ix_h` ON `shop`.`orders` (`k`) USING HASH"},
		{"fulltext", domain.Index{Name: "ft", Method: "fulltext"}, []string{"`body`"},
			"CREATE FULLTEXT INDEX `ft` ON `shop`.`orders` (`body`)"},
		{"nama berbacktick", domain.Index{Name: "a`b", Method: "btree"}, []string{"`c``d`"},
			"CREATE INDEX `a``b` ON `shop`.`orders` (`c``d`) USING BTREE"},
	}
	for _, tc := range cases {
		if got := indexDefinition("shop", "orders", tc.ix, tc.parts); got != tc.want {
			t.Errorf("%s:\n got  %s\n want %s", tc.name, got, tc.want)
		}
	}
}

func TestNewIndexPart(t *testing.T) {
	p := newIndexPart(2, ns("name"), sql.NullInt64{Int64: 10, Valid: true}, ns("D"), sql.NullString{})
	if p.text != "name" || p.quoted != "`name`(10) DESC" || p.seq != 2 {
		t.Errorf("kolom: %+v", p)
	}
	p = newIndexPart(1, sql.NullString{}, sql.NullInt64{}, ns("A"), ns("lower(`name`)"))
	if p.text != "lower(`name`)" || p.quoted != "(lower(`name`))" {
		t.Errorf("fungsional: %+v", p)
	}
}

func TestIsMariaDB(t *testing.T) {
	for v, want := range map[string]bool{
		"8.4.5": false,
		"8.0.36-0ubuntu0.22.04.1": false,
		"10.11.6-MariaDB-1:10.11": true,
		"5.5.5-10.6.12-mariadb": true,
	} {
		if got := isMariaDB(v); got != want {
			t.Errorf("isMariaDB(%q) = %v", v, got)
		}
	}
}

func TestSortSchemaIsDeterministic(t *testing.T) {
	mk := func(order []int) *domain.Schema {
		tables := []domain.Table{
			{Schema: "s", Name: "Zebra"}, {Schema: "s", Name: "apple"}, {Schema: "s", Name: "Apple"}, {Schema: "a", Name: "z"},
		}
		s := &domain.Schema{}
		for _, i := range order {
			t := tables[i]
			t.Columns = []domain.Column{{Name: "b", Position: 2}, {Name: "a", Position: 1}}
			t.Constraints = []domain.Constraint{{Name: "z"}, {Name: "PRIMARY"}, {Name: "a"}}
			t.Indexes = []domain.Index{{Name: "ix2"}, {Name: "PRIMARY"}, {Name: "ix1"}}
			s.Tables = append(s.Tables, t)
		}
		return s
	}
	a, b := mk([]int{0, 1, 2, 3}), mk([]int{3, 2, 1, 0})
	sortSchema(a)
	sortSchema(b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("urutan berbeda:\n%+v\n%+v", a, b)
	}
	var names []string
	for _, tb := range a.Tables {
		names = append(names, tb.Schema+"."+tb.Name)
	}
	if want := []string{"a.z", "s.Apple", "s.Zebra", "s.apple"}; !reflect.DeepEqual(names, want) {
		t.Errorf("tabel = %v, want %v (urut per byte)", names, want)
	}
	if a.Tables[0].Columns[0].Name != "a" || a.Tables[0].Constraints[0].Name != "PRIMARY" || a.Tables[0].Indexes[0].Name != "PRIMARY" {
		t.Errorf("kolom/constraint/index tidak terurut: %+v", a.Tables[0])
	}
}
