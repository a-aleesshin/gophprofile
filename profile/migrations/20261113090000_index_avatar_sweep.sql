-- +goose Up
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_avatars_pending;

CREATE INDEX idx_avatars_sweep ON avatars (status, updated_at)
    WHERE deleted_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_avatars_sweep;

CREATE INDEX idx_avatars_pending ON avatars (updated_at)
    WHERE status IN ('pending', 'processing');
-- +goose StatementEnd
