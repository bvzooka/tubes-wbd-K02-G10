package access_test

import (
	"context"
	"testing"

	"dbplatform/internal/access"
	"dbplatform/internal/auth/authtest"
	"dbplatform/internal/domain"
)

func TestAuthorizer_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := authtest.NewAppDB(t)
	ctx := context.Background()
	authz := access.New(pool)

	multi := authtest.SeedUser(t, pool, "multi", "x", false)
	viewer := authtest.SeedUser(t, pool, "viewer", "x", false)
	loner := authtest.SeedUser(t, pool, "loner", "x", false)
	admin := authtest.SeedUser(t, pool, "admin", "x", true)
	inactive := authtest.SeedUser(t, pool, "inactive", "x", false)

	sales := authtest.SeedConnection(t, pool, "Sales", true)
	hr := authtest.SeedConnection(t, pool, "hr", true)
	archived := authtest.SeedConnection(t, pool, "archived", false)

	readers := authtest.SeedGroup(t, pool, "readers", multi.ID, viewer.ID, inactive.ID)
	editors := authtest.SeedGroup(t, pool, "editors", multi.ID)
	authtest.Grant(t, pool, readers, sales, domain.AccessViewOnly)
	authtest.Grant(t, pool, readers, hr, domain.AccessViewOnly)
	authtest.Grant(t, pool, readers, archived, domain.AccessViewEdit)
	authtest.Grant(t, pool, editors, sales, domain.AccessViewEdit)
	authtest.SetUserActive(t, pool, inactive.ID, false)

	tests := []struct {
		name string
		user int64
		conn int64
		want domain.AccessLevel
	}{
		{"banyak group: tertinggi menang", multi.ID, sales, domain.AccessViewEdit},
		{"satu group view only", viewer.ID, sales, domain.AccessViewOnly},
		{"group lain pada database lain", multi.ID, hr, domain.AccessViewOnly},
		{"tanpa group", loner.ID, sales, domain.AccessNone},
		{"koneksi nonaktif", multi.ID, archived, domain.AccessNone},
		{"user nonaktif", inactive.ID, sales, domain.AccessNone},
		{"admin tanpa group", admin.ID, sales, domain.AccessNone},
		{"koneksi tidak ada", multi.ID, 9999, domain.AccessNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := authz.EffectiveLevel(ctx, tc.user, tc.conn)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("EffectiveLevel = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("Can", func(t *testing.T) {
		cases := []struct {
			user, conn int64
			min        domain.AccessLevel
			want       bool
		}{
			{viewer.ID, sales, domain.AccessViewOnly, true},
			{viewer.ID, sales, domain.AccessViewEdit, false},
			{multi.ID, sales, domain.AccessViewEdit, true},
			{admin.ID, sales, domain.AccessViewOnly, false},
		}
		for _, c := range cases {
			got, err := authz.Can(ctx, c.user, c.conn, c.min)
			if err != nil || got != c.want {
				t.Errorf("Can(%d,%d,%q) = %v, %v; want %v", c.user, c.conn, c.min, got, err, c.want)
			}
		}
	})

	t.Run("ListAccessible", func(t *testing.T) {
		got, err := authz.ListAccessible(ctx, multi.ID)
		if err != nil {
			t.Fatal(err)
		}
		want := []domain.AccessibleDatabase{
			{ConnectionID: hr, Name: "hr", DBMS: domain.DBMSPostgres, Level: domain.AccessViewOnly},
			{ConnectionID: sales, Name: "Sales", DBMS: domain.DBMSPostgres, Level: domain.AccessViewEdit},
		}
		if len(got) != len(want) {
			t.Fatalf("got %+v", got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("[%d] = %+v, want %+v", i, got[i], want[i])
			}
		}
		for _, u := range []int64{loner.ID, admin.ID, inactive.ID} {
			got, err := authz.ListAccessible(ctx, u)
			if err != nil || len(got) != 0 {
				t.Errorf("user %d: %+v %v", u, got, err)
			}
		}
	})

	t.Run("perubahan grant langsung berlaku", func(t *testing.T) {
		authtest.Grant(t, pool, readers, sales, domain.AccessViewEdit)
		got, _ := authz.EffectiveLevel(ctx, viewer.ID, sales)
		if got != domain.AccessViewEdit {
			t.Fatalf("got %q", got)
		}
	})
}
