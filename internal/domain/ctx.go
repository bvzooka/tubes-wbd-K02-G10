package domain

import "context"

type ctxKey int

const (
	principalCtxKey ctxKey = iota + 1
	csrfCtxKey
)

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalCtxKey, p)
}

func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalCtxKey).(Principal)
	return p, ok
}

func WithCSRFToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, csrfCtxKey, token)
}

func CSRFTokenFrom(ctx context.Context) string {
	t, _ := ctx.Value(csrfCtxKey).(string)
	return t
}
