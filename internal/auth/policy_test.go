package auth

import (
	"testing"
	"time"
)

func TestEvaluate(t *testing.T) {
	p := DefaultPolicy()
	t0 := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	base := Session{
		CreatedAt:         t0,
		AbsoluteExpiresAt: t0.Add(p.AbsoluteTimeout),
		LastSeenAt:        t0,
		RotatedAt:         t0,
		UserActive:        true,
	}
	ptr := func(t time.Time) *time.Time { return &t }

	tests := []struct {
		name string
		now  time.Time
		mod  func(s *Session)
		want Decision
	}{
		{"aktif baru dipakai", t0.Add(10 * time.Second), nil, Decision{Valid: true}},
		{"aktif perlu touch", t0.Add(2 * time.Minute), nil, Decision{Valid: true, Touch: true}},
		{"idle tepat batas masih valid", t0.Add(p.IdleTimeout), func(s *Session) { s.RotatedAt = t0.Add(p.IdleTimeout) },
			Decision{Valid: true, Touch: true}},
		{"idle habis", t0.Add(p.IdleTimeout + time.Second), nil, Decision{Revoke: true, RevokeReason: ReasonIdle}},
		{"absolute habis", t0.Add(p.AbsoluteTimeout), func(s *Session) { s.LastSeenAt = t0.Add(p.AbsoluteTimeout - time.Minute) },
			Decision{Revoke: true, RevokeReason: ReasonAbsolute}},
		{"absolute diperiksa sebelum idle", t0.Add(p.AbsoluteTimeout + time.Hour), nil,
			Decision{Revoke: true, RevokeReason: ReasonAbsolute}},
		{"dicabut", t0.Add(time.Second), func(s *Session) { s.RevokedAt = ptr(t0) }, Decision{}},
		{"user nonaktif", t0.Add(time.Second), func(s *Session) { s.UserActive = false },
			Decision{RevokeAllForUser: true, RevokeReason: ReasonUserInactive}},
		{"rotasi periodik", t0.Add(p.RotationInterval), func(s *Session) { s.LastSeenAt = t0.Add(p.RotationInterval - time.Minute) },
			Decision{Valid: true, Rotate: true}},
		{"rotasi karena privilege", t0.Add(time.Second), func(s *Session) { s.RotateRequired = true },
			Decision{Valid: true, Rotate: true}},
		{"identifier lama dalam grace tidak merotasi", t0.Add(p.RotationInterval + time.Second), func(s *Session) {
			s.MatchedPrevious = true
			s.PrevValidUntil = ptr(t0.Add(p.RotationInterval + p.GracePeriod))
			s.LastSeenAt = t0.Add(p.RotationInterval)
			s.RotatedAt = t0.Add(p.RotationInterval)
			s.RotateRequired = true
		}, Decision{Valid: true}},
		{"identifier lama setelah grace", t0.Add(time.Minute), func(s *Session) {
			s.MatchedPrevious = true
			s.PrevValidUntil = ptr(t0.Add(30 * time.Second))
		}, Decision{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := base
			if tc.mod != nil {
				tc.mod(&s)
			}
			if got := Evaluate(tc.now, s, p); got != tc.want {
				t.Fatalf("Evaluate = %+v, want %+v", got, tc.want)
			}
		})
	}
}
