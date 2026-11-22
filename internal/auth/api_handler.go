package auth

import (
	"net/http"

	"dbplatform/internal/apiv1"
	"dbplatform/internal/domain"
)

// APIHandler melayani /api/v1/auth/*.
type APIHandler struct {
	auth     *Authenticator
	sessions *Manager
	secure   bool
}

func NewAPIHandler(auth *Authenticator, sessions *Manager, secure bool) *APIHandler {
	return &APIHandler{auth: auth, sessions: sessions, secure: secure}
}

type userJSON struct {
	ID              int64  `json:"id"`
	Username        string `json:"username"`
	IsPlatformAdmin bool   `json:"is_platform_admin"`
}

type sessionJSON struct {
	User      userJSON `json:"user"`
	CSRFToken string   `json:"csrf_token"`
}

func toUserJSON(p domain.Principal) userJSON {
	return userJSON{ID: p.UserID, Username: p.Username, IsPlatformAdmin: p.IsPlatformAdmin}
}

// LoginCSRF memasang cookie wbd_login_csrf. Token yang sama dikirim balik lewat header
// X-CSRF-Token saat login.
func (h *APIHandler) LoginCSRF(w http.ResponseWriter, r *http.Request) error {
	token, err := NewCSRFToken()
	if err != nil {
		return err
	}
	http.SetCookie(w, loginCSRFCookie(token, h.secure))
	w.Header().Set("Cache-Control", "no-store")
	return apiv1.OK(w, map[string]string{"csrf_token": token})
}

func (h *APIHandler) Login(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := apiv1.Decode(w, r, &in); err != nil {
		return err
	}
	if !sameOrigin(r) || !tokensEqual(cookieValue(r, LoginCSRFCookieName), r.Header.Get(CSRFHeaderName)) {
		return ErrLoginCSRF
	}

	res, err := h.auth.Login(r.Context(), LoginInput{
		Username:      in.Username,
		Password:      in.Password,
		IP:            clientIP(r),
		PreviousToken: cookieValue(r, SessionCookieName),
	})
	if err != nil {
		return err
	}
	http.SetCookie(w, sessionCookie(res.Session.Token, res.Session.ExpiresAt, h.secure))
	http.SetCookie(w, clearLoginCSRFCookie(h.secure))
	return apiv1.OK(w, sessionJSON{User: toUserJSON(res.Principal), CSRFToken: res.Session.CSRFToken})
}

func (h *APIHandler) Logout(w http.ResponseWriter, r *http.Request) error {
	if err := h.sessions.End(r.Context(), cookieValue(r, SessionCookieName)); err != nil {
		return err
	}
	http.SetCookie(w, clearSessionCookie(h.secure))
	return apiv1.NoContent(w)
}

func (h *APIHandler) Me(w http.ResponseWriter, r *http.Request) error {
	return apiv1.OK(w, sessionJSON{
		User:      toUserJSON(apiv1.Actor(r)),
		CSRFToken: domain.CSRFTokenFrom(r.Context()),
	})
}
