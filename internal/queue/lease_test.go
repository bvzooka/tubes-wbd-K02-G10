package queue

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"dbplatform/internal/clock"
	"dbplatform/internal/domain"
	"dbplatform/internal/telemetry"
)

func TestRetryDelay(t *testing.T) {
	for attempt, want := range map[int]time.Duration{1: 5 * time.Second, 2: 15 * time.Second, 3: 45 * time.Second} {
		if got := RetryDelay(5*time.Second, attempt); got != want {
			t.Errorf("attempt %d: %v, want %v", attempt, got, want)
		}
	}
}

type fakeHeartbeat struct {
	ok    atomic.Bool
	err   error
	calls atomic.Int32
}

func (f *fakeHeartbeat) Heartbeat(context.Context, domain.Job, time.Duration, time.Time) (bool, error) {
	f.calls.Add(1)
	return f.ok.Load(), f.err
}

func waitDone(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("context lease tidak dibatalkan")
	}
}

var quiet = telemetry.NewLogger(io.Discard, "error")

func TestLease_HealthyHeartbeatKeepsLease(t *testing.T) {
	hb := &fakeHeartbeat{}
	hb.ok.Store(true)
	l := startLease(context.Background(), domain.Job{ID: "j", Fence: 7}, hb, clock.System{}, time.Second, 5*time.Millisecond, quiet)
	time.Sleep(40 * time.Millisecond)
	l.stop()
	if l.Lost() || hb.calls.Load() < 2 || l.Fence() != 7 {
		t.Fatalf("lost=%v calls=%d", l.Lost(), hb.calls.Load())
	}
}

func TestLease_FenceRejectedMeansLost(t *testing.T) {
	hb := &fakeHeartbeat{} // ok=false: fence tidak cocok
	l := startLease(context.Background(), domain.Job{ID: "j"}, hb, clock.System{}, time.Second, 5*time.Millisecond, quiet)
	waitDone(t, l.Context())
	l.stop()
	if !l.Lost() {
		t.Fatal("lease harus hilang")
	}
}

func TestLease_ErrorsToleratedUntilLeaseExpires(t *testing.T) {
	clk := clock.NewFake(time.Now())
	hb := &fakeHeartbeat{err: errors.New("db putus")}
	l := startLease(context.Background(), domain.Job{ID: "j"}, hb, clk, 30*time.Second, 5*time.Millisecond, quiet)

	time.Sleep(30 * time.Millisecond)
	if l.Lost() {
		t.Fatal("gagal sesaat sebelum lease habis tidak boleh menghilangkan lease")
	}
	clk.Advance(31 * time.Second)
	waitDone(t, l.Context())
	l.stop()
	if !l.Lost() {
		t.Fatal("setelah lease lewat, lease harus hilang")
	}
}
