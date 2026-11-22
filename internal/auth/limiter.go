package auth

import (
	"sync"
	"time"

	"dbplatform/internal/clock"
)

// loginLimiter membatasi percobaan login gagal per kombinasi username dan IP (in-memory).
type loginLimiter struct {
	mu      sync.Mutex
	clock   clock.Clock
	max     int
	window  time.Duration
	entries map[string]*limiterEntry
}

// limiterEntry mencatat jumlah gagal sejak jendela waktu dimulai
type limiterEntry struct {
	failures    int
	windowStart time.Time
}

// maxLimiterEntries mencegah map tumbuh tanpa batas jika diserang banyak kombinasi.
const maxLimiterEntries = 10000

// newLoginLimiter membatasi max kegagalan dalam satu window
func newLoginLimiter(clk clock.Clock, max int, window time.Duration) *loginLimiter {
	return &loginLimiter{clock: clk, max: max, window: window, entries: map[string]*limiterEntry{}}
}

// kunci gabungan username dan IP, supaya satu orang yang salah tidak memblokir orang lain
func limiterKey(username, ip string) string { return username + "\x00" + ip }

// Blocked true jika kombinasi ini sudah mencapai batas kegagalan dalam jendela waktu.
func (l *loginLimiter) Blocked(username, ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[limiterKey(username, ip)]
	if !ok {
		return false
	}
	if l.clock.Now().Sub(e.windowStart) >= l.window {
		delete(l.entries, limiterKey(username, ip))
		return false
	}
	return e.failures >= l.max
}

// Fail mencatat satu kegagalan, atau memulai jendela baru kalau yang lama sudah lewat
func (l *loginLimiter) Fail(username, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	if len(l.entries) >= maxLimiterEntries {
		l.pruneLocked(now)
	}
	key := limiterKey(username, ip)
	e, ok := l.entries[key]
	if !ok || now.Sub(e.windowStart) >= l.window {
		l.entries[key] = &limiterEntry{failures: 1, windowStart: now}
		return
	}
	e.failures++
}

// Reset menghapus catatan gagal setelah login berhasil
func (l *loginLimiter) Reset(username, ip string) {
	l.mu.Lock()
	delete(l.entries, limiterKey(username, ip))
	l.mu.Unlock()
}

// pruneLocked membuang jendela yang sudah kedaluwarsa, dipanggil saat mu sudah dipegang
func (l *loginLimiter) pruneLocked(now time.Time) {
	for k, e := range l.entries {
		if now.Sub(e.windowStart) >= l.window {
			delete(l.entries, k)
		}
	}
}
