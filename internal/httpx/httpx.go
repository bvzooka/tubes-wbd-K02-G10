package httpx

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/clock"
	"dbplatform/internal/contract"
	"dbplatform/internal/domain"
)

type Middleware = func(http.Handler) http.Handler

func Chain(mws ...Middleware) Middleware {
	return func(next http.Handler) http.Handler {
		// Dari belakang biar middleware belakang duluan
		for i := len(mws) - 1; i >= 0; i-- {
			next = mws[i](next)
		}
		return next
	}
}

type Deps struct {
	Pool      *pgxpool.Pool
	Clock     clock.Clock
	Logger    *slog.Logger
	Render    *Renderer
	Flash     *Flasher
	HandleErr contract.ErrorHandler

	Auth    contract.AuthMiddleware
	Authz   contract.Authorizer
	Drivers contract.DriverProvider
}

func (d Deps) Authed() Middleware {
	return Chain(d.Auth.Authenticate, d.Auth.CSRF)
}

func (d Deps) Admin() Middleware {
	return Chain(d.Auth.Authenticate, d.Auth.CSRF, d.Auth.RequireAdmin)
}

func (d Deps) DBView() Middleware {
	return Chain(d.Auth.Authenticate, d.Auth.CSRF, d.Auth.RequireDBAccess(domain.AccessViewOnly))
}

func (d Deps) DBEdit() Middleware {
	return Chain(d.Auth.Authenticate, d.Auth.CSRF, d.Auth.RequireDBAccess(domain.AccessViewEdit))
}
