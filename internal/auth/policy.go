package auth

import "time"

// Policy adalah parameter siklus hidup session.
type Policy struct {
	IdleTimeout      time.Duration
	AbsoluteTimeout  time.Duration
	RotationInterval time.Duration
	GracePeriod      time.Duration
	// TouchInterval adalah jeda minimum antar pembaruan last_seen_at (mengurangi tulisan database).
	TouchInterval time.Duration
}

// DefaultPolicy sesuai nilai default di .env.example.
func DefaultPolicy() Policy {
	return Policy{
		IdleTimeout:      30 * time.Minute,
		AbsoluteTimeout:  12 * time.Hour,
		RotationInterval: 15 * time.Minute,
		GracePeriod:      30 * time.Second,
		TouchInterval:    time.Minute,
	}
}

// Session adalah satu baris tabel sessions beserta data user pemiliknya.
type Session struct {
	ID                int64
	UserID            int64
	CSRFToken         string
	RotateRequired    bool
	CreatedAt         time.Time
	AbsoluteExpiresAt time.Time
	LastSeenAt        time.Time
	RotatedAt         time.Time
	RevokedAt         *time.Time
	PrevValidUntil    *time.Time
	// MatchedPrevious true jika session ditemukan lewat prev_token_hash (identifier lama dalam grace).
	MatchedPrevious bool

	Username        string
	IsPlatformAdmin bool
	UserActive      bool
}

// Revoke reasons yang disimpan di kolom revoked_reason.
const (
	ReasonLogout       = "logout"
	ReasonIdle         = "idle_timeout"
	ReasonAbsolute     = "absolute_timeout"
	ReasonUserInactive = "user_inactive"
	ReasonRelogin      = "relogin"
	ReasonAdmin        = "admin_revoked"
)

// Decision adalah hasil evaluasi satu session pada satu waktu.
type Decision struct {
	Valid bool
	// Revoke true jika session ini harus dicabut dengan RevokeReason.
	Revoke       bool
	RevokeReason string
	// RevokeAllForUser true jika seluruh session user harus dicabut (user nonaktif).
	RevokeAllForUser bool
	Rotate           bool
	Touch            bool
}

// Evaluate menerapkan aturan session secara berurutan. Fungsi murni, tanpa I/O.
func Evaluate(now time.Time, s Session, p Policy) Decision {
	if s.RevokedAt != nil {
		return Decision{}
	}
	if s.MatchedPrevious && (s.PrevValidUntil == nil || !now.Before(*s.PrevValidUntil)) {
		return Decision{}
	}
	if !now.Before(s.AbsoluteExpiresAt) {
		return Decision{Revoke: true, RevokeReason: ReasonAbsolute}
	}
	if now.Sub(s.LastSeenAt) > p.IdleTimeout {
		return Decision{Revoke: true, RevokeReason: ReasonIdle}
	}
	if !s.UserActive {
		return Decision{RevokeAllForUser: true, RevokeReason: ReasonUserInactive}
	}

	d := Decision{Valid: true}
	if !s.MatchedPrevious && (s.RotateRequired || now.Sub(s.RotatedAt) >= p.RotationInterval) {
		d.Rotate = true
		return d // rotasi juga memperbarui last_seen_at
	}
	if now.Sub(s.LastSeenAt) >= p.TouchInterval {
		d.Touch = true
	}
	return d
}
