package main

import (
	"dbplatform/internal/config"
	"dbplatform/internal/contract"
	"dbplatform/internal/httpx"
	"dbplatform/internal/migration"
	"dbplatform/internal/queue"
	"dbplatform/internal/schemadiff"
)

// merakit API migration, proses ini hanya mengajukan dan mengantrekan, SQL-nya dijalankan worker
func wireMigration(
	cfg config.Config, d *httpx.Deps, approvals contract.Approvals,
) (*migration.APIHandler, *schemadiff.APIHandler) {
	repo := migration.NewRepo(d.Pool)
	definitions := migration.NewDefinitionService(repo, d.Authz, d.Logger)
	executions := migration.NewExecutionService(migration.ExecutionDeps{
		Store: repo, DB: d.Pool, Authz: d.Authz, Approvals: approvals,
		Queue: queue.NewRepo(d.Pool, d.Clock), Clock: d.Clock, Logger: d.Logger,
		MaxAttempts: cfg.Worker.MaxAttempts,
	})
	diff := schemadiff.NewService(d.Authz, d.Drivers)
	return migration.NewAPIHandler(definitions, executions), schemadiff.NewAPIHandler(diff, definitions)
}
