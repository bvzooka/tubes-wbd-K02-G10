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
	"dbplatform/internal/middleware"
	"dbplatform/internal/migration"
	"dbplatform/internal/schemadiff"
	"dbplatform/internal/viewer"
	webassets "dbplatform/web"
)

// app menampung yang dibagi antar langkah perakitan: pool, deps handler, mux route, dan daftar cleanup
type app struct {
	ctx    context.Context
	cfg    config.Config
	logger *slog.Logger
	pool   *pgxpool.Pool
	clk    clock.Clock
	d      httpx.Deps
	mux    *http.ServeMux
	// hasher dan sessions diisi mountAuth, dipakai modul admin
	hasher   contract.PasswordHasher
	sessions contract.SessionAdmin
	// cleanups dijalankan terbalik saat berhenti (yang dibuka terakhir ditutup duluan)
	cleanups []func()
}

// tutup semua yang sudah dibuka, urutannya terbalik dari urutan dibuka
func (a *app) close() {
	for i := len(a.cleanups) - 1; i >= 0; i-- {
		a.cleanups[i]()
	}
}

// Ngerakit semua modul, langkah di steps jalan berurutan karena yang belakang butuh hasil yang depan
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
			// kalau ada langkah gagal, tutup dulu yang sudah terbuka (misalnya pool database)
			a.close()
			return nil, nil, err
		}
	}
	return a.buildChain(), a.close, nil
}

// buka koneksi ke database platform lalu jalankan migrasi skemanya
func (a *app) openPlatformDB() error {
	pool, err := appdb.Open(a.ctx, a.cfg.DatabaseURL)
	if err != nil {
		return err
	}
	a.pool = pool
	a.cleanups = append(a.cleanups, pool.Close)
	return appdb.Migrate(a.ctx, pool)
}

// siapkan alat yang dipakai bersama semua handler: flash message, renderer template, dan penanganan error
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

// pasang auth dan otorisasi, pakai versi palsu kalau APP_STUBS=true
func (a *app) mountAuth() error {
	if a.cfg.UseStubs {
		return a.mountStubAuth()
	}
	cfg := a.cfg
	// authz menjawab "user ini boleh akses database apa" berdasarkan group
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
	// simpan hasil rakitan ke app supaya langkah berikutnya bisa memakainya
	a.d.Auth, a.d.Authz, a.hasher, a.sessions = mod.Middleware, authz, mod.Hasher, mod.Sessions

	// buat akun admin pertama kalau belum ada
	created, err := auth.SeedAdmin(a.ctx, a.pool, mod.Hasher, cfg.SeedAdminUsername, cfg.SeedAdminPassword)
	if err != nil {
		return err
	}
	if created {
		a.logger.Info("admin pertama dibuat", "username", cfg.SeedAdminUsername)
	}
	mod.Handler.Register(a.mux)
	// pembersihan session kedaluwarsa jalan di goroutine sendiri, tiap satu jam
	go mod.Sessions.RunCleanup(a.ctx, time.Hour)
	return nil
}

// versi palsu dari auth: semua orang dianggap sudah login, jadi tidak boleh dipakai di production
func (a *app) mountStubAuth() error {
	a.logger.Warn("APP_STUBS=true: autentikasi dan otorisasi DIMATIKAN, hanya untuk pengembangan")
	stub, err := authstub.New(a.ctx, a.pool)
	if err != nil {
		return err
	}
	a.d.Auth, a.d.Authz, a.hasher, a.sessions = stub.Middleware, stub.Authorizer, stub.Hasher, stub.SessionAdmin
	return nil
}

// pasang koneksi database target, drivers dibagi lewat a.d.Drivers jadi ini harus sebelum viewer, explain, migration
func (a *app) mountConnections() error {
	repo := connmgr.NewRepo(a.pool, a.cfg.SecretKey)
	drivers := connmgr.NewProvider(repo, connmgr.DefaultFactory)
	a.cleanups = append(a.cleanups, drivers.Close)
	a.d.Drivers = drivers
	connmgr.NewHandler(a.d, connmgr.NewService(repo, connmgr.DefaultFactory)).Register(a.mux)
	return nil
}

// pasang halaman lihat schema dan data tabel
func (a *app) mountViewer() error {
	viewer.NewHandler(a.d, viewer.NewService(a.d.Authz, a.d.Drivers)).Register(a.mux)
	return nil
}

// pasang halaman EXPLAIN
func (a *app) mountExplain() error {
	explain.NewHandler(explain.NewService(a.d.Authz, a.d.Drivers), a.d.Render, a.d.HandleErr, a.d.Auth).Register(a.mux)
	return nil
}

// pasang halaman admin untuk mengelola user dan group
func (a *app) mountAdmin() error {
	userSvc := admin.NewService(admin.NewUserRepo(a.pool), a.hasher, a.sessions)
	admin.NewUserHandler(a.d, userSvc).Register(a.mux)
	groupSvc := admin.NewGroupService(admin.NewGroupRepo(a.pool), a.sessions)
	admin.NewGroupHandler(a.d, groupSvc).Register(a.mux)
	return nil
}

// pasang migration dan schema diff
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

// route yang bukan milik satu modul: file statis (css/js), halaman awal "/", dan /healthz
func (a *app) mountSystemRoutes() error {
	static, err := fs.Sub(webassets.FS, "static")
	if err != nil {
		return err
	}
	a.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	// halaman awal hanya mengarahkan ke daftar database, dan wajib login
	a.mux.Handle("GET /{$}", a.d.Authed()(http.RedirectHandler("/databases", http.StatusSeeOther)))
	// healthz dipakai untuk cek server hidup dan database platform masih terjangkau
	a.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := a.pool.Ping(r.Context()); err != nil {
			http.Error(w, "database platform tidak terjangkau", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ok")
	})
	return nil
}

// bungkus semua route dengan middleware global: ID request, tangkap panic, log, lalu header keamanan
func (a *app) buildChain() http.Handler {
	return middleware.Chain(
		middleware.WithRequestID,
		middleware.Recover(a.logger, a.d.HandleErr),
		middleware.Logging(a.logger),
		middleware.SecurityHeaders,
	)(a.mux)
}
