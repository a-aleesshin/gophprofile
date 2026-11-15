-- +goose Up
-- +goose StatementBegin
CREATE TABLE uploads (
    id              UUID PRIMARY KEY,
    version         INTEGER      NOT NULL DEFAULT 1,
    namespace       VARCHAR(64)  NOT NULL,
    owner_ref       VARCHAR(255) NOT NULL,
    idempotency_key VARCHAR(255) NOT NULL,
    original_name   VARCHAR(255) NOT NULL,
    staging_key     VARCHAR(512) NOT NULL UNIQUE,
    declared_mime   VARCHAR(255),
    max_size_bytes  BIGINT       NOT NULL,
    status          VARCHAR(32)  NOT NULL DEFAULT 'pending',
    file_id         UUID         REFERENCES files (id),
    expires_at      TIMESTAMPTZ  NOT NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),

    CONSTRAINT uploads_status_check CHECK (
        status IN ('pending', 'uploaded', 'expired', 'aborted')
    )
);

CREATE UNIQUE INDEX uq_uploads_idem ON uploads (namespace, idempotency_key);
CREATE INDEX idx_uploads_pending ON uploads (expires_at) WHERE status = 'pending';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS uploads;
-- +goose StatementEnd
