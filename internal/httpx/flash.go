package httpx

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

const FlashCookieName = "wbd_flash"

const (
	FlashSuccess = "success"
	FlashError   = "error"
	FlashInfo    = "info"
)

type Flash struct {
	Kind    string `json:"k"`
	Message string `json:"m"`
}

type Flasher struct {
	key    []byte
	secure bool
}

func NewFlasher(secret []byte, secure bool) *Flasher {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte("flash"))
	return &Flasher{key: m.Sum(nil), secure: secure}
}

func (f *Flasher) sign(payload string) string {
	m := hmac.New(sha256.New, f.key)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (f *Flasher) Set(w http.ResponseWriter, kind, message string) {
	b, _ := json.Marshal(Flash{Kind: kind, Message: message})
	payload := base64.RawURLEncoding.EncodeToString(b)
	http.SetCookie(w, f.cookie(payload+"."+f.sign(payload), 60))
}

func (f *Flasher) Take(w http.ResponseWriter, r *http.Request) *Flash {
	c, err := r.Cookie(FlashCookieName)
	if err != nil {
		return nil
	}
	http.SetCookie(w, f.cookie("", -1))

	payload, sig, ok := strings.Cut(c.Value, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(f.sign(payload))) {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil
	}
	var fl Flash
	if json.Unmarshal(raw, &fl) != nil || fl.Message == "" {
		return nil
	}
	switch fl.Kind {
	case FlashSuccess, FlashError, FlashInfo:
	default:
		fl.Kind = FlashInfo
	}
	return &fl
}

func (f *Flasher) cookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     FlashCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   f.secure,
	}
}
