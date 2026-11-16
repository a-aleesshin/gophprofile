-- +goose Up
-- +goose StatementBegin
CREATE INDEX idx_outbox_scope ON outbox (transport, partition_key, id)
    INCLUDE (available_at)
    WHERE published_at IS NULL AND failed_at IS NULL AND partition_key IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_outbox_scope;
-- +goose StatementEnd
