package mysql
import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
	gomysql "github.com/go-sql-driver/mysql"
	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
)

const (
	maxConns       = 5
	connectTimeout = 5 * time.Second
	defaultTxLimit = 30 * time.Second
)

type Driver struct {
	dbms domain.DBMS
	dbName string
	cfg *gomysql.Config
	ro *sql.DB

	mu sync.Mutex
	rw *sql.DB
	closed bool
}

var _ dbdriver.Driver = (*Driver)(nil)

func New(ctx context.Context, c domain.Connection, password string) (dbdriver.Driver, error) {
	if c.DBMS != domain.DBMSMySQL && c.DBMS != domain.DBMSMariaDB {
		return nil, fmt.Errorf("driver mysql tidak mendukung dbms %q: %w", c.DBMS, domain.ErrUnsupported)
	}
	cfg, err := BuildConfig(c, password)
	if err != nil {
		return nil, err
	}
	d, err := NewFromConfig(ctx, c.DBMS, cfg)
	if err != nil {
		return nil, err
	}
	return d, nil
}

func NewFromConfig(ctx context.Context, dbms domain.DBMS, cfg *gomysql.Config) (*Driver, error) {
	ro, err := openPool(cfg)
	if err != nil {
		return nil, err
	}
	d := &Driver{dbms: dbms, dbName: cfg.DBName, cfg: cfg.Clone(), ro: ro}
	if err := d.Ping(ctx); err != nil {
		_ = ro.Close()
		return nil, err
	}
	return d, nil
}

func openPool(cfg *gomysql.Config) (*sql.DB, error) {
	conn, err := gomysql.NewConnector(cfg)
	if err != nil {
		return nil, errors.New("konfigurasi koneksi tidak valid")
	}
	db := sql.OpenDB(conn)
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)
	db.SetConnMaxLifetime(5 * time.Minute)
	return db, nil
}

func (d *Driver) Dialect() domain.DBMS { return d.dbms }

func (d *Driver) Capabilities() dbdriver.Capabilities {
	return dbdriver.Capabilities{TransactionalDDL: false}
}

func (d *Driver) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := d.ro.PingContext(ctx); err != nil {
		return ClassifyError(err)
	}
	return nil
}

func (d *Driver) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	_ = d.ro.Close()
	if d.rw != nil {
		_ = d.rw.Close()
		d.rw = nil
	}
}

func (d *Driver) writePool() (*sql.DB, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, errors.New("driver sudah ditutup")
	}
	if d.rw != nil {
		return d.rw, nil
	}
	cfg := d.cfg.Clone()
	cfg.MultiStatements = true
	rw, err := openPool(cfg)
	if err != nil {
		return nil, err
	}
	d.rw = rw
	return rw, nil
}

type txSpec struct {
	readOnly bool
	iso sql.IsolationLevel
	timeout time.Duration
}

func (d *Driver) runTx(ctx context.Context, db *sql.DB, spec txSpec, fn func(context.Context, *sql.Tx) error) error {
	timeout := spec.timeout
	if timeout <= 0 {
		timeout = defaultTxLimit
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := db.Conn(ctx)
	if err != nil {
		return ClassifyError(err)
	}
	defer conn.Close()

	if reset := d.limitStatementTime(ctx, conn, timeout); reset != nil {
		defer reset()
	}

	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: spec.readOnly, Isolation: spec.iso})
	if err != nil {
		return ClassifyError(err)
	}
	defer func() { _ = tx.Rollback() }()

	ferr := fn(ctx, tx)
	if errors.Is(ferr, dbdriver.ErrForceRollback) {
		return nil
	}
	if ferr != nil {
		return fmt.Errorf("tx: %w", ferr)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", ClassifyError(err))
	}
	return nil
}

func (d *Driver) limitStatementTime(ctx context.Context, conn *sql.Conn, timeout time.Duration) func() {
	var set, unset string
	if d.dbms == domain.DBMSMariaDB {
		set = fmt.Sprintf("SET SESSION max_statement_time = %.3f", timeout.Seconds())
		unset = "SET SESSION max_statement_time = DEFAULT"
	} else {
		set = fmt.Sprintf("SET SESSION max_execution_time = %d", timeout.Milliseconds())
		unset = "SET SESSION max_execution_time = DEFAULT"
	}
	if _, err := conn.ExecContext(ctx, set); err != nil {
		return nil
	}
	return func() {
		rctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(rctx, unset)
	}
}

func (d *Driver) WithTx(ctx context.Context, opts dbdriver.TxOptions, fn func(tx dbdriver.Tx) error) error {
	db := d.ro
	if !opts.ReadOnly {
		var err error
		if db, err = d.writePool(); err != nil {
			return err
		}
	}
	spec := txSpec{readOnly: opts.ReadOnly, timeout: opts.Timeout}
	return d.runTx(ctx, db, spec, func(txCtx context.Context, tx *sql.Tx) error {
		return fn(myTx{tx: tx, ctx: txCtx})
	})
}

type myTx struct {
	tx *sql.Tx
	ctx context.Context
}

func (t myTx) bind(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(t.ctx, cancel)
	return ctx, func() { stop(); cancel() }
}

func (t myTx) Exec(ctx context.Context, sqlText string) (int64, error) {
	ctx, done := t.bind(ctx)
	defer done()
	res, err := t.tx.ExecContext(ctx, sqlText)
	if err != nil {
		return 0, err
	}

	if strings.Contains(sqlText, ";") {
		var n int64
		if err := t.tx.QueryRowContext(ctx, "SELECT ROW_COUNT()").Scan(&n); err != nil {
			return 0, err
		}
		if n < 0 {
			n = 0
		}
		return n, nil
	}
	return res.RowsAffected()
}

func (t myTx) Query(ctx context.Context, sqlText string) (*domain.ResultSet, error) {
	if hasMultipleStatements(sqlText) {
		return nil, fmt.Errorf("query harus satu statement: %w", domain.ErrUnsupported)
	}
	ctx, done := t.bind(ctx)
	defer done()
	rows, err := t.tx.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rs, _, err := collect(rows, -1)
	if err != nil {
		return nil, err
	}
	if rows.NextResultSet() {
		return nil, fmt.Errorf("query harus satu statement: %w", domain.ErrUnsupported)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return rs, nil
}

func hasMultipleStatements(s string) bool {
	seenSep := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			if seenSep {
				return true
			}
			q := c
			for i++; i < len(s); i++ {
				if s[i] == '\\' && q != '`' {
					i++
				} else if s[i] == q {
					if i+1 < len(s) && s[i+1] == q { // kutip digandakan
						i++
						continue
					}
					break
				}
			}
		case c == '-' && i+1 < len(s) && s[i+1] == '-' && (i+2 >= len(s) || s[i+2] == ' ' || s[i+2] == '\t' || s[i+2] == '\n' || s[i+2] == '\r'),
			c == '#':
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return seenSep
			}
			i += end + 3
		case c == ';':
			seenSep = true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
		default:
			if seenSep {
				return true
			}
		}
	}
	return false
}

func collect(rows *sql.Rows, limit int) (rs *domain.ResultSet, more bool, err error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, false, err
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, false, err
	}
	binary := make([]bool, len(cols))
	for i, ct := range types {
		binary[i] = isBinaryType(ct.DatabaseTypeName())
	}

	rs = &domain.ResultSet{Columns: cols}
	raw := make([]sql.RawBytes, len(cols))
	ptrs := make([]any, len(cols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	for rows.Next() {
		if limit >= 0 && len(rs.Rows) >= limit {
			more = true
			break
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, false, err
		}
		row := make([]domain.Cell, len(raw))
		for i, v := range raw {
			row[i] = toCell(v, binary[i])
		}
		rs.Rows = append(rs.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return rs, more, nil
}

func isBinaryType(name string) bool {
	switch name {
	case "BLOB", "BINARY", "VARBINARY", "BIT", "GEOMETRY":
		return true
	}
	return false
}

func toCell(v sql.RawBytes, binary bool) domain.Cell {
	if v == nil {
		return domain.Cell{Null: true}
	}
	if binary || !utf8.Valid(v) {
		return domain.Cell{Value: "0x" + hex.EncodeToString(v)}
	}
	return domain.Cell{Value: string(v)}
}
