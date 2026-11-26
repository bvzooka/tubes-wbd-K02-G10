package migration

import (
	"context"
	"errors"
	"fmt"

	"dbplatform/internal/appdb"
	"dbplatform/internal/contract"
	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
)

// kode kegagalan yang disimpan di riwayat
const (
	CodeRevalidation  = "revalidation_failed"
	CodeApprovalStale = "approval_stale"
	CodeConnection    = "connection_failed"
	CodePlatform      = "platform_error"
)

// Failure adalah alasan sebuah attempt tidak jadi dijalankan
type Failure struct {
	Code      string
	Retryable bool // true hanya untuk gangguan sementara yang pasti belum menyentuh database target
	Err       error
}

func (f *Failure) Error() string { return f.Err.Error() }
func (f *Failure) Unwrap() error { return f.Err }

// rejected untuk syarat yang tidak terpenuhi, diulang berapa kali pun hasilnya sama
func rejected(code, format string, args ...any) *Failure {
	return &Failure{Code: code, Err: fmt.Errorf(format, args...)}
}

// transient untuk gangguan yang bisa hilang sendiri, jadi boleh dicoba lagi
func transient(code string, err error) *Failure {
	return &Failure{Code: code, Retryable: true, Err: err}
}

type revalidationStore interface {
	GetDefinition(ctx context.Context, connID int64, version int) (Definition, error)
	VersionStates(ctx context.Context, q appdb.DBTX, connID int64) ([]versionState, error)
}

// Revalidator memeriksa ulang semua syarat tepat sebelum eksekusi, karena keadaan bisa berubah selama permintaan menunggu
type Revalidator struct {
	db        appdb.DBTX
	store     revalidationStore
	authz     contract.Authorizer
	approvals contract.Approvals
	drivers   contract.DriverProvider
}

func NewRevalidator(
	db appdb.DBTX, store revalidationStore, authz contract.Authorizer,
	approvals contract.Approvals, drivers contract.DriverProvider,
) *Revalidator {
	return &Revalidator{db: db, store: store, authz: authz, approvals: approvals, drivers: drivers}
}

// Check mengembalikan driver database target kalau semua syarat masih terpenuhi, tanpa menjalankan SQL apa pun di sana
func (v *Revalidator) Check(ctx context.Context, e domain.Execution) (dbdriver.Driver, *Failure) {
	if e.Status != domain.ExecQueued {
		return nil, rejected(CodeRevalidation, "permintaan berstatus %s, bukan queued", e.Status)
	}
	if f := v.checkRequester(ctx, e); f != nil {
		return nil, f
	}
	driver, f := v.checkTarget(ctx, e.ConnectionID)
	if f != nil {
		return nil, f
	}
	if f := v.checkApprovals(ctx, e); f != nil {
		return nil, f
	}
	if f := v.checkDefinition(ctx, e); f != nil {
		return nil, f
	}
	return driver, nil
}

// checkRequester memakai Authorizer yang sudah menganggap user atau koneksi nonaktif tidak punya akses
func (v *Revalidator) checkRequester(ctx context.Context, e domain.Execution) *Failure {
	ok, err := v.authz.Can(ctx, e.RequestedBy, e.ConnectionID, domain.AccessViewEdit)
	if err != nil {
		return transient(CodePlatform, err)
	}
	if !ok {
		return rejected(CodeRevalidation, "pengaju atau koneksinya nonaktif, atau pengaju tidak lagi punya akses view_edit")
	}
	return nil
}

func (v *Revalidator) checkTarget(ctx context.Context, connID int64) (dbdriver.Driver, *Failure) {
	driver, err := v.drivers.Get(ctx, connID)
	if errors.Is(err, domain.ErrConnectionInactive) || errors.Is(err, domain.ErrNotFound) {
		return nil, rejected(CodeRevalidation, "koneksi tidak bisa dipakai: %w", err)
	}
	if err != nil {
		return nil, transient(CodeConnection, err)
	}
	if driver.Dialect() != domain.DBMSPostgres {
		return nil, rejected(CodeRevalidation, "migration hanya didukung untuk PostgreSQL")
	}
	if err := driver.Ping(ctx); err != nil {
		return nil, transient(CodeConnection, err)
	}
	return driver, nil
}

// checkApprovals menolak kalau ada group yang sekarang punya akses tapi belum pernah menyetujui
func (v *Revalidator) checkApprovals(ctx context.Context, e domain.Execution) *Failure {
	given, err := v.approvals.List(ctx, v.db, e.ID)
	if err != nil {
		return transient(CodePlatform, err)
	}
	if len(given) == 0 {
		return rejected(CodeApprovalStale, "tidak ada persetujuan yang tercatat")
	}
	approved := map[int64]bool{}
	for _, a := range given {
		if a.Status != domain.ApprovalApproved {
			return rejected(CodeApprovalStale, "group %s belum menyetujui", a.GroupName)
		}
		approved[a.GroupID] = true
	}
	required, err := v.approvals.RequiredNow(ctx, v.db, e.ConnectionID)
	if err != nil {
		return transient(CodePlatform, err)
	}
	for _, g := range required {
		if !approved[g.ID] {
			return rejected(CodeApprovalStale, "group %s baru mendapat akses dan belum menyetujui", g.Name)
		}
	}
	return nil
}

// checkDefinition memastikan yang akan dijalankan masih sama dengan yang disetujui dan urutannya masih sah
func (v *Revalidator) checkDefinition(ctx context.Context, e domain.Execution) *Failure {
	d, err := v.store.GetDefinition(ctx, e.ConnectionID, e.Version)
	if err != nil {
		return transient(CodePlatform, err)
	}
	wantSQL := d.UpSQL
	if e.Kind == domain.KindRollback {
		wantSQL = d.DownSQL
	}
	// checksum dihitung ulang dari isi supaya perubahan langsung di tabel juga ketahuan
	if d.ID != e.MigrationID || e.SQLSnapshot != wantSQL || draftChecksum(d.DraftInput) != e.Checksum {
		return rejected(CodeRevalidation, "isi migration berbeda dengan yang disetujui")
	}

	others, err := v.store.VersionStates(ctx, v.db, e.ConnectionID)
	if err != nil {
		return transient(CodePlatform, err)
	}
	err = checkOperation(operation{
		Kind: e.Kind, Resume: e.ResumesExecutionID != nil,
		Target: versionState{Version: d.Version, State: d.State}, Others: others,
		Irreversible: d.Irreversible, HasDown: d.DownSQL != "",
	})
	if err != nil {
		return rejected(CodeRevalidation, "%w", err)
	}

	clean, fields := cleanDraft(d.DraftInput)
	if len(fields) == 0 {
		fields = checkReady(clean)
	}
	if len(fields) > 0 {
		return rejected(CodeRevalidation, "SQL tidak lagi lolos validasi: %v", fields)
	}
	return nil
}
