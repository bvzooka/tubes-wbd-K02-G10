package dbdriver

import (
	"context"
	"errors"
	"time"

	"dbplatform/internal/domain"
)

// ErrForceRollback dikembalikan oleh fn pada WithTx untuk meminta rollback tanpa error.
var ErrForceRollback = errors.New("dbdriver: force rollback")

type Capabilities struct {
	// TransactionalDDL true jika DDL ikut di-rollback (PostgreSQL). False untuk MySQL/MariaDB.
	TransactionalDDL bool
}

type TxOptions struct {
	ReadOnly bool
	Timeout  time.Duration // 0 berarti default driver (30 detik)
}

// Tx adalah handle di dalam satu transaction.
type Tx interface {
	// Exec menjalankan satu atau beberapa statement (tanpa argumen) dan
	// mengembalikan jumlah baris terdampak perintah terakhir.
	Exec(ctx context.Context, sql string) (rowsAffected int64, err error)
	// Query menjalankan tepat satu statement dan mengembalikan seluruh hasil sebagai teks.
	Query(ctx context.Context, sql string) (*domain.ResultSet, error)
}

type Driver interface {
	Dialect() domain.DBMS
	Capabilities() Capabilities
	Ping(ctx context.Context) error
	Introspect(ctx context.Context) (*domain.Schema, error)
	TableData(ctx context.Context, ref domain.TableRef, page domain.PageRequest) (*domain.TableData, error)

	// WithTx menjalankan fn dalam satu transaction.
	// fn mengembalikan nil: commit. ErrForceRollback: rollback dan WithTx mengembalikan nil.
	// Error lain: rollback dan WithTx mengembalikan error tersebut (terbungkus).
	WithTx(ctx context.Context, opts TxOptions, fn func(tx Tx) error) error

	Close()
}
