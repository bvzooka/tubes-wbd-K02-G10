package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/appdb"
	"dbplatform/internal/approval"
	"dbplatform/internal/clock"
	"dbplatform/internal/config"
	"dbplatform/internal/connmgr"
	"dbplatform/internal/contract"
	"dbplatform/internal/dbdriver/obs"
	"dbplatform/internal/queue"
)

var errRunnerNotReady = errors.New("runner migration belum dirakit: sambungkan migration.Runner di cmd/worker/wire.go")

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

	runner, err := buildRunner(pool, drivers, approval.NewService(pool, clk, logger), clk, logger)
	if err != nil {
		cleanup()
		return workerApp{}, nil, err
	}

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

// buildRunner menyambungkan ExecutionRunner milik modul migration.
// TODO(kurt,2026-11-22): ganti dengan migration.NewRunner(...) setelah runner selesai.
func buildRunner(_ *pgxpool.Pool, _ contract.DriverProvider, _ contract.Approvals, _ clock.Clock, _ *slog.Logger) (contract.ExecutionRunner, error) {
	return nil, errRunnerNotReady
}
