package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"dbplatform/internal/dbdriver"
	"dbplatform/internal/domain"
	"dbplatform/internal/telemetry"
)

type fakeDriver struct{ execErr error }

func (fakeDriver) Dialect() domain.DBMS                { return domain.DBMSPostgres }
func (fakeDriver) Capabilities() dbdriver.Capabilities { return dbdriver.Capabilities{} }
func (fakeDriver) Ping(context.Context) error          { return nil }
func (fakeDriver) Close()                              {}
func (fakeDriver) Introspect(context.Context) (*domain.Schema, error) {
	return &domain.Schema{Tables: []domain.Table{{Name: "a"}, {Name: "b"}}}, nil
}
func (fakeDriver) TableData(context.Context, domain.TableRef, domain.PageRequest) (*domain.TableData, error) {
	return &domain.TableData{ResultSet: domain.ResultSet{Rows: make([][]domain.Cell, 3)}}, nil
}
func (f fakeDriver) WithTx(ctx context.Context, _ dbdriver.TxOptions, fn func(dbdriver.Tx) error) error {
	return fn(fakeTx(f))
}
func (fakeDriver) OpenSession(context.Context) (dbdriver.Session, error) { return fakeSession{}, nil }

type fakeTx struct{ execErr error }

func (t fakeTx) Exec(context.Context, string) (int64, error) { return 5, t.execErr }
func (fakeTx) Query(context.Context, string) (*domain.ResultSet, error) {
	return &domain.ResultSet{Rows: make([][]domain.Cell, 1)}, nil
}
func (fakeTx) ExecArgs(context.Context, string, ...any) (int64, error) { return 10, nil }
func (fakeTx) QueryInt(context.Context, string, ...any) (int64, bool, error) {
	return 42, true, nil
}

type fakeSession struct{}

func (fakeSession) TryLock(context.Context, int64) (bool, error) { return true, nil }
func (fakeSession) Unlock(context.Context, int64) error          { return nil }
func (fakeSession) Close()                                       {}
func (fakeSession) WithTx(ctx context.Context, _ dbdriver.TxOptions, fn func(dbdriver.ArgTx) error) error {
	return fn(fakeTx{})
}

func setup(t *testing.T) (*bytes.Buffer, *tracetest.InMemoryExporter) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp)))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	return &bytes.Buffer{}, exp
}

func events(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log bukan JSON: %q", line)
		}
		out = append(out, m)
	}
	return out
}

func TestWrap_EveryCallIsLoggedAndTraced(t *testing.T) {
	buf, exp := setup(t)
	drv := Wrap(fakeDriver{}, telemetry.NewLogger(buf, "info"), "sampledb")
	ctx := context.Background()

	_ = drv.Ping(ctx)
	_, _ = drv.Introspect(ctx)
	_, _ = drv.TableData(ctx, domain.TableRef{Schema: "public", Table: "t"}, domain.PageRequest{Page: 1})
	_ = drv.WithTx(ctx, dbdriver.TxOptions{}, func(tx dbdriver.Tx) error {
		_, _ = tx.Exec(ctx, "UPDATE t SET a = 1")
		_, _ = tx.Query(ctx, "SELECT 1")
		return nil
	})

	const calls = 5
	if got := len(exp.GetSpans()); got != calls {
		t.Fatalf("span db.query = %d, want %d", got, calls)
	}
	// awal dan akhir transaction dicatat terpisah dari pasangan log tiap SQL
	var evs []map[string]any
	transaction := map[any]int{}
	for _, ev := range events(t, buf) {
		if ev["event"] == "tx.begin" || ev["event"] == "tx.commit" {
			transaction[ev["event"]]++
			continue
		}
		evs = append(evs, ev)
	}
	if transaction["tx.begin"] != 1 || transaction["tx.commit"] != 1 {
		t.Errorf("log transaction = %v", transaction)
	}
	if len(evs) != 2*calls {
		t.Fatalf("log = %d baris, want %d", len(evs), 2*calls)
	}
	for i, ev := range evs {
		want := "sql.start"
		if i%2 == 1 {
			want = "sql.result"
		}
		if ev["event"] != want || ev["database"] != "sampledb" || ev["actor"] != "system" {
			t.Errorf("log %d: %v", i, ev)
		}
	}
	if evs[5]["rows"] != float64(3) {
		t.Errorf("TableData harus mencatat jumlah baris: %v", evs[5])
	}
}

func TestWrap_SessionAndArgsAreObserved(t *testing.T) {
	buf, exp := setup(t)
	drv := Wrap(fakeDriver{}, telemetry.NewLogger(buf, "info"), "sampledb")
	sessioner, ok := drv.(dbdriver.Sessioner)
	if !ok {
		t.Fatal("driver yang mendukung Session harus tetap mendukung Session setelah dibungkus")
	}
	ctx := context.Background()
	s, _ := sessioner.OpenSession(ctx)
	_, _ = s.TryLock(ctx, 1)
	_ = s.WithTx(ctx, dbdriver.TxOptions{}, func(tx dbdriver.ArgTx) error {
		_, _ = tx.ExecArgs(ctx, "UPDATE t SET a = 1 WHERE id > $1 AND id <= $2", int64(0), int64(100))
		_, _, _ = tx.QueryInt(ctx, "SELECT max(id) FROM t")
		return nil
	})
	if got := len(exp.GetSpans()); got != 3 {
		t.Fatalf("span = %d, want 3", got)
	}
	if !strings.Contains(buf.String(), `"params":[0,100]`) {
		t.Errorf("parameter batch harus tercatat: %s", buf.String())
	}
}

func TestWrap_ErrorsCarrySQLStateAndRedactSecrets(t *testing.T) {
	buf, _ := setup(t)
	drv := Wrap(fakeDriver{execErr: &pgconn.PgError{Code: "42601", Message: "syntax error"}}, telemetry.NewLogger(buf, "info"), "sampledb")
	ctx := context.Background()

	err := drv.WithTx(ctx, dbdriver.TxOptions{}, func(tx dbdriver.Tx) error {
		_, err := tx.Exec(ctx, "ALTER ROLE app PASSWORD 'Sup3rSecret!'")
		return err
	})
	if !errors.As(err, new(*pgconn.PgError)) {
		t.Fatalf("error asli harus diteruskan: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "Sup3rSecret!") {
		t.Fatalf("password bocor: %s", out)
	}
	if !strings.Contains(out, `"sqlstate":"42601"`) {
		t.Fatalf("sqlstate harus dicatat: %s", out)
	}
}

// akhir transaction harus tercatat sebagai commit atau rollback sesuai hasil isinya
func TestWrap_TransactionEndIsLogged(t *testing.T) {
	buf, _ := setup(t)
	drv := Wrap(fakeDriver{}, telemetry.NewLogger(buf, "info"), "toko")
	boom := errors.New("gagal")
	for _, result := range []error{nil, boom, dbdriver.ErrForceRollback} {
		_ = drv.WithTx(context.Background(), dbdriver.TxOptions{}, func(dbdriver.Tx) error { return result })
	}
	got := buf.String()
	for event, want := range map[string]int{`"event":"tx.begin"`: 3, `"event":"tx.commit"`: 1, `"event":"tx.rollback"`: 2} {
		if n := strings.Count(got, event); n != want {
			t.Errorf("%s muncul %d kali, mau %d", event, n, want)
		}
	}
}
