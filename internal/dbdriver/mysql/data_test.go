package mysql

import (
	"errors"
	"testing"
	gomysql "github.com/go-sql-driver/mysql"
	"dbplatform/internal/domain"
)

func TestSelectSQL(t *testing.T) {
	cases := []struct {
		name string
		ref domain.TableRef
		pk []string
		want string
	}{
		{"tanpa pk", domain.TableRef{Schema: "shop", Table: "v_sales"}, nil,
			"SELECT * FROM `shop`.`v_sales` LIMIT ? OFFSET ?"},
		{"pk tunggal", domain.TableRef{Schema: "shop", Table: "orders"}, []string{"id"},
			"SELECT * FROM `shop`.`orders` ORDER BY `id` LIMIT ? OFFSET ?"},
		{"pk komposit berurutan", domain.TableRef{Schema: "shop", Table: "items"}, []string{"order_id", "sku"},
			"SELECT * FROM `shop`.`items` ORDER BY `order_id`, `sku` LIMIT ? OFFSET ?"},
		{"nama berspasi dan berbacktick", domain.TableRef{Schema: "my db", Table: "we`ird name"}, []string{"Some`Id"},
			"SELECT * FROM `my db`.`we``ird name` ORDER BY `Some``Id` LIMIT ? OFFSET ?"},
		{"upaya injeksi hanya menjadi identifier", domain.TableRef{Schema: "s", Table: "x`; DROP TABLE y;--"}, nil,
			"SELECT * FROM `s`.`x``; DROP TABLE y;--` LIMIT ? OFFSET ?"},
	}
	for _, tc := range cases {
		if got := selectSQL(tc.ref, tc.pk); got != tc.want {
			t.Errorf("%s:\n got  %s\n want %s", tc.name, got, tc.want)
		}
	}
}

func TestNotFoundOnMissing(t *testing.T) {
	if got := notFoundOnMissing(&gomysql.MySQLError{Number: erNoSuchTable}); !errors.Is(got, domain.ErrNotFound) {
		t.Errorf("1146 harus menjadi ErrNotFound, got %v", got)
	}
	other := &gomysql.MySQLError{Number: 1064}
	if got := notFoundOnMissing(other); got != error(other) {
		t.Errorf("error lain harus utuh, got %v", got)
	}
}

func TestUnwrapNotFound(t *testing.T) {
	wrapped := errors.Join(errors.New("tx"), domain.ErrNotFound)
	if got := unwrapNotFound(wrapped); got != domain.ErrNotFound {
		t.Errorf("got %v", got)
	}
	boom := errors.New("boom")
	if got := unwrapNotFound(boom); got != boom {
		t.Errorf("got %v", got)
	}
}
