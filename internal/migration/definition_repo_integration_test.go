package migration_test

import (
	"context"
	"errors"
	"testing"

	"dbplatform/internal/domain"
	"dbplatform/internal/migration"
)

// draft yang dipakai di sini sudah rapi, seolah sudah lewat validasi
func batchInput() migration.DraftInput {
	return migration.DraftInput{
		Name:    "isi kolom total",
		UpSQL:   "UPDATE orders SET total = 1 WHERE id > $1 AND id <= $2",
		DownSQL: "UPDATE orders SET total = NULL WHERE id > $1 AND id <= $2",
		Mode:    domain.ModeBatch,
		Batch:   &domain.BatchConfig{Schema: "public", Table: "orders", KeyColumn: "id", BatchSize: 500},
	}
}

func normalInput() migration.DraftInput {
	return migration.DraftInput{Name: "buat tabel", UpSQL: "CREATE TABLE a (id int)", Mode: domain.ModeNormal}
}

func (e repoEnv) createDefinition(t *testing.T, in migration.DraftInput) migration.Definition {
	t.Helper()
	d, err := e.repo.CreateDefinition(context.Background(), e.conn, in, e.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDefinitionRepo_Integration_CreateAndRead(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	e := newRepoEnv(t)
	ctx := context.Background()

	first := e.createDefinition(t, normalInput())
	second := e.createDefinition(t, batchInput())

	if first.Version != 1 || second.Version != 2 {
		t.Errorf("versi = %d, %d", first.Version, second.Version)
	}
	if first.State != domain.StateDraft || first.Revision != 1 || first.Batch != nil || first.CreatedByName != "budi" {
		t.Errorf("definisi pertama = %+v", first)
	}
	if second.Mode != domain.ModeBatch || second.Batch == nil || *second.Batch != *batchInput().Batch || second.DownSQL == "" {
		t.Errorf("definisi batch = %+v", second)
	}

	list, err := e.repo.ListDefinitions(ctx, e.conn)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Version != 1 || list[0].UpSQL != "" || list[1].DownSQL != "" {
		t.Errorf("daftar harus urut dan tanpa SQL: %+v", list)
	}

	if _, err := e.repo.GetDefinition(ctx, e.conn, 99); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("versi tidak ada: %v", err)
	}
	if _, err := e.repo.CreateDefinition(ctx, 9999, normalInput(), e.user.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("koneksi tidak ada: %v", err)
	}
}

func TestDefinitionRepo_Integration_UpdateOnlyFreshDraft(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	e := newRepoEnv(t)
	ctx := context.Background()
	d := e.createDefinition(t, normalInput())

	changed := normalInput()
	changed.UpSQL = "CREATE TABLE b (id int)"
	updated, err := e.repo.UpdateDefinition(ctx, e.conn, d.Version, d.Revision, changed)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.UpSQL != changed.UpSQL || updated.Checksum == d.Checksum {
		t.Errorf("hasil update = %+v", updated)
	}

	// revisi lama berarti user ngedit salinan yang sudah basi
	if _, err := e.repo.UpdateDefinition(ctx, e.conn, d.Version, d.Revision, changed); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("revisi basi: %v", err)
	}
	if err := e.repo.MoveDefinition(ctx, updated, domain.StateReady); err != nil {
		t.Fatal(err)
	}
	if _, err := e.repo.UpdateDefinition(ctx, e.conn, d.Version, updated.Revision, changed); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("yang sudah beku tidak boleh diubah: %v", err)
	}
	if _, err := e.repo.UpdateDefinition(ctx, e.conn, 99, 1, changed); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("versi tidak ada: %v", err)
	}
}

func TestDefinitionRepo_Integration_MoveChecksCurrentState(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	e := newRepoEnv(t)
	ctx := context.Background()
	draft := e.createDefinition(t, normalInput())

	if err := e.repo.MoveDefinition(ctx, draft, domain.StateReady); err != nil {
		t.Fatal(err)
	}
	if err := e.repo.MoveDefinition(ctx, draft, domain.StateCancelled); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("status basi harus ditolak: %v", err)
	}

	ready, err := e.repo.GetDefinition(ctx, e.conn, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	if ready.State != domain.StateReady || ready.Revision != 1 {
		t.Errorf("setelah freeze = %+v", ready)
	}
	if err := e.repo.MoveDefinition(ctx, ready, domain.StateDraft); err != nil {
		t.Fatal(err)
	}
	reopened, err := e.repo.GetDefinition(ctx, e.conn, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.State != domain.StateDraft || reopened.Revision != 2 {
		t.Errorf("setelah dibuka lagi = %+v", reopened)
	}
}
