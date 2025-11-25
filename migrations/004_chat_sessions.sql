-- +migrate Up
DROP TABLE IF EXISTS user_provider_sessions;

CREATE TABLE IF NOT EXISTS chat_sessions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider_name TEXT NOT NULL,
    title TEXT NOT NULL,
    session_metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    expires_at TIMESTAMPTZ,
    last_interaction TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    total_tokens_used BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    archived_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS chat_sessions_user_idx
    ON chat_sessions (user_id, provider_name, archived_at);

ALTER TABLE user_context_history
    ADD COLUMN IF NOT EXISTS session_id UUID REFERENCES chat_sessions(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS user_context_history_session_idx
    ON user_context_history (session_id, created_at);

-- +migrate Down
DROP INDEX IF EXISTS user_context_history_session_idx;
ALTER TABLE user_context_history DROP COLUMN IF EXISTS session_id;
DROP INDEX IF EXISTS chat_sessions_user_idx;
DROP TABLE IF EXISTS chat_sessions;
