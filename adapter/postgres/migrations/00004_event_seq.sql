-- +goose Up
CREATE TABLE event_seq (
    run_id text   NOT NULL,
    next   bigint NOT NULL,
    PRIMARY KEY (run_id)
);

-- +goose Down
DROP TABLE event_seq;
