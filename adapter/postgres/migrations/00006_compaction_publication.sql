-- +goose Up
ALTER TABLE audit_record ADD COLUMN publication_id text;
CREATE UNIQUE INDEX audit_record_publication ON audit_record (session_id, publication_id) WHERE publication_id IS NOT NULL;
CREATE TABLE event_compaction_publication (
    run_id         text  NOT NULL,
    publication_id text  NOT NULL,
    session_id     text  NOT NULL,
    fingerprint    bytea NOT NULL CHECK (octet_length(fingerprint) = 32),
    PRIMARY KEY (run_id, publication_id)
);
CREATE INDEX event_compaction_publication_session ON event_compaction_publication (session_id);

-- +goose Down
DROP INDEX event_compaction_publication_session;
DROP TABLE event_compaction_publication;
DROP INDEX audit_record_publication;
ALTER TABLE audit_record DROP COLUMN publication_id;
