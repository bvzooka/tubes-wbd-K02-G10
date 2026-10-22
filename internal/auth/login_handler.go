package auth

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"dbplatform/internal/clock"
	"dbplatform/internal/contract"
	"dbplatform/internal/domain"
)

// Renderer adalah bagian httpx.Renderer yang dipakai handler di package ini.
type Renderer interface {
	Page(w http.ResponseWriter, r *http.Request, status int, name string, data any)
}

// credentialVerifier adalah bagian Hasher yang dipakai saat login.
type credentialVerifier interface {
	Verify(hash, plain string) bool
	VerifyDummy(plain string) bool
}

// data template auth/login.html.
type LoginPage struct {
	CSRFToken string
	Username  string
	Next      string
	Error     string
}

const (
	loginTemplate      = "auth/login.html"
	defaultAfterLogin  = "/databases"
	maxLoginFormBytes  = 64 << 10
	loginMaxFailures   = 5
	loginFailureWindow = 15 * time.Minute

	msgInvalidCredentials = "Username atau password salah."
	msgFormExpired        = "Formulir login kedaluwarsa. Silakan coba lagi."
	msgTooManyAttempts    = "Terlalu banyak percobaan login gagal. Coba lagi dalam beberapa menit."
)

// Handler melayani GET/POST /login dan POST /logout.
type Handler struct {
	sessions  *Manager
	users     UserFinder
	verifier  credentialVerifier
	render    Renderer
	handleErr contract.ErrorHandler
	mw        contract.AuthMiddleware
	limiter   *loginLimiter
	secure    bool
	logger    *slog.Logger
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
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		sessions:  d.Sessions,
		users:     d.Users,
		verifier:  d.Verifier,
		render:    d.Render,
		handleErr: d.HandleErr,
		mw:        d.MW,
		limiter:   newLoginLimiter(d.Clock, loginMaxFailures, loginFailureWindow),
		secure:    d.Secure,
		logger:    logger,
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
	username := strings.ToLower(strings.TrimSpace(r.PostFormValue("username")))
	password := r.PostFormValue("password")
	next := safeNext(r.PostFormValue("next"))
	page := LoginPage{Username: username, Next: next}

	cookie, err := r.Cookie(LoginCSRFCookieName)
	if err != nil || !sameOrigin(r) || !tokensEqual(cookie.Value, r.PostFormValue(CSRFFieldName)) {
		page.Error = msgFormExpired
		h.renderLogin(w, r, http.StatusForbidden, page)
		return
	}

	ip := clientIP(r)
	if h.limiter.Blocked(username, ip) {
		h.logger.WarnContext(r.Context(), "login rate limited", "username", username, "ip", ip)
		page.Error = msgTooManyAttempts
		h.renderLogin(w, r, http.StatusTooManyRequests, page)
		return
	}

	user, err := h.users.FindByUsername(r.Context(), username)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		h.handleErr(w, r, err)
		return
	}
	var ok bool
	if err == nil && user.IsActive {
		ok = h.verifier.Verify(user.PasswordHash, password)
	} else {
		ok = h.verifier.VerifyDummy(password)
	}
	if !ok {
		h.limiter.Fail(username, ip)
		h.logger.InfoContext(r.Context(), "login failed", "username", username, "ip", ip)
		page.Error = msgInvalidCredentials
		h.renderLogin(w, r, http.StatusUnauthorized, page)
		return
	}
	h.limiter.Reset(username, ip)

	// Login selalu membuat session baru; session lama dari cookie yang dibawa dicabut.
	if old, err := r.Cookie(SessionCookieName); err == nil && old.Value != "" {
		if err := h.sessions.revokeToken(r.Context(), old.Value, ReasonRelogin); err != nil {
			h.handleErr(w, r, err)
			return
		}
	}
	started, err := h.sessions.Start(r.Context(), user.ID)
	if err != nil {
		h.handleErr(w, r, err)
		return
	}
	h.logger.InfoContext(r.Context(), "login succeeded", "user_id", user.ID)
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
	if strings.ContainsAny(next, "\\\r\n") {
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
