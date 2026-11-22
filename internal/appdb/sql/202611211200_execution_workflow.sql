-- Hanya menambah tabel/kolom; pembersihan kolom lama db_migrations menyusul bersama modul migration baru.


-- 1. Leader group
ALTER TABLE user_groups
    ADD COLUMN leader_id BIGINT REFERENCES users (id);

-- 2. Definisi migration
ALTER TABLE db_migrations
    ADD COLUMN sql_down            TEXT    NOT NULL DEFAULT '',
    ADD COLUMN irreversible        BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN irreversible_reason TEXT    NOT NULL DEFAULT '',
    ADD COLUMN mode                TEXT    NOT NULL DEFAULT 'normal' CHECK (mode IN ('normal', 'batch')),
    ADD COLUMN batch_config        JSONB,
    ADD COLUMN revision            INTEGER NOT NULL DEFAULT 1,
    ADD COLUMN state               TEXT    NOT NULL DEFAULT 'draft';

-- 3. Eksekusi
CREATE TABLE migration_executions (
    id                      BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    migration_id            BIGINT      NOT NULL REFERENCES db_migrations (id),
    connection_id           BIGINT      NOT NULL REFERENCES connections (id),
    version                 INTEGER     NOT NULL,
    revision                INTEGER     NOT NULL,
    kind                    TEXT        NOT NULL CHECK (kind IN ('apply', 'rollback', 'forward')),
    mode                    TEXT        NOT NULL CHECK (mode IN ('normal', 'batch')),
    batch_config            JSONB,
    sql_snapshot            TEXT        NOT NULL,
    checksum                TEXT        NOT NULL,
    rolls_back_execution_id BIGINT REFERENCES migration_executions (id),
    resumes_execution_id    BIGINT REFERENCES migration_executions (id),
    root_execution_id       BIGINT REFERENCES migration_executions (id),
    status                  TEXT        NOT NULL DEFAULT 'pending_review'
        CHECK (status IN ('pending_review', 'rejected', 'approved', 'cancelled', 'queued', 'running',
                          'success', 'failed', 'partial', 'partially_reverted', 'outcome_unknown')),
    requested_by            BIGINT      NOT NULL REFERENCES users (id),
    requested_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    approved_at             TIMESTAMPTZ,
    queued_at               TIMESTAMPTZ,
    started_at              TIMESTAMPTZ,
    finished_at             TIMESTAMPTZ,
    failure_code            TEXT        NOT NULL DEFAULT '',
    failure_message         TEXT        NOT NULL DEFAULT '',
    scope_max               BIGINT,
    cursor_value            BIGINT,
    batches_committed       INTEGER     NOT NULL DEFAULT 0,
    rows_committed          BIGINT      NOT NULL DEFAULT 0,
    correlation_id          TEXT        NOT NULL DEFAULT '',
    legacy                  BOOLEAN     NOT NULL DEFAULT FALSE
);
CREATE INDEX migration_executions_conn_idx ON migration_executions (connection_id, id);
CREATE INDEX migration_executions_migration_idx ON migration_executions (migration_id);
-- Satu eksekusi hanya boleh dibatalkan oleh satu rollback yang berhasil.
CREATE UNIQUE INDEX migration_executions_one_rollback
    ON migration_executions (rolls_back_execution_id)
    WHERE kind = 'rollback' AND status = 'success';

-- 4. Approval
CREATE TABLE execution_approvals (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    execution_id BIGINT      NOT NULL REFERENCES migration_executions (id),
    group_id     BIGINT      NOT NULL,            -- tanpa FK: riwayat harus utuh
    group_name   TEXT        NOT NULL,
    leader_id    BIGINT      NOT NULL REFERENCES users (id),
    status       TEXT        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    decided_by   BIGINT REFERENCES users (id),
    decided_at   TIMESTAMPTZ,
    comment      TEXT        NOT NULL DEFAULT '',
    auto         BOOLEAN     NOT NULL DEFAULT FALSE,
    CONSTRAINT execution_approvals_unique UNIQUE (execution_id, group_id)
);
CREATE INDEX execution_approvals_leader_idx ON execution_approvals (leader_id) WHERE status = 'pending';

-- 5. Queue
CREATE TABLE migration_jobs (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    execution_id     BIGINT      NOT NULL UNIQUE REFERENCES migration_executions (id),
    connection_id    BIGINT      NOT NULL REFERENCES connections (id),
    state            TEXT        NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'running', 'done')),
    attempt          INTEGER     NOT NULL DEFAULT 0,
    max_attempts     INTEGER     NOT NULL DEFAULT 3,
    run_after        TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_owner      TEXT        NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMPTZ,
    heartbeat_at     TIMESTAMPTZ,
    fence            BIGINT      NOT NULL DEFAULT 0,
    traceparent      TEXT        NOT NULL DEFAULT '',
    correlation_id   TEXT        NOT NULL DEFAULT '',
    enqueued_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at       TIMESTAMPTZ,
    finished_at      TIMESTAMPTZ,
    last_error       TEXT        NOT NULL DEFAULT ''
);
CREATE INDEX migration_jobs_ready_idx ON migration_jobs (enqueued_at) WHERE state = 'queued';

CREATE SEQUENCE connection_lock_fence_seq;
CREATE TABLE connection_locks (
    connection_id BIGINT PRIMARY KEY REFERENCES connections (id),
    job_id        UUID        NOT NULL REFERENCES migration_jobs (id),
    owner         TEXT        NOT NULL,
    fence         BIGINT      NOT NULL,
    acquired_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    heartbeat_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL
);

-- 6. Riwayat (append-only, satu baris per attempt)
CREATE TABLE migration_history (
    id                      BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    execution_id            BIGINT      NOT NULL REFERENCES migration_executions (id),
    attempt                 INTEGER     NOT NULL,
    migration_id            BIGINT      NOT NULL REFERENCES db_migrations (id),
    connection_id           BIGINT      NOT NULL REFERENCES connections (id),
    version                 INTEGER     NOT NULL,
    revision                INTEGER     NOT NULL,
    kind                    TEXT        NOT NULL,
    executed_by             BIGINT      NOT NULL REFERENCES users (id),
    status                  TEXT        NOT NULL CHECK (status IN ('success', 'failed', 'partial', 'outcome_unknown')),
    committed               BOOLEAN     NOT NULL,
    sql_executed            TEXT        NOT NULL,   -- salinan SQL yang benar-benar dijalankan ('' bila tidak dieksekusi)
    rolls_back_execution_id BIGINT,
    batches_committed       INTEGER     NOT NULL DEFAULT 0,
    rows_committed          BIGINT      NOT NULL DEFAULT 0,
    failure_code            TEXT        NOT NULL DEFAULT '',
    failure_message         TEXT        NOT NULL DEFAULT '',
    correlation_id          TEXT        NOT NULL DEFAULT '',
    trace_id                TEXT        NOT NULL DEFAULT '',
    started_at              TIMESTAMPTZ NOT NULL,
    finished_at             TIMESTAMPTZ NOT NULL,
    CONSTRAINT migration_history_attempt_key UNIQUE (execution_id, attempt)
);
CREATE INDEX migration_history_conn_idx ON migration_history (connection_id, id);

CREATE FUNCTION migration_history_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'migration_history bersifat append-only';
END;
$$;
CREATE TRIGGER migration_history_no_change
    BEFORE UPDATE OR DELETE ON migration_history
    FOR EACH ROW EXECUTE FUNCTION migration_history_immutable();
