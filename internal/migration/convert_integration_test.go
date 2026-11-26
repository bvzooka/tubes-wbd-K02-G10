package migration_test

import (
	"context"
	"os"
	"reflect"
	"testing"

	"dbplatform/internal/domain"
	"dbplatform/internal/migration"
)

// database test sudah menjalankan konversi saat masih kosong, jadi file yang sama dijalankan lagi setelah data M1 diisi
func (f flowEnv) convert(t *testing.T) {
	t.Helper()
	sql, err := os.ReadFile("../appdb/sql/202611270100_convert_m1_migrations.sql")
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, string(sql))
}

func TestConvertM1_Integration_StatesAndHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	f := newFlowEnv(t)
	ctx := context.Background()

	// lima migration gaya M1, hanya kolom status yang terisi
	statuses := []string{"success", "failed", "cancelled", "success", "draft"}
	for i, status := range statuses {
		m := f.create(t, f.conn, "m1", "SELECT 1")
		f.setStatus(t, f.conn, m.Version, status)
		if status == "success" || status == "failed" {
			_, err := f.pool.Exec(ctx, `
				UPDATE db_migrations SET executed_by = $2, executed_at = now(), finished_at = now(), error_message = $3
				WHERE id = $1`, m.ID, f.user.ID, map[bool]string{true: "salah sintaks"}[statuses[i] == "failed"])
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	f.convert(t)
	// dijalankan dua kali tidak boleh menggandakan apa pun
	f.convert(t)

	want := []domain.MigrationState{
		domain.StateApplied, domain.StateSuperseded, domain.StateCancelled, domain.StateApplied, domain.StateDraft,
	}
	list, err := f.repo.ListDefinitions(ctx, f.conn)
	if err != nil {
		t.Fatal(err)
	}
	var got []domain.MigrationState
	for _, d := range list {
		got = append(got, d.State)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("status = %v, mau %v", got, want)
	}
	if !list[0].Irreversible || list[0].IrreversibleReason == "" || list[1].Irreversible {
		t.Errorf("irreversible = %+v lalu %+v", list[0], list[1])
	}

	entries, _, err := f.executions.History(ctx, f.viewer, f.conn, migration.HistoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("riwayat = %d baris, mau 3", len(entries))
	}
	if entries[0].Version != 1 || entries[0].Status != "success" || !entries[0].Committed || entries[0].SQLExecuted != "SELECT 1" {
		t.Errorf("riwayat pertama = %+v", entries[0])
	}
	if entries[1].Version != 2 || entries[1].Status != "failed" || entries[1].Committed || entries[1].FailureMessage != "salah sintaks" {
		t.Errorf("riwayat kedua = %+v", entries[1])
	}

	_, err = f.executions.Request(ctx, f.editor, f.conn, 4, domain.KindRollback, false)
	if err == nil {
		t.Error("migration M1 tidak boleh bisa di-rollback")
	}
}
