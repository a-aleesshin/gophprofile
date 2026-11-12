-- +goose Up
-- +goose StatementBegin
CREATE INDEX idx_outbox_published ON outbox (published_at)
    WHERE published_at IS NOT NULL;

CREATE INDEX idx_processed_messages_age ON processed_messages (processed_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_processed_messages_age;
DROP INDEX IF EXISTS idx_outbox_published;
-- +goose StatementEnd
