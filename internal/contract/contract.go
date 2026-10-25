package contract

import (
	"context"
	"net/http"

	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
)

// ErrorHandler memetakan error domain ke respons HTTP.
type ErrorHandler func(w http.ResponseWriter, r *http.Request, err error)

// Authorizer menghitung level akses efektif user pada sebuah database.
type Authorizer interface {
	EffectiveLevel(ctx context.Context, userID, connectionID int64) (domain.AccessLevel, error)
	Can(ctx context.Context, userID, connectionID int64, min domain.AccessLevel) (bool, error)
	ListAccessible(ctx context.Context, userID int64) ([]domain.AccessibleDatabase, error)
}

type PasswordHasher interface {
	Hash(plain string) (string, error)
	Verify(hash, plain string) bool
}

// SessionAdmin memungkinkan modul lain mencabut atau merotasi session user.
type SessionAdmin interface {
	RevokeAllForUser(ctx context.Context, userID int64) error
	MarkRotationNeeded(ctx context.Context, userIDs ...int64) error
}

type AuthMiddleware interface {
	Authenticate(next http.Handler) http.Handler
	RequireAdmin(next http.Handler) http.Handler
	RequireDBAccess(min domain.AccessLevel) func(http.Handler) http.Handler
	CSRF(next http.Handler) http.Handler
}

// DriverProvider memberikan driver untuk sebuah koneksi database target.
type DriverProvider interface {
	Get(ctx context.Context, connectionID int64) (dbdriver.Driver, error)
}
