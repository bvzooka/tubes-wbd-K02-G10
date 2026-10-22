package auth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"dbplatform/internal/contract"
	"dbplatform/internal/domain"
)

// sessionResolver adalah bagian Manager yang dipakai middleware.
type sessionResolver interface {
	Resolve(ctx context.Context, raw string) (*Resolved, error)
}

// Middleware memenuhi contract.AuthMiddleware.
type Middleware struct {
	sessions  sessionResolver
	authz     contract.Authorizer
	handleErr contract.ErrorHandler
	secure    bool
}

func NewMiddleware(sessions sessionResolver, authz contract.Authorizer, handleErr contract.ErrorHandler, secure bool) *Middleware {
	return &Middleware{sessions: sessions, authz: authz, handleErr: handleErr, secure: secure}
}

var _ contract.AuthMiddleware = (*Middleware)(nil)

func (m *Middleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(SessionCookieName)
		if err != nil || c.Value == "" {
			redirectToLogin(w, r)
			return
		}
		res, err := m.sessions.Resolve(r.Context(), c.Value)
		if errors.Is(err, domain.ErrUnauthenticated) {
			http.SetCookie(w, clearSessionCookie(m.secure))
			redirectToLogin(w, r)
			return
		}
		if err != nil {
			m.handleErr(w, r, err)
			return
		}
		if res.NewToken != "" {
			http.SetCookie(w, sessionCookie(res.NewToken, res.ExpiresAt, m.secure))
		}
		// Halaman terautentikasi tidak boleh disimpan cache bersama.
		w.Header().Set("Cache-Control", "no-store")

		ctx := domain.WithPrincipal(r.Context(), res.Principal)
		ctx = domain.WithCSRFToken(ctx, res.CSRFToken)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func redirectToLogin(w http.ResponseWriter, r *http.Request) {
	target := "/login"
	if r.Method == http.MethodGet {
		target += "?next=" + url.QueryEscape(r.URL.RequestURI())
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (m *Middleware) CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if safeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		expected := domain.CSRFTokenFrom(r.Context())
		if !sameOrigin(r) || !tokensEqual(submittedCSRFToken(r), expected) {
			m.handleErr(w, r, domain.ErrForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Middleware) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := domain.PrincipalFrom(r.Context())
		if !ok {
			m.handleErr(w, r, domain.ErrUnauthenticated)
			return
		}
		if !p.IsPlatformAdmin {
			m.handleErr(w, r, domain.ErrForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Middleware) RequireDBAccess(min domain.AccessLevel) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := domain.PrincipalFrom(r.Context())
			if !ok {
				m.handleErr(w, r, domain.ErrUnauthenticated)
				return
			}
			dbID, err := strconv.ParseInt(r.PathValue("dbID"), 10, 64)
			if err != nil || dbID <= 0 {
				m.handleErr(w, r, domain.ErrNotFound)
				return
			}
			allowed, err := m.authz.Can(r.Context(), p.UserID, dbID, min)
			if err != nil {
				m.handleErr(w, r, err)
				return
			}
			if !allowed {
				m.handleErr(w, r, domain.ErrForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
