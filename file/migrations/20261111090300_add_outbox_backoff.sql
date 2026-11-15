-- +goose Up
-- +goose StatementBegin
ALTER TABLE outbox ADD COLUMN available_at TIMESTAMPTZ NOT NULL DEFAULT now();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE outbox DROP COLUMN available_at;
-- +goose StatementEnd
