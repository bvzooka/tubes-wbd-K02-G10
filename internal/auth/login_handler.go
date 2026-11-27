package auth

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"unicode"

	"dbplatform/internal/clock"
	"dbplatform/internal/contract"
)

// Renderer adalah bagian httpx.Renderer yang dipakai handler di package ini.
type Renderer interface {
	Page(w http.ResponseWriter, r *http.Request, status int, name string, data any)
}

// data template auth/login.html.
type LoginPage struct {
	CSRFToken string
	Username  string
	Next      string
	Error     string
}

const (
	loginTemplate     = "auth/login.html"
	defaultAfterLogin = "/databases"
	maxLoginFormBytes = 64 << 10
)

// Handler melayani GET/POST /login dan POST /logout.
type Handler struct {
	sessions  *Manager
	auth      *Authenticator
	render    Renderer
	handleErr contract.ErrorHandler
	mw        contract.AuthMiddleware
	secure    bool
}

type HandlerDeps struct {
	Sessions  *Manager
	Users     UserFinder
	Verifier  credentialVerifier
	Render    Renderer
	HandleErr contract.ErrorHandler
	MW        contract.AuthMiddleware
	Clock     clock.Clock
	Secure    bool
	Logger    *slog.Logger
}

func NewHandler(d HandlerDeps) *Handler {
	return &Handler{
		sessions:  d.Sessions,
		auth:      NewAuthenticator(d.Sessions, d.Users, d.Verifier, d.Clock, d.Logger),
		render:    d.Render,
		handleErr: d.HandleErr,
		mw:        d.MW,
		secure:    d.Secure,
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /login", h.loginForm)
	mux.HandleFunc("POST /login", h.login)
	mux.Handle("POST /logout", h.mw.Authenticate(h.mw.CSRF(http.HandlerFunc(h.logout))))
}

func (h *Handler) loginForm(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))
	// Sudah login: langsung lanjut.
	if c, err := r.Cookie(SessionCookieName); err == nil && c.Value != "" {
		if res, err := h.sessions.Resolve(r.Context(), c.Value); err == nil {
			if res.NewToken != "" {
				http.SetCookie(w, sessionCookie(res.NewToken, res.ExpiresAt, h.secure))
			}
			http.Redirect(w, r, next, http.StatusSeeOther)
			return
		}
	}
	h.renderLogin(w, r, http.StatusOK, LoginPage{Next: next})
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginFormBytes)
	if err := r.ParseForm(); err != nil {
		h.renderLogin(w, r, http.StatusBadRequest, LoginPage{Error: msgFormExpired})
		return
	}
	username := NormalizeUsername(r.PostFormValue("username"))
	next := safeNext(r.PostFormValue("next"))
	page := LoginPage{Username: username, Next: next}

	cookie, err := r.Cookie(LoginCSRFCookieName)
	if err != nil || !sameOrigin(r) || !tokensEqual(cookie.Value, r.PostFormValue(CSRFFieldName)) {
		page.Error = msgFormExpired
		h.renderLogin(w, r, http.StatusForbidden, page)
		return
	}

	res, err := h.auth.Login(r.Context(), LoginInput{
		Username:      username,
		Password:      r.PostFormValue("password"),
		IP:            clientIP(r),
		PreviousToken: cookieValue(r, SessionCookieName),
	})
	switch {
	case errors.Is(err, ErrTooManyAttempts):
		page.Error = msgTooManyAttempts
		h.renderLogin(w, r, http.StatusTooManyRequests, page)
		return
	case errors.Is(err, ErrInvalidCredentials):
		page.Error = msgInvalidCredentials
		h.renderLogin(w, r, http.StatusUnauthorized, page)
		return
	case err != nil:
		h.handleErr(w, r, err)
		return
	}
	started := res.Session
	http.SetCookie(w, sessionCookie(started.Token, started.ExpiresAt, h.secure))
	http.SetCookie(w, clearLoginCSRFCookie(h.secure))
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookieName); err == nil {
		if err := h.sessions.End(r.Context(), c.Value); err != nil {
			h.handleErr(w, r, err)
			return
		}
	}
	http.SetCookie(w, clearSessionCookie(h.secure))
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// renderLogin selalu membuat token CSRF login baru dan memasangnya di cookie dan form.
func (h *Handler) renderLogin(w http.ResponseWriter, r *http.Request, status int, page LoginPage) {
	token, err := NewCSRFToken()
	if err != nil {
		h.handleErr(w, r, err)
		return
	}
	http.SetCookie(w, loginCSRFCookie(token, h.secure))
	w.Header().Set("Cache-Control", "no-store")
	page.CSRFToken = token
	h.render.Page(w, r, status, loginTemplate, page)
}

// safeNext hanya menerima path lokal (diawali satu "/") untuk mencegah open redirect.
func safeNext(next string) string {
	if next == "" || next[0] != '/' {
		return defaultAfterLogin
	}
	if len(next) > 1 && (next[1] == '/' || next[1] == '\\') {
		return defaultAfterLogin
	}
	// browser membuang tab dan baris baru dari URL, jadi path berisi karakter kontrol bisa terbaca sebagai //situs-lain
	if strings.Contains(next, "\\") || strings.ContainsFunc(next, unicode.IsControl) {
		return defaultAfterLogin
	}
	return next
}

// clientIP memakai RemoteAddr; header X-Forwarded-For sengaja tidak dipercaya.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func cookieValue(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}
