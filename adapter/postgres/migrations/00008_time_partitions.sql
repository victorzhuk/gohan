-- +goose Up
-- Time-partition journal/audit/event by day. Staging tables keep the old
-- shape until Migrate backfills in Go (version 8 post-up), then drops them.
ALTER TABLE journal_record RENAME TO journal_record_v7;
ALTER TABLE journal_record_v7 RENAME CONSTRAINT journal_record_pkey TO journal_record_v7_pkey;
ALTER INDEX journal_record_fingerprint RENAME TO journal_record_v7_fingerprint;
ALTER INDEX journal_record_unknown RENAME TO journal_record_v7_unknown;
CREATE TABLE journal_record (
    session_id  text        NOT NULL,
    call_id     text        NOT NULL,
    state       smallint    NOT NULL,
    fingerprint text        NOT NULL,
    tool        text        NOT NULL,
    args        jsonb,
    result      jsonb,
    at          timestamptz NOT NULL,
    attempts    int         NOT NULL DEFAULT 0,
    endpoint    text        NOT NULL DEFAULT '',
    expires_at  timestamptz NOT NULL,
    payload     bytea       NOT NULL,
    PRIMARY KEY (session_id, call_id, at)
) PARTITION BY RANGE (at);
CREATE INDEX journal_record_fingerprint ON journal_record (session_id, fingerprint);
CREATE INDEX journal_record_unknown ON journal_record (session_id) WHERE state = 0;

ALTER TABLE audit_record RENAME TO audit_record_v7;
ALTER TABLE audit_record_v7 RENAME CONSTRAINT audit_record_pkey TO audit_record_v7_pkey;
ALTER INDEX audit_record_tenant RENAME TO audit_record_v7_tenant;
ALTER INDEX audit_record_publication RENAME TO audit_record_v7_publication;
CREATE TABLE audit_record (
    session_id     text        NOT NULL,
    seq            bigint      NOT NULL,
    kind           text        NOT NULL,
    at             timestamptz NOT NULL,
    tenant         text        NOT NULL,
    prev_hash      text        NOT NULL,
    hash           text        NOT NULL,
    payload        bytea       NOT NULL,
    publication_id text,
    PRIMARY KEY (session_id, seq, at)
) PARTITION BY RANGE (at);
CREATE INDEX audit_record_tenant ON audit_record (tenant, at);

ALTER TABLE event_record RENAME TO event_record_v7;
ALTER TABLE event_record_v7 RENAME CONSTRAINT event_record_pkey TO event_record_v7_pkey;
CREATE TABLE event_record (
    run_id  text        NOT NULL,
    seq     bigint      NOT NULL,
    kind    text        NOT NULL,
    at      timestamptz NOT NULL,
    payload bytea       NOT NULL,
    PRIMARY KEY (run_id, seq, at)
) PARTITION BY RANGE (at);

ALTER TABLE run_record SET (fillfactor = 70);

-- +goose Down
ALTER TABLE journal_record RENAME TO journal_record_v8;
ALTER TABLE journal_record_v8 RENAME CONSTRAINT journal_record_pkey TO journal_record_v8_pkey;
ALTER INDEX journal_record_fingerprint RENAME TO journal_record_v8_fingerprint;
ALTER INDEX journal_record_unknown RENAME TO journal_record_v8_unknown;
CREATE TABLE journal_record (
    session_id  text        NOT NULL,
    call_id     text        NOT NULL,
    state       smallint    NOT NULL,
    fingerprint text        NOT NULL,
    tool        text        NOT NULL,
    args        jsonb,
    result      jsonb,
    at          timestamptz NOT NULL,
    attempts    int         NOT NULL DEFAULT 0,
    endpoint    text        NOT NULL DEFAULT '',
    expires_at  timestamptz NOT NULL,
    payload     bytea       NOT NULL,
    PRIMARY KEY (session_id, call_id)
);
INSERT INTO journal_record
    (session_id, call_id, state, fingerprint, tool, args, result, at, attempts, endpoint, expires_at, payload)
SELECT
    session_id, call_id, state, fingerprint, tool, args, result, at, attempts, endpoint, expires_at, payload
FROM journal_record_v8;
DROP TABLE journal_record_v8;
CREATE INDEX journal_record_fingerprint ON journal_record (session_id, fingerprint);
CREATE INDEX journal_record_unknown ON journal_record (session_id) WHERE state = 0;

ALTER TABLE audit_record RENAME TO audit_record_v8;
ALTER TABLE audit_record_v8 RENAME CONSTRAINT audit_record_pkey TO audit_record_v8_pkey;
ALTER INDEX audit_record_tenant RENAME TO audit_record_v8_tenant;
ALTER INDEX IF EXISTS audit_record_publication RENAME TO audit_record_v8_publication;
CREATE TABLE audit_record (
    session_id     text        NOT NULL,
    seq            bigint      NOT NULL,
    kind           text        NOT NULL,
    at             timestamptz NOT NULL,
    tenant         text        NOT NULL,
    prev_hash      text        NOT NULL,
    hash           text        NOT NULL,
    payload        bytea       NOT NULL,
    publication_id text,
    PRIMARY KEY (session_id, seq)
);
INSERT INTO audit_record
    (session_id, seq, kind, at, tenant, prev_hash, hash, payload, publication_id)
SELECT
    session_id, seq, kind, at, tenant, prev_hash, hash, payload, publication_id
FROM audit_record_v8;
DROP TABLE audit_record_v8;
CREATE INDEX audit_record_tenant ON audit_record (tenant, at);
CREATE UNIQUE INDEX audit_record_publication ON audit_record (session_id, publication_id) WHERE publication_id IS NOT NULL;

ALTER TABLE event_record RENAME TO event_record_v8;
ALTER TABLE event_record_v8 RENAME CONSTRAINT event_record_pkey TO event_record_v8_pkey;
CREATE TABLE event_record (
    run_id  text        NOT NULL,
    seq     bigint      NOT NULL,
    kind    text        NOT NULL,
    at      timestamptz NOT NULL,
    payload bytea       NOT NULL,
    PRIMARY KEY (run_id, seq)
);
INSERT INTO event_record
    (run_id, seq, kind, at, payload)
SELECT
    run_id, seq, kind, at, payload
FROM event_record_v8;
DROP TABLE event_record_v8;

ALTER TABLE run_record SET (fillfactor = 100);
