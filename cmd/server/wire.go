package main

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"dbplatform/internal/apiv1"
	"dbplatform/internal/appdb"
	"dbplatform/internal/approval"
	"dbplatform/internal/clock"
	"dbplatform/internal/config"
	"dbplatform/internal/contract"
	"dbplatform/internal/httpx"
	"dbplatform/internal/middleware"
	webassets "dbplatform/web"
)

// buildApp merakit seluruh aplikasi API. Rincian per modul ada di wire_*.go, peta route
// JSON ada di routes.go.
func buildApp(ctx context.Context, cfg config.Config, logger *slog.Logger) (http.Handler, func(), error) {
	pool, err := appdb.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, err
	}
	closers := []func(){pool.Close}
	cleanup := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}
	fail := func(err error) (http.Handler, func(), error) {
		cleanup()
		return nil, nil, err
	}
	if err := appdb.Migrate(ctx, pool); err != nil {
		return fail(err)
	}

	clk := clock.System{}
	flash := httpx.NewFlasher(cfg.SecretKey, cfg.Secure())
	render, err := httpx.NewRenderer(webassets.FS, flash)
	if err != nil {
		return fail(err)
	}
	writeAPIErr := apiv1.WriteError(logger)
	handleErr := errorHandlerFor(writeAPIErr, httpx.HandleError(render, logger))
	d := httpx.Deps{Pool: pool, Clock: clk, Logger: logger, Render: render, Flash: flash, HandleErr: handleErr}
	mux := http.NewServeMux()
	spaMode := cfg.WebDistDir != ""

	authn, err := wireAuth(ctx, cfg, &d, mux, spaMode)
	if err != nil {
		return fail(err)
	}
	drivers := wireDrivers(cfg, pool, logger)
	closers = append(closers, drivers.Close)
	d.Drivers = drivers.Provider

	adminAPI := wireAdmin(&d, authn, mux, spaMode)
	if err := wireLegacyPages(ctx, cfg, &d, mux); err != nil {
		return fail(err)
	}

	api := apiv1.NewRouter(mux, authn.apiMiddleware(writeAPIErr), writeAPIErr)
	registerAPIRoutes(api, apiHandlers{
		auth:      authn.api,
		admin:     adminAPI,
		approvals: approval.NewAPIHandler(approval.NewService(pool, clk, logger)),

		connections: wireConnections(cfg, pool),
		viewer:      wireViewer(&d),
	})

	if err := wireStatic(mux, d, cfg, pool); err != nil {
		return fail(err)
	}

	handler := middleware.Chain(
		middleware.WithRequestID,
		otelMiddleware,
		middleware.Logging(logger),
		middleware.Recover(logger, handleErr),
		middleware.SecurityHeaders,
	)(mux)
	return handler, cleanup, nil
}

// errorHandlerFor memilih respons JSON untuk /api/* dan halaman HTML untuk sisanya.
func errorHandlerFor(api, html contract.ErrorHandler) contract.ErrorHandler {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		if apiv1.IsAPIRequest(r) {
			api(w, r, err)
			return
		}
		html(w, r, err)
	}
}

func otelMiddleware(next http.Handler) http.Handler {
	return otelhttp.NewHandler(next, "http.request")
}

func wireStatic(mux *http.ServeMux, d httpx.Deps, cfg config.Config, pool *pgxpool.Pool) error {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			http.Error(w, "database platform tidak terjangkau", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ok")
	})

	if cfg.WebDistDir != "" {
		mux.Handle("GET /", apiv1.SPA(os.DirFS(cfg.WebDistDir)))
		return nil
	}
	static, err := fs.Sub(webassets.FS, "static")
	if err != nil {
		return err
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.Handle("GET /{$}", d.Authed()(http.RedirectHandler("/databases", http.StatusSeeOther)))
	return nil
}
