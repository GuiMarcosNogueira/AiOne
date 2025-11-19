-- +migrate Up
CREATE TABLE IF NOT EXISTS user_context_history (
	id BIGSERIAL PRIMARY KEY,
	user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	provider_name TEXT NOT NULL,
	role TEXT NOT NULL,
	message TEXT,
	media_type TEXT,
	media_path TEXT,
	tokens_estimated INT NOT NULL DEFAULT 0,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS user_context_history_idx
	ON user_context_history (user_id, provider_name, created_at);

-- +migrate Down
DROP TABLE IF EXISTS user_context_history;
