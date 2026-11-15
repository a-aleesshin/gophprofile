-- +goose Up
-- +goose StatementBegin
ALTER TABLE outbox ADD COLUMN failed_at TIMESTAMPTZ;

DROP INDEX idx_outbox_pending;

CREATE INDEX idx_outbox_pending ON outbox (transport, id)
    WHERE published_at IS NULL AND failed_at IS NULL;

CREATE INDEX idx_outbox_parked ON outbox (failed_at) WHERE failed_at IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_outbox_parked;
DROP INDEX idx_outbox_pending;

CREATE INDEX idx_outbox_pending ON outbox (transport, id) WHERE published_at IS NULL;

ALTER TABLE outbox DROP COLUMN failed_at;
-- +goose StatementEnd
