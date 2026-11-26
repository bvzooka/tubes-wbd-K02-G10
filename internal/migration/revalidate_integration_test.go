package migration_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/access"
	"dbplatform/internal/dbdriver/postgres"
	"dbplatform/internal/domain"
	"dbplatform/internal/migration"
	"dbplatform/internal/testutil"
)

type runEnv struct {
	flowEnv
	target  *pgxpool.Pool
	drivers *testutil.FakeDriverProvider
}

// database target memakai driver PostgreSQL asli
func newRunEnv(t *testing.T) runEnv {
	t.Helper()
	f := newFlowEnv(t)
	target := testutil.NewTargetDB(t)
	return runEnv{
		flowEnv: f,
		target:  target,
		drivers: testutil.NewFakeDriverProvider().Set(f.conn, postgres.NewFromPool(target)),
	}
}

func (r runEnv) revalidator() *migration.Revalidator {
	return migration.NewRevalidator(r.pool, r.repo, access.New(r.pool), r.approvals, r.drivers)
}

// approvedAndQueued melewati alur asli: freeze, ajukan, disetujui, jalankan
func (r runEnv) approvedAndQueued(t *testing.T) domain.Execution {
	t.Helper()
	ctx := context.Background()
	ex, err := r.apply(r.ready(t))
	if err != nil {
		t.Fatal(err)
	}
	r.approveByViewer(t, ex.ID)
	if _, err := r.executions.Run(ctx, r.editor, ex.ID); err != nil {
		t.Fatal(err)
	}
	got, err := r.repo.GetExecution(ctx, ex.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// code kosong berarti revalidasi harus lolos
func TestRevalidator_Integration_Check(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	tests := []struct {
		name      string
		change    func(*testing.T, runEnv, *domain.Execution)
		code      string
		retryable bool
	}{
		{"semua masih sah", func(*testing.T, runEnv, *domain.Execution) {}, "", false},
		{"belum antre", func(_ *testing.T, _ runEnv, e *domain.Execution) {
			e.Status = domain.ExecApproved
		}, migration.CodeRevalidation, false},
		{"akses pengaju dicabut", func(t *testing.T, r runEnv, _ *domain.Execution) {
			r.exec(t, `DELETE FROM group_members WHERE user_id = (SELECT id FROM users WHERE username = 'budi')`)
		}, migration.CodeRevalidation, false},
		{"group baru mendapat akses", func(t *testing.T, r runEnv, _ *domain.Execution) {
			testutil.Grant(t, r.pool, testutil.SeedGroup(t, r.pool, "baru"), r.conn, domain.AccessViewOnly)
		}, migration.CodeApprovalStale, false},
		{"sql diubah langsung di tabel", func(t *testing.T, r runEnv, _ *domain.Execution) {
			r.exec(t, `UPDATE db_migrations SET sql_text = 'DROP TABLE users'`)
		}, migration.CodeRevalidation, false},
		{"versi lebih tinggi sudah aktif", func(t *testing.T, r runEnv, _ *domain.Execution) {
			other := r.createDefinition(t, normalInput())
			if err := r.repo.MoveDefinition(context.Background(), other, domain.StateApplied); err != nil {
				t.Fatal(err)
			}
		}, migration.CodeRevalidation, false},
		{"database target mati", func(_ *testing.T, r runEnv, _ *domain.Execution) {
			r.target.Close()
		}, migration.CodeConnection, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRunEnv(t)
			e := r.approvedAndQueued(t)
			tt.change(t, r, &e)

			driver, failure := r.revalidator().Check(context.Background(), e)

			if tt.code == "" {
				if failure != nil || driver == nil {
					t.Fatalf("harusnya lolos: %v", failure)
				}
				return
			}
			if failure == nil || failure.Code != tt.code || failure.Retryable != tt.retryable {
				t.Errorf("failure = %+v, mau %s retry %v", failure, tt.code, tt.retryable)
			}
		})
	}
}
