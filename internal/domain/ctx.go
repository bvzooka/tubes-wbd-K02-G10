package domain

import "context"

// tipe kunci sendiri supaya tidak bentrok dengan kunci context paket lain
type ctxKey int

const (
	principalCtxKey ctxKey = iota + 1
	csrfCtxKey
)

// User yang login disimpen di context request
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalCtxKey, p)
}

// PrincipalFrom mengambil user dari context, ok false kalau request belum login
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalCtxKey).(Principal)
	return p, ok
}

// token CSRF disimpan di context supaya template form bisa memakainya
func WithCSRFToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, csrfCtxKey, token)
}

// CSRFTokenFrom mengembalikan token CSRF, kosong kalau tidak ada
func CSRFTokenFrom(ctx context.Context) string {
	t, _ := ctx.Value(csrfCtxKey).(string)
	return t
}
