-- +goose Up
-- +goose StatementBegin
CREATE TABLE outbox (
    id            BIGSERIAL PRIMARY KEY,
    transport     VARCHAR(16)  NOT NULL,
    destination   VARCHAR(128) NOT NULL,
    partition_key VARCHAR(255),
    message_type  VARCHAR(64)  NOT NULL,
    payload       JSONB        NOT NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    published_at  TIMESTAMPTZ,
    attempts      INTEGER      NOT NULL DEFAULT 0,
    last_error    TEXT,

    CONSTRAINT outbox_transport_check CHECK (transport IN ('rabbit', 'kafka'))
);

CREATE INDEX idx_outbox_unpublished ON outbox (id) WHERE published_at IS NULL;

CREATE TABLE processed_messages (
    consumer     VARCHAR(64) NOT NULL,
    message_id   UUID        NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (consumer, message_id)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS processed_messages;
DROP TABLE IF EXISTS outbox;
-- +goose StatementEnd
