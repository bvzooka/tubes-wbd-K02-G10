CREATE TABLE users (
    id                BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username          TEXT        NOT NULL,
    password_hash     TEXT        NOT NULL,
    is_platform_admin BOOLEAN     NOT NULL DEFAULT FALSE,
    is_active         BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT users_username_format CHECK (username ~ '^[a-z0-9_.-]{3,32}$')
);
CREATE UNIQUE INDEX users_username_key ON users (username);

CREATE TABLE user_groups (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT user_groups_name_len CHECK (char_length(name) BETWEEN 1 AND 64)
);
CREATE UNIQUE INDEX user_groups_name_key ON user_groups (lower(name));

CREATE TABLE group_members (
    group_id BIGINT NOT NULL REFERENCES user_groups (id) ON DELETE CASCADE,
    user_id  BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX group_members_user_idx ON group_members (user_id);

CREATE TABLE connections (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name            TEXT        NOT NULL,
    dbms            TEXT        NOT NULL CHECK (dbms IN ('postgres', 'mysql', 'mariadb')),
    host            TEXT        NOT NULL,
    port            INTEGER     NOT NULL CHECK (port BETWEEN 1 AND 65535),
    database_name   TEXT        NOT NULL,
    db_username     TEXT        NOT NULL,
    db_password_enc BYTEA       NOT NULL,
    ssl_mode        TEXT        NOT NULL DEFAULT 'prefer',
    is_active       BOOLEAN     NOT NULL DEFAULT TRUE,
    created_by      BIGINT REFERENCES users (id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT connections_name_len CHECK (char_length(name) BETWEEN 1 AND 64)
);
CREATE UNIQUE INDEX connections_name_key ON connections (lower(name));

CREATE TABLE group_database_access (
    group_id      BIGINT      NOT NULL REFERENCES user_groups (id) ON DELETE CASCADE,
    connection_id BIGINT      NOT NULL REFERENCES connections (id) ON DELETE CASCADE,
    access_level  TEXT        NOT NULL CHECK (access_level IN ('view_only', 'view_edit')),
    granted_by    BIGINT REFERENCES users (id),
    granted_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, connection_id)
);
CREATE INDEX group_database_access_conn_idx ON group_database_access (connection_id);

CREATE TABLE sessions (
    id                  BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id             BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash          BYTEA       NOT NULL,
    prev_token_hash     BYTEA,
    prev_valid_until    TIMESTAMPTZ,
    csrf_token          TEXT        NOT NULL,
    rotate_required     BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at          TIMESTAMPTZ NOT NULL,
    absolute_expires_at TIMESTAMPTZ NOT NULL,
    last_seen_at        TIMESTAMPTZ NOT NULL,
    rotated_at          TIMESTAMPTZ NOT NULL,
    revoked_at          TIMESTAMPTZ,
    revoked_reason      TEXT
);
CREATE UNIQUE INDEX sessions_token_hash_key ON sessions (token_hash);
CREATE INDEX sessions_prev_token_hash_idx ON sessions (prev_token_hash) WHERE prev_token_hash IS NOT NULL;
CREATE INDEX sessions_user_idx ON sessions (user_id);

CREATE TABLE db_migrations (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    connection_id BIGINT      NOT NULL REFERENCES connections (id),
    version       INTEGER     NOT NULL CHECK (version > 0),
    name          TEXT        NOT NULL,
    sql_text      TEXT        NOT NULL,
    checksum      TEXT        NOT NULL,
    status        TEXT        NOT NULL DEFAULT 'draft'
                  CHECK (status IN ('draft', 'running', 'success', 'failed', 'cancelled')),
    error_message TEXT        NOT NULL DEFAULT '',
    warning       TEXT        NOT NULL DEFAULT '',
    created_by    BIGINT      NOT NULL REFERENCES users (id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    executed_by   BIGINT REFERENCES users (id),
    executed_at   TIMESTAMPTZ,
    finished_at   TIMESTAMPTZ,
    CONSTRAINT db_migrations_version_key UNIQUE (connection_id, version),
    CONSTRAINT db_migrations_name_len CHECK (char_length(name) BETWEEN 1 AND 120)
);
CREATE UNIQUE INDEX db_migrations_one_running ON db_migrations (connection_id) WHERE status = 'running';
