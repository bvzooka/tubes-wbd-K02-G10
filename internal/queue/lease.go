package queue

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"dbplatform/internal/clock"
	"dbplatform/internal/contract"
	"dbplatform/internal/domain"
)

type heartbeater interface {
	Heartbeat(ctx context.Context, job domain.Job, lease time.Duration, now time.Time) (bool, error)
}

// lease memenuhi contract.Lease. Selama hidup, ia memperpanjang lease tiap interval
// heartbeat. Bila fence ditolak, atau heartbeat terus gagal sampai lease lewat, context
// dibatalkan agar runner berhenti menyentuh target.
type lease struct {
	ctx    context.Context
	cancel context.CancelFunc
	job    domain.Job
	lost   atomic.Bool
	wg     sync.WaitGroup
}

var _ contract.Lease = (*lease)(nil)

func startLease(parent context.Context, job domain.Job, repo heartbeater, clk clock.Clock, duration, every time.Duration, logger *slog.Logger) *lease {
	ctx, cancel := context.WithCancel(parent)
	l := &lease{ctx: ctx, cancel: cancel, job: job}
	l.wg.Go(func() { l.keepAlive(repo, clk, duration, every, logger) })
	return l
}

func (l *lease) keepAlive(repo heartbeater, clk clock.Clock, duration, every time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	validUntil := clk.Now().Add(duration)
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-ticker.C:
		}
		now := clk.Now()
		ok, err := repo.Heartbeat(context.WithoutCancel(l.ctx), l.job, duration, now)
		switch {
		case err == nil && ok:
			validUntil = now.Add(duration)
			continue
		case err == nil && !ok:
			logger.WarnContext(l.ctx, "lease lost", "event", "job.lease_lost", "job_id", l.job.ID, "fence", l.job.Fence)
		case now.Before(validUntil):
			logger.WarnContext(l.ctx, "heartbeat failed, retrying", "event", "job.heartbeat", "job_id", l.job.ID, "error", err)
			continue
		default:
			logger.ErrorContext(l.ctx, "lease expired after heartbeat failures", "event", "job.lease_lost", "job_id", l.job.ID, "error", err)
		}
		l.lost.Store(true)
		l.cancel()
		return
	}
}

func (l *lease) Context() context.Context { return l.ctx }
func (l *lease) Fence() int64             { return l.job.Fence }
func (l *lease) Lost() bool               { return l.lost.Load() }

func (l *lease) stop() {
	l.cancel()
	l.wg.Wait()
}
