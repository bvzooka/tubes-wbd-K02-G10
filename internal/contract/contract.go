package contract

import (
	"context"
	"net/http"

	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
)

// ErrorHandler memetakan error domain ke respons HTTP (diimplementasikan httpx.HandleError).
type ErrorHandler func(w http.ResponseWriter, r *http.Request, err error)

// ---- Disediakan Kurt ----

type Authorizer interface {
	// EffectiveLevel mengembalikan level tertinggi user pada koneksi (AccessNone jika tidak ada).
	EffectiveLevel(ctx context.Context, userID, connectionID int64) (domain.AccessLevel, error)
	// Can true jika level efektif minimal min.
	Can(ctx context.Context, userID, connectionID int64, min domain.AccessLevel) (bool, error)
	// ListAccessible mengembalikan semua database yang boleh diakses user (koneksi aktif saja).
	ListAccessible(ctx context.Context, userID int64) ([]domain.AccessibleDatabase, error)
}

type PasswordHasher interface {
	Hash(plain string) (string, error)
	Verify(hash, plain string) bool
}

type SessionAdmin interface {
	// RevokeAllForUser menonaktifkan semua session aktif user.
	RevokeAllForUser(ctx context.Context, userID int64) error
	// MarkRotationNeeded membuat session aktif user tersebut dirotasi pada request berikutnya.
	MarkRotationNeeded(ctx context.Context, userIDs ...int64) error
}

type AuthMiddleware interface {
	Authenticate(next http.Handler) http.Handler
	RequireAdmin(next http.Handler) http.Handler
	// RequireDBAccess membaca r.PathValue("dbID").
	RequireDBAccess(min domain.AccessLevel) func(http.Handler) http.Handler
	CSRF(next http.Handler) http.Handler
}

// ---- Disediakan Reva ----

type DriverProvider interface {
	// Get mengembalikan driver untuk koneksi. Error: ErrNotFound, ErrConnectionInactive, ErrConnectionFailed.
	Get(ctx context.Context, connectionID int64) (dbdriver.Driver, error)
}
