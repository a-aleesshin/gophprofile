-- +goose Up
-- +goose StatementBegin
CREATE TABLE failed_messages (
    consumer        VARCHAR(64) NOT NULL,
    message_id      UUID        NOT NULL,
    attempts        INTEGER     NOT NULL,
    last_error      TEXT        NOT NULL,
    first_failed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_failed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    dropped_at      TIMESTAMPTZ,

    PRIMARY KEY (consumer, message_id),
    CONSTRAINT failed_messages_attempts_check CHECK (attempts > 0)
);

CREATE INDEX idx_failed_messages_open ON failed_messages (last_failed_at)
    WHERE dropped_at IS NULL;

CREATE INDEX idx_failed_messages_dropped ON failed_messages (dropped_at)
    WHERE dropped_at IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS failed_messages;
-- +goose StatementEnd
