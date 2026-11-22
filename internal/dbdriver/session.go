package dbdriver

import "context"

const PlatformMigrationLockKey int64 = 7_310_002

type ArgTx interface {
	Tx
	ExecArgs(ctx context.Context, sql string, args ...any) (rowsAffected int64, err error)
	QueryInt(ctx context.Context, sql string, args ...any) (value int64, found bool, err error)
}

// Session memegang satu koneksi, jadi advisory lock sesi tetap terikat selama job.
type Session interface {
	TryLock(ctx context.Context, key int64) (bool, error)
	Unlock(ctx context.Context, key int64) error
	WithTx(ctx context.Context, opts TxOptions, fn func(tx ArgTx) error) error
	Close()
}

type Sessioner interface {
	OpenSession(ctx context.Context) (Session, error)
}
