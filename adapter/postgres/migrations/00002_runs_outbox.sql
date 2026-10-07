-- +goose Up
CREATE TABLE run_signal (
    seq     bigserial   NOT NULL,
    run_id  text        NOT NULL,
    kind    smallint    NOT NULL,
    message jsonb,
    at      timestamptz NOT NULL,
    PRIMARY KEY (seq)
);
CREATE INDEX run_signal_run ON run_signal (run_id, seq);

CREATE TABLE run_notice (
    seq        bigserial   NOT NULL,
    id         text        NOT NULL,
    run_id     text        NOT NULL,
    session_id text        NOT NULL,
    tenant     text        NOT NULL DEFAULT '',
    kind       smallint    NOT NULL,
    reason     text        NOT NULL DEFAULT '',
    at         timestamptz NOT NULL,
    claim      timestamptz,
    PRIMARY KEY (seq),
    UNIQUE (id)
);
CREATE INDEX run_notice_pending ON run_notice (seq) WHERE claim IS NULL;

CREATE TABLE run_counter (
    id         int   NOT NULL,
    generation bigint NOT NULL,
    PRIMARY KEY (id)
);
INSERT INTO run_counter VALUES (1, 0);

ALTER TABLE checkpoint_record ADD COLUMN IF NOT EXISTS created_at timestamptz NOT NULL DEFAULT now();

-- +goose Down
ALTER TABLE checkpoint_record DROP COLUMN IF EXISTS created_at;
DROP TABLE run_counter;
DROP TABLE run_notice;
DROP TABLE run_signal;
