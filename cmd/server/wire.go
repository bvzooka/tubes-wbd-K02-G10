package main

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/access"
	"dbplatform/internal/admin"
	"dbplatform/internal/appdb"
	"dbplatform/internal/auth"
	"dbplatform/internal/authstub"
	"dbplatform/internal/clock"
	"dbplatform/internal/config"
	"dbplatform/internal/connmgr"
	"dbplatform/internal/contract"
	"dbplatform/internal/explain"
	"dbplatform/internal/httpx"
	"dbplatform/internal/migration"
	"dbplatform/internal/schemadiff"
	"dbplatform/internal/viewer"
	webassets "dbplatform/web"
)

type app struct {
	ctx      context.Context
	cfg      config.Config
	logger   *slog.Logger
	pool     *pgxpool.Pool
	clk      clock.Clock
	d        httpx.Deps
	mux      *http.ServeMux
	hasher   contract.PasswordHasher
	sessions contract.SessionAdmin
	cleanups []func()
}

func (a *app) close() {
	for i := len(a.cleanups) - 1; i >= 0; i-- {
		a.cleanups[i]()
	}
}

// Ngerakit semua modul
func buildApp(ctx context.Context, cfg config.Config, logger *slog.Logger) (http.Handler, func(), error) {
	a := &app{ctx: ctx, cfg: cfg, logger: logger, clk: clock.System{}, mux: http.NewServeMux()}

	steps := []func() error{
		a.openPlatformDB,
		a.buildDeps,
		a.mountAuth,
		a.mountConnections,
		a.mountViewer,
		a.mountExplain,
		a.mountAdmin,
		a.mountMigration,
		a.mountSystemRoutes,
	}
	for _, step := range steps {
		if err := step(); err != nil {
			a.close()
			return nil, nil, err
		}
	}
	return a.buildChain(), a.close, nil
}

func (a *app) openPlatformDB() error {
	pool, err := appdb.Open(a.ctx, a.cfg.DatabaseURL)
	if err != nil {
		return err
	}
	a.pool = pool
	a.cleanups = append(a.cleanups, pool.Close)
	return appdb.Migrate(a.ctx, pool)
}

func (a *app) buildDeps() error {
	flash := httpx.NewFlasher(a.cfg.SecretKey, a.cfg.Secure())
	render, err := httpx.NewRenderer(webassets.FS, flash)
	if err != nil {
		return err
	}
	a.d = httpx.Deps{
		Pool: a.pool, Clock: a.clk, Logger: a.logger,
		Render: render, Flash: flash, HandleErr: httpx.HandleError(render, a.logger),
	}
	return nil
}

func (a *app) mountAuth() error {
	if a.cfg.UseStubs {
		return a.mountStubAuth()
	}
	cfg := a.cfg
	authz := access.New(a.pool)
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
		Pool: a.pool, Clock: a.clk, Render: a.d.Render, HandleErr: a.d.HandleErr, Authorizer: authz, Logger: a.logger,
	})
	a.d.Auth, a.d.Authz, a.hasher, a.sessions = mod.Middleware, authz, mod.Hasher, mod.Sessions

	created, err := auth.SeedAdmin(a.ctx, a.pool, mod.Hasher, cfg.SeedAdminUsername, cfg.SeedAdminPassword)
	if err != nil {
		return err
	}
	if created {
		a.logger.Info("admin pertama dibuat", "username", cfg.SeedAdminUsername)
	}
	mod.Handler.Register(a.mux)
	go mod.Sessions.RunCleanup(a.ctx, time.Hour)
	return nil
}

func (a *app) mountStubAuth() error {
	a.logger.Warn("APP_STUBS=true: autentikasi dan otorisasi DIMATIKAN, hanya untuk pengembangan")
	stub, err := authstub.New(a.ctx, a.pool)
	if err != nil {
		return err
	}
	a.d.Auth, a.d.Authz, a.hasher, a.sessions = stub.Middleware, stub.Authorizer, stub.Hasher, stub.SessionAdmin
	return nil
}

func (a *app) mountConnections() error {
	repo := connmgr.NewRepo(a.pool, a.cfg.SecretKey)
	drivers := connmgr.NewProvider(repo, connmgr.DefaultFactory)
	a.cleanups = append(a.cleanups, drivers.Close)
	a.d.Drivers = drivers
	connmgr.NewHandler(a.d, connmgr.NewService(repo, connmgr.DefaultFactory)).Register(a.mux)
	return nil
}

func (a *app) mountViewer() error {
	viewer.NewHandler(a.d, viewer.NewService(a.d.Authz, a.d.Drivers)).Register(a.mux)
	return nil
}

func (a *app) mountExplain() error {
	explain.NewHandler(explain.NewService(a.d.Authz, a.d.Drivers), a.d.Render, a.d.HandleErr, a.d.Auth).Register(a.mux)
	return nil
}

func (a *app) mountAdmin() error {
	userSvc := admin.NewService(admin.NewUserRepo(a.pool), a.hasher, a.sessions)
	admin.NewUserHandler(a.d, userSvc).Register(a.mux)
	groupSvc := admin.NewGroupService(admin.NewGroupRepo(a.pool), a.sessions)
	admin.NewGroupHandler(a.d, groupSvc).Register(a.mux)
	return nil
}

func (a *app) mountMigration() error {
	svc := migration.NewService(migration.NewRepo(a.pool), a.d.Authz, a.d.Drivers, a.clk)
	// Migration yang nyangkut running ditandain gagal pas start
	recovered, err := svc.RecoverStuck(a.ctx)
	if err != nil {
		return err
	}
	if recovered > 0 {
		a.logger.Warn("migration yang terputus ditandai gagal", "jumlah", recovered)
	}
	migration.NewHandler(a.d, svc).Register(a.mux)
	// Schema diff bikin draft lewat service migration
	schemadiff.NewHandler(a.d, schemadiff.NewService(a.d.Authz, a.d.Drivers, svc)).Register(a.mux)
	return nil
}

func (a *app) mountSystemRoutes() error {
	static, err := fs.Sub(webassets.FS, "static")
	if err != nil {
		return err
	}
	a.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	a.mux.Handle("GET /{$}", a.d.Authed()(http.RedirectHandler("/databases", http.StatusSeeOther)))
	a.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := a.pool.Ping(r.Context()); err != nil {
			http.Error(w, "database platform tidak terjangkau", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ok")
	})
	return nil
}

func (a *app) buildChain() http.Handler {
	return httpx.Chain(
		httpx.WithRequestID,
		httpx.Recover(a.logger, a.d.HandleErr),
		httpx.Logging(a.logger),
		httpx.SecurityHeaders,
	)(a.mux)
}
