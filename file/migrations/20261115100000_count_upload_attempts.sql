-- +goose Up
-- +goose StatementBegin
ALTER TABLE uploads ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;

ALTER TABLE uploads ADD CONSTRAINT uploads_attempts_check CHECK (attempts >= 0);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE uploads DROP CONSTRAINT IF EXISTS uploads_attempts_check;

ALTER TABLE uploads DROP COLUMN IF EXISTS attempts;
-- +goose StatementEnd
