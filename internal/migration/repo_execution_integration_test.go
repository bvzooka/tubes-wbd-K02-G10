package migration_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"dbplatform/internal/domain"
	"dbplatform/internal/migration"
	"dbplatform/internal/testutil"
)

func microsNow() time.Time { return time.Now().Truncate(time.Microsecond) }

func TestRepo_Integration_ClaimMarksRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newRepoEnv(t)
	m := e.create(t, e.conn, "satu", "SELECT 1")
	now := microsNow()

	got, err := e.repo.Claim(ctx, e.conn, m.Version, e.user.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.MigrationRunning || got.ExecutedBy == nil || *got.ExecutedBy != e.user.ID || got.ExecutedByName != "budi" {
		t.Errorf("hasil claim = %+v", got)
	}
	if got.ExecutedAt == nil || !got.ExecutedAt.Equal(now) || got.FinishedAt != nil {
		t.Errorf("executed_at = %v, finished_at = %v, mau %v dan nil", got.ExecutedAt, got.FinishedAt, now)
	}
	if got.SQL != "SELECT 1" {
		t.Errorf("claim harus mengembalikan SQL untuk dijalankan, dapat %q", got.SQL)
	}
}

func TestRepo_Integration_ClaimOnlyDrafts(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newRepoEnv(t)

	for _, status := range []string{"running", "success", "failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			m := e.create(t, e.conn, "x", "SELECT 1")
			e.setStatus(t, e.conn, m.Version, status)

			if _, err := e.repo.Claim(ctx, e.conn, m.Version, e.user.ID, microsNow()); !errors.Is(err, domain.ErrMigrationNotDraft) {
				t.Errorf("err = %v, mau ErrMigrationNotDraft", err)
			}
			if got, _ := e.repo.Get(ctx, e.conn, m.Version); string(got.Status) != status {
				t.Errorf("status berubah menjadi %s", got.Status)
			}
			e.setStatus(t, e.conn, m.Version, "cancelled")
		})
	}
}

func TestRepo_Integration_ClaimUnknownAndCrossConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newRepoEnv(t)
	other := testutil.SeedConnection(t, e.pool, "lain", true)
	m := e.create(t, e.conn, "x", "SELECT 1")

	if _, err := e.repo.Claim(ctx, e.conn, 99, e.user.ID, microsNow()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("versi tak ada: err = %v", err)
	}
	if _, err := e.repo.Claim(ctx, other, m.Version, e.user.ID, microsNow()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("koneksi lain: err = %v, mau ErrNotFound (IDOR)", err)
	}
	if got, _ := e.repo.Get(ctx, e.conn, m.Version); got.Status != domain.MigrationDraft {
		t.Errorf("status = %s, harus tetap draft", got.Status)
	}
}

func TestRepo_Integration_ClaimEnforcesVersionOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newRepoEnv(t)
	e.create(t, e.conn, "satu", "SELECT 1")
	e.create(t, e.conn, "dua", "SELECT 2")
	e.create(t, e.conn, "tiga", "SELECT 3")

	_, err := e.repo.Claim(ctx, e.conn, 3, e.user.ID, microsNow())
	var ooo *migration.OutOfOrderError
	if !errors.As(err, &ooo) || ooo.Blocking != 1 || !errors.Is(err, domain.ErrMigrationOutOfOrder) {
		t.Fatalf("err = %v, mau OutOfOrderError yang menyebut versi 1 terlebih dulu", err)
	}
	if got, _ := e.repo.Get(ctx, e.conn, 3); got.Status != domain.MigrationDraft {
		t.Errorf("versi 3 harus tetap draft, dapat %s", got.Status)
	}

	if _, err := e.repo.Claim(ctx, e.conn, 1, e.user.ID, microsNow()); err != nil {
		t.Fatalf("versi terendah harus boleh: %v", err)
	}
	_, err = e.repo.Claim(ctx, e.conn, 2, e.user.ID, microsNow())
	if !errors.As(err, &ooo) || ooo.Blocking != 1 {
		t.Errorf("versi 1 masih running, versi 2 harus menunggu: err = %v", err)
	}

	if err := e.repo.Finish(ctx, e.conn, 1, domain.MigrationSuccess, "", "", microsNow()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.repo.Claim(ctx, e.conn, 2, e.user.ID, microsNow()); err != nil {
		t.Errorf("setelah versi 1 sukses, versi 2 harus boleh: %v", err)
	}
}

func TestRepo_Integration_ClaimIgnoresFailedAndCancelledEarlierVersions(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newRepoEnv(t)
	e.create(t, e.conn, "satu", "SELECT 1")
	e.create(t, e.conn, "dua", "SELECT 2")
	e.create(t, e.conn, "tiga", "SELECT 3")
	e.setStatus(t, e.conn, 1, "failed")
	e.setStatus(t, e.conn, 2, "cancelled")

	if _, err := e.repo.Claim(ctx, e.conn, 3, e.user.ID, microsNow()); err != nil {
		t.Errorf("versi lama yang failed atau cancelled tidak boleh menghalangi: %v", err)
	}
}

func TestRepo_Integration_ClaimOrderIsPerConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newRepoEnv(t)
	other := testutil.SeedConnection(t, e.pool, "lain", true)
	e.create(t, e.conn, "draft menggantung", "SELECT 1")
	m := e.create(t, other, "milik koneksi lain", "SELECT 2")

	if _, err := e.repo.Claim(ctx, other, m.Version, e.user.ID, microsNow()); err != nil {
		t.Errorf("draft di koneksi lain tidak boleh menghalangi: %v", err)
	}
}

func TestRepo_Integration_ConcurrentClaimsOfSameMigrationYieldOneWinner(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newRepoEnv(t)
	m := e.create(t, e.conn, "x", "SELECT 1")

	const workers = 8
	results := make([]error, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, results[i] = e.repo.Claim(ctx, e.conn, m.Version, e.user.ID, microsNow())
		}()
	}
	close(start)
	wg.Wait()

	var won, notDraft int
	for _, err := range results {
		switch {
		case err == nil:
			won++
		case errors.Is(err, domain.ErrMigrationNotDraft):
			notDraft++
		default:
			t.Fatalf("error tak terduga: %v", err)
		}
	}
	if won != 1 || notDraft != workers-1 {
		t.Errorf("menang = %d, ditolak = %d; mau 1 dan %d", won, notDraft, workers-1)
	}
}

func TestRepo_Integration_ConcurrentClaimsNeverLetLaterVersionJumpAhead(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newRepoEnv(t)
	e.create(t, e.conn, "satu", "SELECT 1")
	e.create(t, e.conn, "dua", "SELECT 2")

	for round := 0; round < 20; round++ {
		e.setStatus(t, e.conn, 1, "draft")
		e.setStatus(t, e.conn, 2, "draft")

		start := make(chan struct{})
		var wg sync.WaitGroup
		var err1, err2 error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, err1 = e.repo.Claim(ctx, e.conn, 1, e.user.ID, microsNow())
		}()
		go func() {
			defer wg.Done()
			<-start
			_, err2 = e.repo.Claim(ctx, e.conn, 2, e.user.ID, microsNow())
		}()
		close(start)
		wg.Wait()

		var ooo *migration.OutOfOrderError
		if !errors.As(err2, &ooo) {
			t.Fatalf("putaran %d: versi 2 harus selalu ditolak selama versi 1 belum selesai, err = %v", round, err2)
		}
		if err1 != nil {
			t.Fatalf("putaran %d: versi 1 harus berhasil: %v", round, err1)
		}
	}
}

func TestRepo_Integration_FinishRecordsOutcome(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newRepoEnv(t)

	ok := e.create(t, e.conn, "sukses", "SELECT 1")
	if _, err := e.repo.Claim(ctx, e.conn, ok.Version, e.user.ID, microsNow()); err != nil {
		t.Fatal(err)
	}
	done := microsNow()
	if err := e.repo.Finish(ctx, e.conn, ok.Version, domain.MigrationSuccess, "", "peringatan DDL", done); err != nil {
		t.Fatal(err)
	}
	got, _ := e.repo.Get(ctx, e.conn, ok.Version)
	if got.Status != domain.MigrationSuccess || got.Warning != "peringatan DDL" || got.ErrorMessage != "" {
		t.Errorf("sukses = %+v", got)
	}
	if got.FinishedAt == nil || !got.FinishedAt.Equal(done) {
		t.Errorf("finished_at = %v, mau %v", got.FinishedAt, done)
	}

	bad := e.create(t, e.conn, "gagal", "SELECT 2")
	if _, err := e.repo.Claim(ctx, e.conn, bad.Version, e.user.ID, microsNow()); err != nil {
		t.Fatal(err)
	}
	if err := e.repo.Finish(ctx, e.conn, bad.Version, domain.MigrationFailed, "relation \"x\" does not exist", "", microsNow()); err != nil {
		t.Fatal(err)
	}
	got, _ = e.repo.Get(ctx, e.conn, bad.Version)
	if got.Status != domain.MigrationFailed || got.ErrorMessage != "relation \"x\" does not exist" {
		t.Errorf("gagal = %+v", got)
	}
}

func TestRepo_Integration_FinishOnlyAffectsRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newRepoEnv(t)
	other := testutil.SeedConnection(t, e.pool, "lain", true)
	m := e.create(t, e.conn, "x", "SELECT 1")

	if err := e.repo.Finish(ctx, e.conn, m.Version, domain.MigrationSuccess, "", "", microsNow()); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("draft belum running: err = %v, mau ErrConflict", err)
	}
	if _, err := e.repo.Claim(ctx, e.conn, m.Version, e.user.ID, microsNow()); err != nil {
		t.Fatal(err)
	}
	if err := e.repo.Finish(ctx, other, m.Version, domain.MigrationSuccess, "", "", microsNow()); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("koneksi lain: err = %v, mau ErrConflict", err)
	}
	if err := e.repo.Finish(ctx, e.conn, m.Version, domain.MigrationSuccess, "", "", microsNow()); err != nil {
		t.Fatal(err)
	}
	if err := e.repo.Finish(ctx, e.conn, m.Version, domain.MigrationFailed, "dua kali", "", microsNow()); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("selesai dua kali: err = %v, mau ErrConflict", err)
	}
	if got, _ := e.repo.Get(ctx, e.conn, m.Version); got.Status != domain.MigrationSuccess || got.ErrorMessage != "" {
		t.Errorf("hasil pertama tidak boleh tertimpa: %+v", got)
	}
}

func TestRepo_Integration_FailStuck(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	e := newRepoEnv(t)
	other := testutil.SeedConnection(t, e.pool, "lain", true)

	old := e.create(t, e.conn, "terputus", "SELECT 1")
	fresh := e.create(t, other, "baru berjalan", "SELECT 2")
	draft := e.create(t, other, "draft", "SELECT 3")
	for _, c := range []struct {
		conn    int64
		version int
		age     string
	}{{e.conn, old.Version, "10 minutes"}, {other, fresh.Version, "1 minute"}} {
		_, err := e.pool.Exec(ctx, `
			UPDATE db_migrations SET status = 'running', executed_at = now() - $3::interval
			WHERE connection_id = $1 AND version = $2`, c.conn, c.version, c.age)
		if err != nil {
			t.Fatal(err)
		}
	}

	finished := microsNow()
	n, err := e.repo.FailStuck(ctx, time.Now().Add(-5*time.Minute), "terputus, periksa manual", finished)
	if err != nil || n != 1 {
		t.Fatalf("FailStuck = %d, %v; mau 1", n, err)
	}

	got, _ := e.repo.Get(ctx, e.conn, old.Version)
	if got.Status != domain.MigrationFailed || got.ErrorMessage != "terputus, periksa manual" || got.FinishedAt == nil || !got.FinishedAt.Equal(finished) {
		t.Errorf("yang terputus = %+v", got)
	}
	if got, _ := e.repo.Get(ctx, other, fresh.Version); got.Status != domain.MigrationRunning {
		t.Errorf("yang baru berjalan tidak boleh disentuh: %s", got.Status)
	}
	if got, _ := e.repo.Get(ctx, other, draft.Version); got.Status != domain.MigrationDraft {
		t.Errorf("draft tidak boleh disentuh: %s", got.Status)
	}

	if n, err := e.repo.FailStuck(ctx, time.Now().Add(-5*time.Minute), "x", microsNow()); err != nil || n != 0 {
		t.Errorf("panggilan kedua: %d, %v; mau 0", n, err)
	}
}
