package migration

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"dbplatform/internal/clock"
	"dbplatform/internal/contract"
	"dbplatform/internal/domain"
	"dbplatform/internal/telemetry"
)

const (
	// jeda sebelum mencoba lagi kalau database target sedang dipakai
	busyDelay          = 5 * time.Second
	defaultExecTimeout = 30 * time.Minute
)

type runnerStore interface {
	GetExecution(ctx context.Context, id int64) (domain.Execution, error)
	MarkRunning(ctx context.Context, job domain.Job, executionID int64, at time.Time) error
	RecordAttempt(ctx context.Context, job domain.Job, e domain.Execution, a Attempt) error
	ChainIDs(ctx context.Context, rootID int64) ([]int64, error)
	SaveProgress(ctx context.Context, job domain.Job, executionID int64, p Progress) error
}

type RunnerDeps struct {
	Store       runnerStore
	Revalidator *Revalidator
	Drivers     contract.DriverProvider
	Clock       clock.Clock
	Logger      *slog.Logger
	Timeout     time.Duration
}

// Runner menjalankan satu job dan mencatat hasilnya, kapan diulang diputuskan oleh worker
type Runner struct{ deps RunnerDeps }

func NewRunner(deps RunnerDeps) *Runner {
	if deps.Timeout <= 0 {
		deps.Timeout = defaultExecTimeout
	}
	return &Runner{deps: deps}
}

// Run memakai context lease untuk database target, sedangkan penulisan ke database platform tidak ikut dibatalkan
func (r *Runner) Run(ctx context.Context, job domain.Job, lease contract.Lease) domain.RunResult {
	platform := context.WithoutCancel(ctx)
	e, err := r.deps.Store.GetExecution(platform, job.ExecutionID)
	if err != nil {
		return later(err)
	}
	switch e.Status {
	case domain.ExecQueued:
	case domain.ExecRunning:
		// attempt sebelumnya terputus sebelum hasilnya tercatat, jadi buktinya dicek dulu
		return r.Reconcile(ctx, job)
	default:
		// pesan job terkirim dua kali, hasilnya sudah ada dan tidak boleh dijalankan lagi
		return settled(e)
	}

	a := Attempt{StartedAt: r.deps.Clock.Now()}
	driver, failure := r.deps.Revalidator.Check(targetContext(lease.Context(), job, e), e)
	if failure != nil {
		return r.fail(platform, job, e, a, failure)
	}
	if e.Mode == domain.ModeBatch {
		return r.runBatch(lease, job, e, driver, a)
	}
	return r.runNormal(lease, job, e, driver, a)
}

// Reconcile dipanggil saat worker mati di tengah job, hasilnya ditentukan dari bukti di database target
func (r *Runner) Reconcile(ctx context.Context, job domain.Job) domain.RunResult {
	platform := context.WithoutCancel(ctx)
	e, err := r.deps.Store.GetExecution(platform, job.ExecutionID)
	if err != nil {
		return later(err)
	}
	switch e.Status {
	case domain.ExecRunning:
	case domain.ExecQueued:
		// belum sempat menyentuh target, aman diantrekan lagi
		return later(nil)
	default:
		return settled(e)
	}
	a := Attempt{StartedAt: r.deps.Clock.Now(), SQLExecuted: e.SQLSnapshot}
	if e.StartedAt != nil {
		a.StartedAt = *e.StartedAt
	}
	cause := errors.New("worker terputus saat menjalankan migration")
	if e.Mode == domain.ModeBatch {
		return r.verifyBatch(ctx, job, e, a, cause, true)
	}
	return r.verify(ctx, job, e, a, cause, true)
}

// finish mencatat attempt ke riwayat dan log aplikasi
func (r *Runner) finish(ctx context.Context, job domain.Job, e domain.Execution, a Attempt, retryable bool) domain.RunResult {
	a.Number = job.Attempt
	a.FinishedAt = r.deps.Clock.Now()
	a.WillRetry = retryable && job.Attempt < job.MaxAttempts

	// query disamarkan oleh logger kalau memuat password
	r.deps.Logger.InfoContext(ctx, "migration attempt finished",
		"event", "migration.attempt", "actor", e.RequestedByName, "connection_id", e.ConnectionID,
		"migration_id", e.MigrationID, "execution_id", e.ID, "job_id", job.ID, "version", e.Version,
		"revision", e.Revision, "kind", e.Kind, "attempt", a.Number, "status", a.Outcome, "committed", a.Committed,
		"failure_code", a.FailureCode, "error", a.FailureMessage, "query", a.SQLExecuted)

	if err := r.deps.Store.RecordAttempt(ctx, job, e, a); err != nil {
		r.deps.Logger.ErrorContext(ctx, "record attempt failed",
			"event", "migration.record_failed", "execution_id", e.ID, "error", err)
		// status masih running, jadi giliran berikutnya ngecek dulu buktinya di database target
		return later(err)
	}
	result := domain.RunResult{Outcome: a.Outcome, Retryable: retryable}
	if a.FailureMessage != "" {
		result.Err = errors.New(a.FailureMessage)
	}
	return result
}

// fail untuk attempt yang gagal tanpa perubahan apa pun di database target
func (r *Runner) fail(ctx context.Context, job domain.Job, e domain.Execution, a Attempt, f *Failure) domain.RunResult {
	a.Outcome, a.FailureCode, a.FailureMessage = domain.OutcomeFailed, f.Code, f.Error()
	return r.finish(ctx, job, e, a, f.Retryable)
}

// later mengembalikan job ke antrean tanpa dihitung sebagai percobaan
// targetContext menandai semua log SQL ke database target dengan identitas pekerjaannya
func targetContext(ctx context.Context, job domain.Job, e domain.Execution) context.Context {
	return telemetry.WithLogAttrs(ctx,
		"migration_id", e.MigrationID, "execution_id", e.ID, "job_id", job.ID,
		"attempt", job.Attempt, "requested_by", e.RequestedByName)
}

func later(err error) domain.RunResult {
	return domain.RunResult{Outcome: domain.OutcomeRequeue, RetryAfter: busyDelay, Err: err}
}

func settled(e domain.Execution) domain.RunResult {
	switch e.Status {
	case domain.ExecSuccess:
		return domain.RunResult{Outcome: domain.OutcomeSuccess}
	case domain.ExecOutcomeUnknown:
		return domain.RunResult{Outcome: domain.OutcomeUnknown}
	}
	return domain.RunResult{Outcome: domain.OutcomeFailed}
}
