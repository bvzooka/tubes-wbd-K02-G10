package main

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"dbplatform/internal/access"
	"dbplatform/internal/appdb"
	"dbplatform/internal/auth"
	"dbplatform/internal/authstub"
	"dbplatform/internal/clock"
	"dbplatform/internal/config"
	"dbplatform/internal/contract"
	"dbplatform/internal/httpx"
	webassets "dbplatform/web"
)

func buildApp(ctx context.Context, cfg config.Config, logger *slog.Logger) (http.Handler, func(), error) {
	pool, err := appdb.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { pool.Close() }
	fail := func(err error) (http.Handler, func(), error) {
		cleanup()
		return nil, nil, err
	}
	if err := appdb.Migrate(ctx, pool); err != nil {
		return fail(err)
	}

	var clk clock.Clock = clock.System{}
	flash := httpx.NewFlasher(cfg.SecretKey, cfg.Secure())
	render, err := httpx.NewRenderer(webassets.FS, flash)
	if err != nil {
		return fail(err)
	}
	handleErr := httpx.HandleError(render, logger)

	d := httpx.Deps{
		Pool: pool, Clock: clk, Logger: logger,
		Render: render, Flash: flash, HandleErr: handleErr,
	}
	mux := http.NewServeMux()

	var hasher contract.PasswordHasher
	if cfg.UseStubs {
		logger.Warn("APP_STUBS=true: autentikasi dan otorisasi DIMATIKAN, hanya untuk pengembangan")
		stub, err := authstub.New(ctx, pool)
		if err != nil {
			return fail(err)
		}
		d.Auth, d.Authz, hasher = stub.Middleware, stub.Authorizer, stub.Hasher
	} else {
		authz := access.New(pool)
		mod := auth.New(auth.Config{
			Policy: auth.Policy{
				IdleTimeout:      cfg.Session.IdleTimeout,
				AbsoluteTimeout:  cfg.Session.AbsoluteTimeout,
				RotationInterval: cfg.Session.RotationInterval,
				GracePeriod:      cfg.Session.GracePeriod,
			},
			Secure:     cfg.Secure(),
			BcryptCost: 12,
		}, auth.Deps{
			Pool: pool, Clock: clk, Render: render, HandleErr: handleErr, Authorizer: authz, Logger: logger,
		})
		d.Auth, d.Authz, hasher = mod.Middleware, authz, mod.Hasher

		created, err := auth.SeedAdmin(ctx, pool, mod.Hasher, cfg.SeedAdminUsername, cfg.SeedAdminPassword)
		if err != nil {
			return fail(err)
		}
		if created {
			logger.Info("admin pertama dibuat", "username", cfg.SeedAdminUsername)
		}
		mod.Handler.Register(mux)
		go mod.Sessions.RunCleanup(ctx, time.Hour)
	}
	_ = hasher

	static, err := fs.Sub(webassets.FS, "static")
	if err != nil {
		return fail(err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.Handle("GET /{$}", d.Authed()(http.RedirectHandler("/databases", http.StatusSeeOther)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			http.Error(w, "database platform tidak terjangkau", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ok")
	})

	handler := httpx.Chain(
		httpx.WithRequestID,
		httpx.Recover(logger, handleErr),
		httpx.Logging(logger),
		httpx.SecurityHeaders,
	)(mux)
	return handler, cleanup, nil
}
