-- +migrate Up
CREATE TABLE IF NOT EXISTS user_provider_sessions (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider_name TEXT NOT NULL,
    encrypted_provider_key BYTEA NOT NULL,
    encryption_key_id TEXT NOT NULL,
    last_interaction TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    total_tokens_used BIGINT NOT NULL DEFAULT 0,
    session_metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, provider_name)
);

CREATE INDEX IF NOT EXISTS user_provider_sessions_provider_idx
    ON user_provider_sessions (provider_name);

-- +migrate Down
DROP TABLE IF EXISTS user_provider_sessions;
