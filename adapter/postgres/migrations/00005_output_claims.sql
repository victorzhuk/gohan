-- +goose Up
-- Output claim ledger and session index cursors.
--
-- Backfill note: this step is reconstructive, not a rewrite. Session claims
-- are inserted only where run_record.result_ref, run_record.session_id and
-- session_record all survive. Owners discarded by an earlier
-- ON CONFLICT (ref) DO NOTHING write on output_record, and bytes already
-- erased, are irrecoverable: where neither a surviving reference nor an
-- owner record remains, this migration asserts nothing about that ref and
-- invents no synthetic session. Every output_record tenant and subject with
-- no surviving session attribution is preserved as a legacy owner claim.

CREATE TABLE output_claim (
    ref        text        NOT NULL,
    session_id text        NOT NULL,
    tenant     text        NOT NULL,
    subject    text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (ref, session_id)
);
CREATE INDEX output_claim_owner ON output_claim (tenant, subject);
CREATE INDEX output_claim_session ON output_claim (session_id);

CREATE TABLE output_legacy_claim (
    ref     text NOT NULL,
    tenant  text NOT NULL,
    subject text NOT NULL,
    PRIMARY KEY (ref, tenant, subject)
);
CREATE INDEX output_legacy_claim_owner ON output_legacy_claim (tenant, subject);

CREATE TABLE session_cursor (
    cursor_id  text        NOT NULL PRIMARY KEY,
    tenant     text        NOT NULL,
    subject    text        NOT NULL,
    filters    text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX session_cursor_expires ON session_cursor (expires_at);

CREATE TABLE session_cursor_item (
    cursor_id  text   NOT NULL,
    ordinal    bigint NOT NULL,
    session_id text   NOT NULL,
    PRIMARY KEY (cursor_id, ordinal),
    FOREIGN KEY (cursor_id) REFERENCES session_cursor (cursor_id) ON DELETE CASCADE
);

INSERT INTO output_claim (ref, session_id, tenant, subject)
SELECT o.ref, r.session_id,
       COALESCE(s.owner->>'Tenant', o.tenant),
       COALESCE(s.owner->>'Subject', o.subject)
FROM output_record o
JOIN run_record r ON r.result_ref = o.ref
JOIN session_record s ON s.session_id = r.session_id
ON CONFLICT DO NOTHING;

INSERT INTO output_legacy_claim (ref, tenant, subject)
SELECT DISTINCT o.ref, o.tenant, o.subject
FROM output_record o
WHERE NOT EXISTS (
    SELECT 1 FROM output_claim c WHERE c.ref = o.ref
);

-- +goose Down
DROP TABLE session_cursor_item;
DROP TABLE session_cursor;
DROP TABLE output_legacy_claim;
DROP TABLE output_claim;
