package httpx

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/clock"
	"dbplatform/internal/contract"
	"dbplatform/internal/domain"
	"dbplatform/internal/middleware"
)

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

func (d Deps) Authed() middleware.Middleware {
	return middleware.Chain(d.Auth.Authenticate, d.Auth.CSRF)
}

func (d Deps) Admin() middleware.Middleware {
	return middleware.Chain(d.Auth.Authenticate, d.Auth.CSRF, d.Auth.RequireAdmin)
}

func (d Deps) DBView() middleware.Middleware {
	return middleware.Chain(d.Auth.Authenticate, d.Auth.CSRF, d.Auth.RequireDBAccess(domain.AccessViewOnly))
}

func (d Deps) DBEdit() middleware.Middleware {
	return middleware.Chain(d.Auth.Authenticate, d.Auth.CSRF, d.Auth.RequireDBAccess(domain.AccessViewEdit))
}
