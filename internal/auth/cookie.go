package auth

import (
	"net/http"
	"time"
)

// nama cookie session dan cookie token CSRF untuk form login
const (
	SessionCookieName   = "wbd_session"
	LoginCSRFCookieName = "wbd_login_csrf"

	// loginCSRFMaxAge membatasi umur form login.
	loginCSRFMaxAge = time.Hour
)

// HttpOnly supaya tidak terbaca JavaScript, SameSite Lax supaya tidak ikut terkirim dari situs lain
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

// MaxAge -1 menyuruh browser menghapus cookie
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

// cookie ini hanya dikirim ke path /login karena hanya form login yang memakainya
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
