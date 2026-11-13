-- +goose Up
-- +goose StatementBegin
CREATE TABLE files (
    id              UUID PRIMARY KEY,
    version         INTEGER      NOT NULL DEFAULT 1,
    namespace       VARCHAR(64)  NOT NULL,
    owner_ref       VARCHAR(255) NOT NULL,
    original_name   VARCHAR(255) NOT NULL,
    mime_type       VARCHAR(255) NOT NULL,
    size_bytes      BIGINT       NOT NULL,
    checksum_sha256 CHAR(64),
    width           INT,
    height          INT,
    storage_key     VARCHAR(512) NOT NULL,
    status          VARCHAR(32)  NOT NULL DEFAULT 'received',
    reject_reason   VARCHAR(64),
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ,

    CONSTRAINT files_status_check CHECK (
        status IN ('received', 'processing', 'ready', 'rejected', 'deleting', 'deleted')
    )
);

CREATE INDEX idx_files_owner ON files (namespace, owner_ref) WHERE deleted_at IS NULL;
CREATE INDEX idx_files_status ON files (status, updated_at)
    WHERE status IN ('received', 'processing', 'deleting');
CREATE INDEX idx_files_checksum ON files (namespace, checksum_sha256) WHERE deleted_at IS NULL;

CREATE TABLE file_variants (
    file_id         UUID         NOT NULL REFERENCES files (id) ON DELETE CASCADE,
    name            VARCHAR(32)  NOT NULL,
    ext             VARCHAR(8)   NOT NULL,
    mime_type       VARCHAR(255) NOT NULL,
    width           INT,
    height          INT,
    size_bytes      BIGINT       NOT NULL,
    checksum_sha256 CHAR(64)     NOT NULL,
    storage_key     VARCHAR(512) NOT NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),

    PRIMARY KEY (file_id, name)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS file_variants;
DROP TABLE IF EXISTS files;
-- +goose StatementEnd
