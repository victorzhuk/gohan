-- +goose Up
CREATE TABLE session_workspace (
    session_id text   NOT NULL,
    ref        text   NOT NULL,
    generation bigint NOT NULL,
    owner      jsonb  NOT NULL,
    PRIMARY KEY (session_id)
);

-- +goose Down
DROP TABLE session_workspace;
