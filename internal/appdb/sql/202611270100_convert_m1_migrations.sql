-- migration buatan M1 hanya mengisi kolom status, kolom state milik M2 masih bawaan 'draft'
-- M2 tidak pernah mengubah kolom status, jadi migration M2 tidak ikut tersentuh

-- 1. tiap eksekusi M1 dibuatkan satu permintaan bertanda legacy, berhasil maupun gagal
INSERT INTO migration_executions (
    migration_id, connection_id, version, revision, kind, mode, sql_snapshot, checksum, status,
    requested_by, requested_at, started_at, finished_at, failure_code, failure_message, legacy)
SELECT id, connection_id, version, revision, 'apply', 'normal', sql_text, checksum, status,
       COALESCE(executed_by, created_by), COALESCE(executed_at, updated_at), executed_at,
       COALESCE(finished_at, executed_at, updated_at),
       CASE WHEN status = 'failed' THEN 'legacy' ELSE '' END, error_message, TRUE
FROM db_migrations
WHERE status IN ('success', 'failed') AND state = 'draft'
ORDER BY connection_id, version;

-- 2. riwayatnya satu baris per permintaan legacy, diurutkan menurut versi supaya urutan tampilnya benar
INSERT INTO migration_history (
    execution_id, attempt, migration_id, connection_id, version, revision, kind, executed_by, status,
    committed, sql_executed, failure_code, failure_message, started_at, finished_at)
SELECT e.id, 1, e.migration_id, e.connection_id, e.version, e.revision, e.kind, e.requested_by, e.status,
       e.status = 'success', e.sql_snapshot, e.failure_code, e.failure_message,
       COALESCE(e.started_at, e.requested_at), e.finished_at
FROM migration_executions e
WHERE e.legacy AND NOT EXISTS (SELECT 1 FROM migration_history h WHERE h.execution_id = e.id)
ORDER BY e.connection_id, e.version;

-- 3. status definisi disamakan
-- yang berhasil jadi irreversible karena M1 tidak punya SQL down
-- yang gagal jadi superseded supaya tidak menahan versi di atasnya
-- yang masih running tidak diketahui hasilnya, jadi database-nya diblok sampai diperiksa
UPDATE db_migrations
SET state = CASE status
        WHEN 'success' THEN 'applied'
        WHEN 'failed' THEN 'superseded'
        WHEN 'cancelled' THEN 'cancelled'
        ELSE 'unknown'
    END,
    irreversible = irreversible OR status = 'success',
    irreversible_reason = CASE
        WHEN status = 'success' THEN 'Dibuat pada Milestone 1 tanpa SQL down'
        ELSE irreversible_reason
    END
WHERE state = 'draft' AND status <> 'draft';
