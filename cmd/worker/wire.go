package main

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/access"
	"dbplatform/internal/appdb"
	"dbplatform/internal/approval"
	"dbplatform/internal/clock"
	"dbplatform/internal/config"
	"dbplatform/internal/connmgr"
	"dbplatform/internal/contract"
	"dbplatform/internal/dbdriver/obs"
	"dbplatform/internal/migration"
	"dbplatform/internal/queue"
)

// hasil rakitan: worker queue dan handler /metrics miliknya
type workerApp struct {
	worker  *queue.Worker
	metrics http.Handler
}

// buildWorker merakit worker. Migrasi skema platform TIDAK dijalankan di sini; itu
// tanggung jawab proses API.
func buildWorker(ctx context.Context, cfg config.Config, logger *slog.Logger) (workerApp, func(), error) {
	pool, err := appdb.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return workerApp{}, nil, err
	}
	clk := clock.System{}
	cached := connmgr.NewProvider(connmgr.NewRepo(pool, cfg.SecretKey), connmgr.DefaultFactory)
	drivers := obs.WrapProvider(cached, obs.NamesFromDB(pool), logger)
	cleanup := func() { cached.Close(); pool.Close() }

	runner := buildRunner(pool, drivers, approval.NewService(pool, clk, logger), clk, logger)

	w := queue.NewWorker(queue.NewRepo(pool, clk), runner, queue.Config{
		ID:           cfg.Worker.ID,
		Concurrency:  cfg.Worker.Concurrency,
		PollInterval: cfg.Worker.PollInterval,
		Lease:        cfg.Worker.Lease,
		Heartbeat:    cfg.Worker.Heartbeat,
		RetryBase:    cfg.Worker.RetryBase,
	}, clk, logger)
	return workerApp{worker: w, metrics: metricsHandler(obs.PlatformPools(pool, cached.PoolStats))}, cleanup, nil
}

// merakit runner migration beserta pemeriksa syarat yang dijalankan sebelum eksekusi
func buildRunner(
	pool *pgxpool.Pool, drivers contract.DriverProvider, approvals contract.Approvals, clk clock.Clock, logger *slog.Logger,
) contract.ExecutionRunner {
	repo := migration.NewRepo(pool)
	return migration.NewRunner(migration.RunnerDeps{
		Store: repo, Drivers: drivers, Clock: clk, Logger: logger,
		Revalidator: migration.NewRevalidator(pool, repo, access.New(pool), approvals, drivers),
	})
}
