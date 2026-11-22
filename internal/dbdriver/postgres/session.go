package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"dbplatform/internal/dbdriver"
)

// membatasi waktu melepas semua advisory lock saat Session ditutup
const releaseTimeout = 5 * time.Second

// SQL advisory lock tingkat sesi: terikat ke koneksi, ga ke transaction
const (
	tryLockSQL = `SELECT pg_try_advisory_lock($1)`
	unlockSQL = `SELECT pg_advisory_unlock($1)`
	unlockAllSQL = `SELECT pg_advisory_unlock_all()`
)

var _ dbdriver.Sessioner = (*Driver)(nil)

// memegang satu koneksi dari pool selama sesi berjalan
type session struct {
	conn *pgxpool.Conn
}

var _ dbdriver.Session = (*session)(nil)

// mengambil satu koneksi dari pool dan memegangnya sampai Close
//koneksi harus dipin karena advisory lock sesi hilang kalau koneksinya dipakai atau ditutup pihak lain
func (d *Driver) OpenSession(ctx context.Context) (dbdriver.Session, error) {
	conn, err := d.pool.Acquire(ctx)
	if err != nil {
		return nil, ClassifyError(err)
	}
	return &session{conn: conn}, nil
}

// mengambil advisory lock dengan key tanpa menunggu, false bila dipegang sesi lain
func (s *session) TryLock(ctx context.Context, key int64) (bool, error) {
	var got bool
	if err := s.conn.QueryRow(ctx, tryLockSQL, key).Scan(&got); err != nil {
		return false, fmt.Errorf("try advisory lock %d: %w", key, ClassifyError(err))
	}
	return got, nil
}

// melepas advisory lock yang dipegang sesi ini. Lock yang tidak dipegang dianggap error,
// karena menandakan runner salah menghitung kepemilikan lock
func (s *session) Unlock(ctx context.Context, key int64) error {
	var released bool
	if err := s.conn.QueryRow(ctx, unlockSQL, key).Scan(&released); err != nil {
		return fmt.Errorf("advisory unlock %d: %w", key, ClassifyError(err))
	}
	if !released {
		return fmt.Errorf("advisory unlock %d: lock tidak dipegang sesi ini", key)
	}
	return nil
}

// menjalankan fn dalam satu transaction di koneksi terpin, aturan commit dan rollback sama dengan Driver.WithTx
func (s *session) WithTx(ctx context.Context, opts dbdriver.TxOptions, fn func(tx dbdriver.ArgTx) error) error {
	spec := txSpec{readOnly: opts.ReadOnly, timeout: opts.Timeout}
	return runTxOn(ctx, s.conn, spec, func(tx pgx.Tx) error {
		return fn(pgArgTx{pgTx{tx}})
	})
}

// melepas semua advisory lock lalu mengembalikan koneksi ke pool. Bila lock gagal dilepas,
// koneksi ditutup (bukan dikembalikan) biar lock yang tersisa ga bocor ke pemakai berikutnya
func (s *session) Close() {
	// context baru karena ctx job bisa sudah dibatalkan saat sesi ditutup
	ctx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
	defer cancel()
	if _, err := s.conn.Exec(ctx, unlockAllSQL); err != nil {
		_ = s.conn.Conn().Close(ctx)
	}
	s.conn.Release()
}
