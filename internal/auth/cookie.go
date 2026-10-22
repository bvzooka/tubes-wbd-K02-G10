package auth

import (
	"net/http"
	"time"
)

const (
	SessionCookieName   = "wbd_session"
	LoginCSRFCookieName = "wbd_login_csrf"

	// loginCSRFMaxAge membatasi umur form login.
	loginCSRFMaxAge = time.Hour
)

func sessionCookie(value string, expires time.Time, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}

func clearSessionCookie(secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}

func loginCSRFCookie(value string, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     LoginCSRFCookieName,
		Value:    value,
		Path:     "/login",
		MaxAge:   int(loginCSRFMaxAge.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}

func clearLoginCSRFCookie(secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     LoginCSRFCookieName,
		Value:    "",
		Path:     "/login",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}
