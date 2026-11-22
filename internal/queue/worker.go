package queue

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"dbplatform/internal/clock"
	"dbplatform/internal/contract"
	"dbplatform/internal/domain"
	"dbplatform/internal/telemetry"
)

type Config struct {
	ID            string
	Concurrency   int
	PollInterval  time.Duration
	Lease         time.Duration
	Heartbeat     time.Duration
	RetryBase     time.Duration
	ReapInterval  time.Duration // default 10s
	ShutdownGrace time.Duration // default 30s
}

// Worker mengambil job dari queue, memegang lease-nya, memanggil runner, lalu memutuskan
// job selesai, dijadwalkan ulang, atau di-retry berdasarkan RunResult.
type Worker struct {
	repo   *Repo
	runner contract.ExecutionRunner
	cfg    Config
	clock  clock.Clock
	logger *slog.Logger
	tracer trace.Tracer
}

func NewWorker(repo *Repo, runner contract.ExecutionRunner, cfg Config, clk clock.Clock, logger *slog.Logger) *Worker {
	if cfg.ReapInterval == 0 {
		cfg.ReapInterval = 10 * time.Second
	}
	if cfg.ShutdownGrace == 0 {
		cfg.ShutdownGrace = 30 * time.Second
	}
	return &Worker{repo: repo, runner: runner, cfg: cfg, clock: clk, logger: logger, tracer: otel.Tracer("dbplatform/queue")}
}

// Run berjalan sampai ctx dibatalkan. Setelah itu worker berhenti mengambil job baru dan
// memberi job yang sedang berjalan waktu ShutdownGrace untuk selesai.
func (w *Worker) Run(ctx context.Context) {
	// Job berjalan memakai context sendiri agar tidak langsung terputus saat SIGTERM.
	jobCtx, cancelJobs := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelJobs()

	var running sync.WaitGroup
	slots := make(chan struct{}, w.cfg.Concurrency)

	running.Go(func() { w.reapLoop(ctx, jobCtx) })

	for ctx.Err() == nil {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			continue
		}
		job, ok, err := w.repo.Claim(ctx, w.cfg.ID, w.cfg.Lease, w.clock.Now())
		if err != nil && ctx.Err() == nil {
			w.logger.ErrorContext(ctx, "claim failed", "event", "job.claim", "error", err)
		}
		if !ok {
			<-slots
			sleep(ctx, w.cfg.PollInterval)
			continue
		}
		running.Go(func() {
			defer func() { <-slots }()
			w.process(jobCtx, job, w.runner.Run)
		})
	}

	done := make(chan struct{})
	go func() { running.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(w.cfg.ShutdownGrace):
		w.logger.Warn("shutdown grace exceeded, cancelling running jobs", "event", "worker.shutdown")
		cancelJobs()
		<-done
	}
}

func (w *Worker) reapLoop(ctx, jobCtx context.Context) {
	for {
		sleep(ctx, w.cfg.ReapInterval)
		if ctx.Err() != nil {
			return
		}
		w.ReapOnce(jobCtx)
	}
}

// ReapOnce mengambil alih job running yang lease-nya habis (worker-nya mati) lalu
// meminta runner merekonsiliasi hasilnya dari bukti di database target.
func (w *Worker) ReapOnce(ctx context.Context) {
	stale, err := w.repo.Expired(ctx, w.clock.Now())
	if err != nil {
		w.logger.ErrorContext(ctx, "list expired jobs failed", "event", "recovery.scan", "error", err)
		return
	}
	for _, s := range stale {
		job, ok, err := w.repo.TakeOver(ctx, s, w.cfg.ID, w.cfg.Lease, w.clock.Now())
		if err != nil || !ok {
			continue
		}
		w.process(ctx, job, func(ctx context.Context, job domain.Job, _ contract.Lease) domain.RunResult {
			res := w.runner.Reconcile(ctx, job)
			w.logger.InfoContext(ctx, "job reconciled", "event", "recovery.reconciled",
				"job_id", job.ID, "execution_id", job.ExecutionID, "outcome", res.Outcome)
			return res
		})
	}
}

type runFunc func(ctx context.Context, job domain.Job, lease contract.Lease) domain.RunResult

func (w *Worker) process(ctx context.Context, job domain.Job, run runFunc) {
	ctx = telemetry.WithCorrelationID(ctx, job.CorrelationID)
	ctx, span := w.startJobSpan(ctx, job)
	defer span.End()

	log := w.logger.With("job_id", job.ID, "execution_id", job.ExecutionID, "attempt", job.Attempt, "actor", "system")
	log.InfoContext(ctx, "job claimed", "event", "job.claimed", "fence", job.Fence)
	log.InfoContext(ctx, "connection lock acquired", "event", "lock.acquired", "connection_id", job.ConnectionID)

	l := startLease(ctx, job, w.repo, w.clock, w.cfg.Lease, w.cfg.Heartbeat, log)
	result := run(l.Context(), job, l)
	l.stop()

	if result.Err != nil {
		span.SetStatus(codes.Error, result.Err.Error())
	}
	if l.Lost() {
		log.WarnContext(ctx, "lease lost, result left to reaper", "event", "job.finished", "status", "lease_lost")
		return
	}
	w.finalize(context.WithoutCancel(ctx), log, job, result)
}

func (w *Worker) finalize(ctx context.Context, log *slog.Logger, job domain.Job, result domain.RunResult) {
	now := w.clock.Now()
	lastErr := ""
	if result.Err != nil {
		lastErr = result.Err.Error()
	}

	var err error
	switch {
	case result.Outcome == domain.OutcomeRequeue:
		runAfter := now.Add(orDefault(result.RetryAfter, w.cfg.PollInterval))
		err = w.repo.Reschedule(ctx, job, runAfter, lastErr, false)
		log.InfoContext(ctx, "job requeued", "event", "job.retry", "status", "requeued", "run_after", runAfter)
	case result.Retryable && job.Attempt < job.MaxAttempts:
		runAfter := now.Add(orDefault(result.RetryAfter, RetryDelay(w.cfg.RetryBase, job.Attempt)))
		err = w.repo.Reschedule(ctx, job, runAfter, lastErr, true)
		log.InfoContext(ctx, "job scheduled for retry", "event", "job.retry", "status", "retry", "run_after", runAfter, "error", lastErr)
	default:
		err = w.repo.Finish(ctx, job, lastErr, now)
		log.InfoContext(ctx, "job finished", "event", "job.finished", "status", result.Outcome, "error", lastErr)
	}

	switch {
	case errors.Is(err, ErrLeaseLost):
		log.WarnContext(ctx, "finalize rejected: fence changed", "event", "job.finished", "status", "lease_lost")
	case err != nil:
		log.ErrorContext(ctx, "finalize job failed", "event", "job.finished", "error", err)
	default:
		log.InfoContext(ctx, "connection lock released", "event", "lock.released", "connection_id", job.ConnectionID)
	}
}

// startJobSpan membuat span queue.job yang ter-LINK ke trace request pembuatnya, karena
// job bisa berjalan jauh setelah request selesai.
func (w *Worker) startJobSpan(ctx context.Context, job domain.Job) (context.Context, trace.Span) {
	carrier := propagation.MapCarrier{"traceparent": job.Traceparent}
	origin := trace.SpanContextFromContext(propagation.TraceContext{}.Extract(context.Background(), carrier))
	opts := []trace.SpanStartOption{trace.WithAttributes(
		attribute.Int64("execution.id", job.ExecutionID),
		attribute.String("job.id", job.ID),
		attribute.Int("job.attempt", job.Attempt),
	)}
	if origin.IsValid() {
		opts = append(opts, trace.WithLinks(trace.Link{SpanContext: origin}))
	}
	return w.tracer.Start(ctx, "queue.job", opts...)
}

// Traceparent mengambil header W3C traceparent dari span aktif, untuk disimpan saat Enqueue.
func Traceparent(ctx context.Context) string {
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	return carrier["traceparent"]
}

func orDefault(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
