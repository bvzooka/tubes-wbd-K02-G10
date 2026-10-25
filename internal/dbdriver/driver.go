package dbdriver

import (
	"context"
	"errors"
	"time"

	"dbplatform/internal/domain"
)

// ErrForceRollback dikembalikan oleh fn pada WithTx untuk meminta rollback tanpa error.
var ErrForceRollback = errors.New("dbdriver: force rollback")

// TransactionalDDL bernilai true bila DDL ikut di-rollback (PostgreSQL), false untuk MySQL dan MariaDB.
type Capabilities struct {
	TransactionalDDL bool
}

type TxOptions struct {
	ReadOnly bool
	Timeout  time.Duration // 0 berarti default driver (30 detik)
}

// Exec menerima satu atau beberapa statement tanpa argumen, Query tepat satu statement dengan hasil berupa teks.
type Tx interface {
	Exec(ctx context.Context, sql string) (rowsAffected int64, err error)
	Query(ctx context.Context, sql string) (*domain.ResultSet, error)
}

type Driver interface {
	Dialect() domain.DBMS
	Capabilities() Capabilities
	Ping(ctx context.Context) error
	Introspect(ctx context.Context) (*domain.Schema, error)
	TableData(ctx context.Context, ref domain.TableRef, page domain.PageRequest) (*domain.TableData, error)

	// WithTx commit bila fn nil, rollback tanpa error bila ErrForceRollback, dan rollback dengan error bila lainnya.
	WithTx(ctx context.Context, opts TxOptions, fn func(tx Tx) error) error

	Close()
}
