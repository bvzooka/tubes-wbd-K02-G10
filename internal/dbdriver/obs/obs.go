package obs

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"dbplatform/internal/appdb"
	"dbplatform/internal/contract"
	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
	"dbplatform/internal/telemetry"
)

type NameLookup func(ctx context.Context, connectionID int64) string

func NamesFromDB(db appdb.DBTX) NameLookup {
	return func(ctx context.Context, id int64) string {
		var name string
		if err := db.QueryRow(ctx, `SELECT name FROM connections WHERE id = $1`, id).Scan(&name); err != nil {
			return ""
		}
		return name
	}
}

type provider struct {
	inner  contract.DriverProvider
	name   NameLookup
	logger *slog.Logger
}

// WrapProvider mengembalikan DriverProvider yang semua driver-nya sudah terbungkus.
func WrapProvider(inner contract.DriverProvider, name NameLookup, logger *slog.Logger) contract.DriverProvider {
	return &provider{inner: inner, name: name, logger: logger}
}

func (p *provider) Get(ctx context.Context, connectionID int64) (dbdriver.Driver, error) {
	drv, err := p.inner.Get(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	return Wrap(drv, p.logger, p.name(ctx, connectionID)), nil
}

// Wrap membungkus satu driver. Driver yang mendukung Session tetap mendukung Session.
func Wrap(drv dbdriver.Driver, logger *slog.Logger, database string) dbdriver.Driver {
	o := &observer{logger: logger, tracer: otel.Tracer("dbplatform/dbdriver"), database: database}
	d := &driver{inner: drv, o: o}
	if s, ok := drv.(dbdriver.Sessioner); ok {
		return &sessionDriver{driver: d, sessioner: s}
	}
	return d
}

// ---- observer ----

type observer struct {
	logger   *slog.Logger
	tracer   trace.Tracer
	database string
}

// observe menjalankan fn sebagai satu query yang tercatat. fn mengembalikan jumlah baris.
func (o *observer) observe(ctx context.Context, op, query string, params []any, fn func(ctx context.Context) (int64, error)) error {
	query = telemetry.RedactSQL(query)
	ctx, span := o.tracer.Start(ctx, "db.query", trace.WithAttributes(
		attribute.String("db.name", o.database),
		attribute.String("db.operation", op),
		attribute.String("db.statement", query),
	))
	defer span.End()

	attrs := []any{"actor", actorOf(ctx), "database", o.database, "op", op, "query", query}
	if len(params) > 0 {
		attrs = append(attrs, "params", params)
	}
	o.logger.InfoContext(ctx, "sql start", append([]any{"event", "sql.start"}, attrs...)...)

	start := time.Now()
	rows, err := fn(ctx)
	attrs = append(attrs, "duration_ms", time.Since(start).Milliseconds(), "rows", rows)

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		attrs = append(attrs, "status", "error", "error", err.Error())
		if state := sqlState(err); state != "" {
			attrs = append(attrs, "sqlstate", state)
		}
		o.logger.WarnContext(ctx, "sql result", append([]any{"event", "sql.result"}, attrs...)...)
		return err
	}
	span.SetAttributes(attribute.Int64("db.rows", rows))
	o.logger.InfoContext(ctx, "sql result", append([]any{"event", "sql.result", "status", "ok"}, attrs...)...)
	return nil
}

// transaction mencatat mulai dan akhir sebuah transaction secara terpisah dari SQL di dalamnya
// run memanggil done dengan hasil isi transaction, dari situ diketahui akhirnya commit atau rollback
func (o *observer) transaction(ctx context.Context, run func(done func(error) error) error) error {
	attrs := []any{"actor", actorOf(ctx), "database", o.database}
	o.logger.InfoContext(ctx, "transaction begin", append([]any{"event", "tx.begin"}, attrs...)...)

	start := time.Now()
	finished := false
	var bodyErr error
	err := run(func(result error) error {
		finished, bodyErr = true, result
		return result
	})

	event, status := "tx.commit", "ok"
	switch {
	case !finished:
		// isi transaction tidak pernah dijalankan, berarti gagal saat membuka
		event, status = "tx.begin", "error"
	case errors.Is(bodyErr, dbdriver.ErrForceRollback):
		event = "tx.rollback"
	case bodyErr != nil:
		event, status = "tx.rollback", "error"
	case err != nil:
		status = "error"
	}
	attrs = append(attrs, "event", event, "status", status, "duration_ms", time.Since(start).Milliseconds())
	if err != nil {
		attrs = append(attrs, "error", err.Error())
	}
	o.logger.InfoContext(ctx, "transaction end", attrs...)
	return err
}

func actorOf(ctx context.Context) string {
	if p, ok := domain.PrincipalFrom(ctx); ok {
		return p.Username
	}
	return "system"
}

func sqlState(err error) string {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code
	}
	return ""
}

// ---- driver ----

type driver struct {
	inner dbdriver.Driver
	o     *observer
}

func (d *driver) Dialect() domain.DBMS                { return d.inner.Dialect() }
func (d *driver) Capabilities() dbdriver.Capabilities { return d.inner.Capabilities() }
func (d *driver) Close()                              { d.inner.Close() }

func (d *driver) Ping(ctx context.Context) error {
	return d.inner.Ping(d.o.hooked(ctx, "ping"))
}

func (d *driver) Introspect(ctx context.Context) (*domain.Schema, error) {
	return d.inner.Introspect(d.o.hooked(ctx, "introspect"))
}

func (d *driver) TableData(ctx context.Context, ref domain.TableRef, page domain.PageRequest) (*domain.TableData, error) {
	return d.inner.TableData(d.o.hooked(ctx, "table_data"), ref, page)
}

// memasang hook yg mencatat setiap SQL driver dengan nama operasi op
func (o *observer) hooked(ctx context.Context, op string) context.Context {
	return dbdriver.WithQueryHook(ctx, func(ctx context.Context, sql string, args []any, run func(context.Context) (int64, error)) error {
		return o.observe(ctx, op, sql, args, run)
	})
}

// membungkus pembuat driver, dipakai untuk tes koneksi
func WrapFactory[F ~func(context.Context, domain.Connection, string) (dbdriver.Driver, error)](factory F, logger *slog.Logger) F {
	return func(ctx context.Context, c domain.Connection, password string) (dbdriver.Driver, error) {
		database := connectionLabel(c)
		o := &observer{logger: logger, tracer: otel.Tracer("dbplatform/dbdriver"), database: database}
		drv, err := factory(o.hooked(ctx, "connect"), c, password)
		if err != nil {
			return nil, err
		}
		return Wrap(drv, logger, database), nil
	}
}

// memakai nama koneksi, atau host/database untuk koneksi yang belum disimpan
func connectionLabel(c domain.Connection) string {
	if c.Name != "" {
		return c.Name
	}
	return c.Host + ":" + strconv.Itoa(c.Port) + "/" + c.DatabaseName
}

func (d *driver) WithTx(ctx context.Context, opts dbdriver.TxOptions, fn func(tx dbdriver.Tx) error) error {
	return d.o.transaction(ctx, func(done func(error) error) error {
		return d.inner.WithTx(ctx, opts, func(inner dbdriver.Tx) error {
			return done(fn(&tx{inner: inner, o: d.o}))
		})
	})
}

type tx struct {
	inner dbdriver.Tx
	o     *observer
}

func (t *tx) Exec(ctx context.Context, sql string) (int64, error) {
	var n int64
	err := t.o.observe(ctx, "exec", sql, nil, func(ctx context.Context) (int64, error) {
		var err error
		n, err = t.inner.Exec(ctx, sql)
		return n, err
	})
	return n, err
}

func (t *tx) Query(ctx context.Context, sql string) (*domain.ResultSet, error) {
	var rs *domain.ResultSet
	err := t.o.observe(ctx, "query", sql, nil, func(ctx context.Context) (int64, error) {
		var err error
		rs, err = t.inner.Query(ctx, sql)
		if rs == nil {
			return 0, err
		}
		return int64(len(rs.Rows)), err
	})
	return rs, err
}

// ---- session (driver Postgres) ----

type sessionDriver struct {
	*driver
	sessioner dbdriver.Sessioner
}

func (d *sessionDriver) OpenSession(ctx context.Context) (dbdriver.Session, error) {
	s, err := d.sessioner.OpenSession(ctx)
	if err != nil {
		return nil, err
	}
	return &session{inner: s, o: d.o}, nil
}

type session struct {
	inner dbdriver.Session
	o     *observer
}

func (s *session) Close() { s.inner.Close() }

func (s *session) TryLock(ctx context.Context, key int64) (bool, error) {
	var ok bool
	err := s.o.observe(ctx, "try_lock", "SELECT pg_try_advisory_lock($1)", []any{key}, func(ctx context.Context) (int64, error) {
		var err error
		ok, err = s.inner.TryLock(ctx, key)
		return 0, err
	})
	return ok, err
}

func (s *session) Unlock(ctx context.Context, key int64) error {
	return s.o.observe(ctx, "unlock", "SELECT pg_advisory_unlock($1)", []any{key}, func(ctx context.Context) (int64, error) {
		return 0, s.inner.Unlock(ctx, key)
	})
}

func (s *session) WithTx(ctx context.Context, opts dbdriver.TxOptions, fn func(tx dbdriver.ArgTx) error) error {
	return s.o.transaction(ctx, func(done func(error) error) error {
		return s.inner.WithTx(ctx, opts, func(inner dbdriver.ArgTx) error {
			return done(fn(&argTx{tx: tx{inner: inner, o: s.o}, inner: inner}))
		})
	})
}

type argTx struct {
	tx
	inner dbdriver.ArgTx
}

func (t *argTx) ExecArgs(ctx context.Context, sql string, args ...any) (int64, error) {
	var n int64
	err := t.o.observe(ctx, "exec", sql, args, func(ctx context.Context) (int64, error) {
		var err error
		n, err = t.inner.ExecArgs(ctx, sql, args...)
		return n, err
	})
	return n, err
}

func (t *argTx) QueryInt(ctx context.Context, sql string, args ...any) (int64, bool, error) {
	var v int64
	var found bool
	err := t.o.observe(ctx, "query", sql, args, func(ctx context.Context) (int64, error) {
		var err error
		v, found, err = t.inner.QueryInt(ctx, sql, args...)
		if found {
			return 1, err
		}
		return 0, err
	})
	return v, found, err
}
