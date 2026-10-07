-- +goose Up
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
CREATE INDEX journal_record_fingerprint ON journal_record (session_id, fingerprint);
CREATE INDEX journal_record_unknown ON journal_record (session_id) WHERE state = 0;

CREATE TABLE session_record (
    session_id text        NOT NULL,
    owner      jsonb       NOT NULL,
    kind       smallint    NOT NULL DEFAULT 0,
    archived   boolean     NOT NULL DEFAULT false,
    version    bigint      NOT NULL DEFAULT 0,
    forked_from jsonb,
    history    jsonb       NOT NULL,
    payload    bytea       NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id)
);

CREATE TABLE run_record (
    run_id       text        NOT NULL,
    session_id   text        NOT NULL,
    tenant       text        NOT NULL,
    operation_id text,
    state        smallint    NOT NULL,
    seq          bigint      NOT NULL DEFAULT 0,
    result_ref   text        NOT NULL DEFAULT '',
    payload      bytea       NOT NULL,
    started_at   timestamptz NOT NULL,
    heartbeat_at timestamptz NOT NULL,
    generation   bigint      NOT NULL DEFAULT 0,
    lease_expires timestamptz,
    resume_token text,
    pending      jsonb,
    preempted    boolean     NOT NULL DEFAULT false,
    suspend_reason text      NOT NULL DEFAULT '',
    finish_reason text        NOT NULL DEFAULT '',
    PRIMARY KEY (run_id)
);
CREATE UNIQUE INDEX run_record_operation ON run_record (tenant, operation_id) WHERE operation_id IS NOT NULL;
CREATE INDEX run_record_heartbeat ON run_record (heartbeat_at);
CREATE INDEX run_record_session ON run_record (session_id);

CREATE TABLE checkpoint_record (
    token       text        NOT NULL,
    run_id      text        NOT NULL,
    session_id  text        NOT NULL,
    consumed    boolean     NOT NULL DEFAULT false,
    expires_at  timestamptz,
    payload     bytea       NOT NULL,
    input       jsonb,
    PRIMARY KEY (token)
);
CREATE INDEX checkpoint_record_run ON checkpoint_record (run_id);

CREATE TABLE output_record (
    ref       text   NOT NULL,
    tenant    text   NOT NULL,
    subject   text   NOT NULL,
    content   bytea  NOT NULL,
    payload   bytea  NOT NULL,
    PRIMARY KEY (ref)
);
CREATE INDEX output_record_subject ON output_record (tenant, subject);

CREATE TABLE event_record (
    run_id     text        NOT NULL,
    seq        bigint      NOT NULL,
    kind       text        NOT NULL,
    at         timestamptz NOT NULL,
    payload    bytea       NOT NULL,
    PRIMARY KEY (run_id, seq)
);

CREATE TABLE audit_record (
    session_id  text        NOT NULL,
    seq         bigint      NOT NULL,
    kind        text        NOT NULL,
    at          timestamptz NOT NULL,
    tenant      text        NOT NULL,
    prev_hash   text        NOT NULL,
    hash        text        NOT NULL,
    payload     bytea       NOT NULL,
    PRIMARY KEY (session_id, seq)
);
CREATE INDEX audit_record_tenant ON audit_record (tenant, at);

CREATE TABLE notes_record (
    scope    smallint    NOT NULL,
    tenant   text        NOT NULL,
    subject  text        NOT NULL,
    session  text        NOT NULL,
    notes    text        NOT NULL DEFAULT '',
    version  bigint      NOT NULL DEFAULT 0,
    PRIMARY KEY (scope, tenant, subject, session)
);

CREATE TABLE redaction_map (
    tenant  text  NOT NULL,
    session text  NOT NULL,
    payload bytea NOT NULL,
    PRIMARY KEY (tenant, session)
);

CREATE TABLE session_blob (
    session_id text NOT NULL,
    blob_ref   text NOT NULL,
    provider   text NOT NULL,
    PRIMARY KEY (session_id, blob_ref)
);

-- +goose Down
DROP TABLE session_blob;
DROP TABLE redaction_map;
DROP TABLE notes_record;
DROP TABLE audit_record;
DROP TABLE event_record;
DROP TABLE output_record;
DROP TABLE checkpoint_record;
DROP TABLE run_record;
DROP TABLE session_record;
DROP TABLE journal_record;
