package main

import (
	"context"
	"net/http"
	"time"

	"dbplatform/internal/access"
	"dbplatform/internal/auth"
	"dbplatform/internal/authstub"
	"dbplatform/internal/config"
	"dbplatform/internal/contract"
	"dbplatform/internal/httpx"
)

type authBundle struct {
	hasher   contract.PasswordHasher
	sessions contract.SessionAdmin
	api      *auth.APIHandler // nil saat APP_STUBS=true
	module   *auth.Module     // nil saat APP_STUBS=true
	stub     contract.AuthMiddleware
}

// apiMiddleware: middleware auth yang menjawab 401 JSON, bukan redirect ke /login.
func (a authBundle) apiMiddleware(writeErr contract.ErrorHandler) contract.AuthMiddleware {
	if a.module == nil {
		return a.stub
	}
	return a.module.Middleware.ForAPI(writeErr)
}

func wireAuth(ctx context.Context, cfg config.Config, d *httpx.Deps, mux *http.ServeMux, spaMode bool) (authBundle, error) {
	if cfg.UseStubs {
		d.Logger.Warn("APP_STUBS=true: autentikasi dan otorisasi DIMATIKAN, hanya untuk pengembangan")
		stub, err := authstub.New(ctx, d.Pool)
		if err != nil {
			return authBundle{}, err
		}
		d.Auth, d.Authz = stub.Middleware, stub.Authorizer
		return authBundle{hasher: stub.Hasher, sessions: stub.SessionAdmin, stub: stub.Middleware}, nil
	}

	authz := access.New(d.Pool)
	mod := auth.New(auth.Config{
		Policy: auth.Policy{
			IdleTimeout:      cfg.Session.IdleTimeout,
			AbsoluteTimeout:  cfg.Session.AbsoluteTimeout,
			RotationInterval: cfg.Session.RotationInterval,
			GracePeriod:      cfg.Session.GracePeriod,
		},
		Secure:     cfg.Secure(),
		BcryptCost: 12,
	}, auth.Deps{Pool: d.Pool, Clock: d.Clock, Render: d.Render, HandleErr: d.HandleErr, Authorizer: authz, Logger: d.Logger})
	d.Auth, d.Authz = mod.Middleware, authz

	created, err := auth.SeedAdmin(ctx, d.Pool, mod.Hasher, cfg.SeedAdminUsername, cfg.SeedAdminPassword)
	if err != nil {
		return authBundle{}, err
	}
	if created {
		d.Logger.Info("admin pertama dibuat", "username", cfg.SeedAdminUsername)
	}
	if !spaMode {
		mod.Handler.Register(mux)
	}
	go mod.Sessions.RunCleanup(ctx, time.Hour)
	return authBundle{hasher: mod.Hasher, sessions: mod.Sessions, api: mod.API, module: mod}, nil
}
