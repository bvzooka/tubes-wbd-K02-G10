package migration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"dbplatform/internal/access"
	"dbplatform/internal/clock"
	"dbplatform/internal/dbdriver/obs"
	"dbplatform/internal/domain"
	"dbplatform/internal/migration"
	"dbplatform/internal/telemetry"
)

// trace satu job memuat pengambilan kunci, lalu tiap batch beserta query di dalamnya
func TestRunner_Integration_SpansCoverLockAndBatches(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	spans := tracetest.NewInMemoryExporter()
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans)))
	t.Cleanup(func() { otel.SetTracerProvider(previous) })

	b := newBatchEnv(t)
	ctx := context.Background()
	job := b.claim(t, b.d, domain.KindApply, false)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	drivers := obs.WrapProvider(b.drivers, obs.NamesFromDB(b.pool), logger)
	runner := migration.NewRunner(migration.RunnerDeps{
		Store: b.repo, Drivers: drivers, Clock: clock.System{}, Logger: logger,
		Revalidator: migration.NewRevalidator(b.pool, b.repo, access.New(b.pool), b.approvals, drivers),
	})
	if got := runner.Run(ctx, job, fakeLease{ctx}); got.Outcome != domain.OutcomeSuccess {
		t.Fatalf("hasil = %+v", got)
	}
	if err := b.queue().Finish(ctx, job, "", time.Now()); err != nil {
		t.Fatal(err)
	}

	locks := 0
	batches := map[trace.SpanID]int64{}
	var queries tracetest.SpanStubs
	for _, s := range spans.GetSpans() {
		attrs := map[attribute.Key]attribute.Value{}
		for _, a := range s.Attributes {
			attrs[a.Key] = a.Value
		}
		switch s.Name {
		case "migration.lock":
			locks++
			if !attrs["lock.acquired"].AsBool() {
				t.Error("span kunci harus mencatat kuncinya didapat")
			}
		case "migration.batch":
			if !attrs["batch.empty"].AsBool() {
				batches[s.SpanContext.SpanID()] = attrs["batch.number"].AsInt64()
			}
		case "db.query":
			if attrs["db.statement"].AsString() == b.d.UpSQL {
				queries = append(queries, s)
			}
		}
	}
	if locks != 1 || len(batches) != 3 || len(queries) != 3 {
		t.Fatalf("span kunci = %d, batch = %d, query = %d; mau 1, 3, 3", locks, len(batches), len(queries))
	}
	// tiap query SQL up harus berada di bawah span batch yang berbeda
	seen := map[int64]bool{}
	for _, q := range queries {
		seen[batches[q.Parent.SpanID()]] = true
	}
	if !seen[1] || !seen[2] || !seen[3] {
		t.Errorf("induk query = %v, mau batch 1, 2, 3", seen)
	}
}

// tiap log SQL bisa ditelusuri ke migration, eksekusi, job, percobaan, dan batch
func TestRunner_Integration_SQLLogsCarryIdentity(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	b := newBatchEnv(t)
	ctx := context.Background()
	job := b.claim(t, b.d, domain.KindApply, false)

	// decorator log yang sama dengan yang dipasang cmd/worker
	var out bytes.Buffer
	logger := telemetry.NewLogger(&out, "info")
	drivers := obs.WrapProvider(b.drivers, obs.NamesFromDB(b.pool), logger)
	// driver yang dipakai runner datang dari revalidator, jadi keduanya harus memakai yang sudah dibungkus
	runner := migration.NewRunner(migration.RunnerDeps{
		Store: b.repo, Drivers: drivers, Clock: clock.System{}, Logger: logger,
		Revalidator: migration.NewRevalidator(b.pool, b.repo, access.New(b.pool), b.approvals, drivers),
	})
	if got := runner.Run(ctx, job, fakeLease{ctx}); got.Outcome != domain.OutcomeSuccess {
		t.Fatalf("hasil = %+v", got)
	}
	if err := b.queue().Finish(ctx, job, "", time.Now()); err != nil {
		t.Fatal(err)
	}

	events := map[string]int{}
	batches := map[float64]bool{}
	for _, line := range bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n")) {
		var entry map[string]any
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatalf("log bukan JSON: %s", line)
		}
		event, _ := entry["event"].(string)
		events[event]++
		query, _ := entry["query"].(string)
		if event != "sql.result" || query != b.d.UpSQL {
			continue
		}
		for _, key := range []string{"migration_id", "execution_id", "job_id", "attempt", "batch", "database", "params", "duration_ms", "rows"} {
			if entry[key] == nil {
				t.Errorf("log SQL tanpa %s: %s", key, line)
			}
		}
		if n, ok := entry["batch"].(float64); ok {
			batches[n] = true
		}
	}
	if len(batches) != 3 {
		t.Errorf("nomor batch di log = %v, mau 1, 2, 3", batches)
	}
	for _, event := range []string{"sql.start", "sql.result", "tx.begin", "tx.commit", "migration.batch_committed", "migration.attempt"} {
		if events[event] == 0 {
			t.Errorf("event %s tidak tercatat", event)
		}
	}
}
