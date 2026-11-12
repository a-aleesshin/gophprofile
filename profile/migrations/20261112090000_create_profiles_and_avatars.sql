-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE profiles (
    user_id           VARCHAR(255) PRIMARY KEY,
    email             CITEXT UNIQUE,
    email_sha256      CHAR(64) UNIQUE,
    current_avatar_id UUID,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE avatars (
    id            UUID PRIMARY KEY,
    version       INTEGER      NOT NULL DEFAULT 1,
    user_id       VARCHAR(255) NOT NULL REFERENCES profiles (user_id),
    upload_id     UUID UNIQUE,
    file_id       UUID,
    status        VARCHAR(32)  NOT NULL DEFAULT 'pending',
    file_name     VARCHAR(255),
    mime_type     VARCHAR(255),
    size_bytes    BIGINT,
    width         INTEGER,
    height        INTEGER,
    variants      JSONB,
    failed_reason TEXT,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ,

    CONSTRAINT avatars_status_check CHECK (
        status IN ('pending', 'processing', 'ready', 'failed', 'deleted')
    )
);

ALTER TABLE profiles
    ADD CONSTRAINT fk_profiles_current_avatar
    FOREIGN KEY (current_avatar_id) REFERENCES avatars (id);

CREATE INDEX idx_avatars_user ON avatars (user_id, created_at DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX idx_avatars_pending ON avatars (updated_at)
    WHERE status IN ('pending', 'processing');

CREATE UNIQUE INDEX uq_avatars_file ON avatars (file_id)
    WHERE file_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE profiles DROP CONSTRAINT IF EXISTS fk_profiles_current_avatar;

DROP TABLE IF EXISTS avatars;
DROP TABLE IF EXISTS profiles;
-- +goose StatementEnd
