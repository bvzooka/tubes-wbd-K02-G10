package auth

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"dbplatform/internal/domain"
)

// fakeStore adalah SessionStore in-memory dengan semantik sama seperti Repo.
type fakeStore struct {
	mu     sync.Mutex
	nextID int64
	rows   map[int64]*fakeRow
	users  map[int64]fakeUser
}

type fakeRow struct {
	NewSession
	id             int64
	prevHash       []byte
	prevValidUntil *time.Time
	rotateRequired bool
	lastSeen       time.Time
	rotatedAt      time.Time
	revokedAt      *time.Time
	revokedReason  string
}

type fakeUser struct {
	username string
	admin    bool
	active   bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{rows: map[int64]*fakeRow{}, users: map[int64]fakeUser{}}
}

func (f *fakeStore) addUser(id int64, username string, admin, active bool) {
	f.mu.Lock()
	f.users[id] = fakeUser{username: username, admin: admin, active: active}
	f.mu.Unlock()
}

func (f *fakeStore) Create(_ context.Context, s NewSession) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	f.rows[f.nextID] = &fakeRow{NewSession: s, id: f.nextID, lastSeen: s.CreatedAt, rotatedAt: s.CreatedAt}
	return f.nextID, nil
}

func (f *fakeStore) FindByTokenHash(_ context.Context, hash []byte, now time.Time) (Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var match *fakeRow
	prev := false
	for _, r := range f.rows {
		if bytes.Equal(r.TokenHash, hash) {
			match, prev = r, false
			break
		}
		if r.prevHash != nil && bytes.Equal(r.prevHash, hash) && r.prevValidUntil.After(now) {
			match, prev = r, true
		}
	}
	if match == nil {
		return Session{}, domain.ErrNotFound
	}
	u := f.users[match.UserID]
	return Session{
		ID: match.id, UserID: match.UserID, CSRFToken: match.CSRFToken, RotateRequired: match.rotateRequired,
		CreatedAt: match.CreatedAt, AbsoluteExpiresAt: match.AbsoluteExpiresAt, LastSeenAt: match.lastSeen,
		RotatedAt: match.rotatedAt, RevokedAt: match.revokedAt, PrevValidUntil: match.prevValidUntil,
		MatchedPrevious: prev, Username: u.username, IsPlatformAdmin: u.admin, UserActive: u.active,
	}, nil
}

func (f *fakeStore) Touch(_ context.Context, id int64, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.rows[id]; ok && r.revokedAt == nil {
		r.lastSeen = now
	}
	return nil
}

func (f *fakeStore) Rotate(_ context.Context, id int64, oldHash, newHash []byte, now, prevValidUntil time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[id]
	if !ok || r.revokedAt != nil || !bytes.Equal(r.TokenHash, oldHash) {
		return false, nil
	}
	r.prevHash = r.TokenHash
	r.prevValidUntil = &prevValidUntil
	r.TokenHash = newHash
	r.rotatedAt, r.lastSeen = now, now
	r.rotateRequired = false
	return true, nil
}

func (f *fakeStore) Revoke(_ context.Context, id int64, now time.Time, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.rows[id]; ok && r.revokedAt == nil {
		r.revokedAt, r.revokedReason = &now, reason
	}
	return nil
}

func (f *fakeStore) RevokeAllForUser(_ context.Context, userID int64, now time.Time, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.UserID == userID && r.revokedAt == nil {
			r.revokedAt, r.revokedReason = &now, reason
		}
	}
	return nil
}

func (f *fakeStore) MarkRotationNeeded(_ context.Context, userIDs []int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		for _, id := range userIDs {
			if r.UserID == id && r.revokedAt == nil {
				r.rotateRequired = true
			}
		}
	}
	return nil
}

func (f *fakeStore) DeleteExpired(context.Context, time.Time) (int64, error) { return 0, nil }

func (f *fakeStore) row(id int64) fakeRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *f.rows[id]
}

// fakeRenderer merekam panggilan Page terakhir.
type fakeRenderer struct {
	status int
	name   string
	data   any
}

func (r *fakeRenderer) Page(w http.ResponseWriter, _ *http.Request, status int, name string, data any) {
	r.status, r.name, r.data = status, name, data
	w.WriteHeader(status)
}

// testHandleErr meniru pemetaan httpx.HandleError.
func testHandleErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrUnauthenticated):
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	case errors.Is(err, domain.ErrForbidden):
		http.Error(w, "forbidden", http.StatusForbidden)
	case errors.Is(err, domain.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// fakeAuthorizer memberi level tetap per (user, koneksi).
type fakeAuthorizer struct {
	levels map[[2]int64]domain.AccessLevel
}

func (a fakeAuthorizer) EffectiveLevel(_ context.Context, userID, connID int64) (domain.AccessLevel, error) {
	return a.levels[[2]int64{userID, connID}], nil
}

func (a fakeAuthorizer) Can(ctx context.Context, userID, connID int64, min domain.AccessLevel) (bool, error) {
	l, _ := a.EffectiveLevel(ctx, userID, connID)
	return l.AtLeast(min), nil
}

func (a fakeAuthorizer) ListAccessible(context.Context, int64) ([]domain.AccessibleDatabase, error) {
	return nil, nil
}

// fakeUsers adalah UserFinder in-memory.
type fakeUsers map[string]UserCredential

func (f fakeUsers) FindByUsername(_ context.Context, username string) (UserCredential, error) {
	u, ok := f[username]
	if !ok {
		return UserCredential{}, domain.ErrNotFound
	}
	return u, nil
}
