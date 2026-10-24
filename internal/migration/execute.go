package migration

import (
	"context"
	"errors"
	"fmt"
	"time"

	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
)

const (
	execTimeout   = 60 * time.Second
	execGrace     = 10 * time.Second
	stuckAfter    = 5 * time.Minute
	maxErrorChars = 2000

	msgInterrupted       = "Eksekusi terputus, status di database target tidak pasti; periksa manual."
	msgDDLWarning        = "DBMS ini tidak mendukung rollback untuk DDL; perubahan sebagian mungkin sudah diterapkan."
	msgTargetInactive    = "Koneksi database target sedang dinonaktifkan."
	msgTargetUnavailable = "Tidak dapat menyiapkan koneksi ke database target."
)

func (s *Service) Execute(ctx context.Context, actor domain.Principal, connID int64, version int) (domain.Migration, error) {
	if err := s.require(ctx, actor, connID, domain.AccessViewEdit); err != nil {
		return domain.Migration{}, err
	}

	claimed, err := s.store.Claim(ctx, connID, version, actor.UserID, s.clock.Now())
	if err != nil {
		return domain.Migration{}, err
	}

	detached := context.WithoutCancel(ctx)
	status, errMsg, warning := s.run(detached, connID, claimed.SQL)
	if err := s.store.Finish(detached, connID, version, status, errMsg, warning, s.clock.Now()); err != nil {
		return domain.Migration{}, fmt.Errorf("record migration result: %w", err)
	}
	return s.store.Get(detached, connID, version)
}

func (s *Service) run(ctx context.Context, connID int64, sql string) (domain.MigrationStatus, string, string) {
	if msg := CheckSQL(sql); msg != "" {
		return domain.MigrationFailed, msg, ""
	}

	driver, err := s.drivers.Get(ctx, connID)
	if err != nil {
		return domain.MigrationFailed, targetMessage(err), ""
	}

	var warning string
	if !driver.Capabilities().TransactionalDDL {
		if stmts, err := Split(sql); err == nil && ContainsDDL(stmts) {
			warning = msgDDLWarning
		}
	}

	execCtx, cancel := context.WithTimeout(ctx, execTimeout+execGrace)
	defer cancel()
	err = driver.WithTx(execCtx, dbdriver.TxOptions{Timeout: execTimeout}, func(tx dbdriver.Tx) error {
		_, err := tx.Exec(execCtx, sql)
		return err
	})
	if err != nil {
		return domain.MigrationFailed, truncate(err.Error(), maxErrorChars), warning
	}
	return domain.MigrationSuccess, "", warning
}

func (s *Service) RecoverStuck(ctx context.Context) (int, error) {
	now := s.clock.Now()
	return s.store.FailStuck(ctx, now.Add(-stuckAfter), msgInterrupted, now)
}

func targetMessage(err error) string {
	switch {
	case errors.Is(err, domain.ErrConnectionInactive):
		return msgTargetInactive
	case errors.Is(err, domain.ErrConnectionFailed):
		return truncate(err.Error(), maxErrorChars)
	default:
		return msgTargetUnavailable
	}
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}
