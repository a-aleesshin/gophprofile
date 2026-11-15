-- +goose Up
-- +goose StatementBegin
DROP INDEX idx_outbox_unpublished;

CREATE INDEX idx_outbox_pending ON outbox (transport, id) WHERE published_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_outbox_pending;

CREATE INDEX idx_outbox_unpublished ON outbox (id) WHERE published_at IS NULL;
-- +goose StatementEnd
