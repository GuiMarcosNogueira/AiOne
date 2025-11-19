package providersessions

import "time"

// Session represents the persistence model stored in the database.
type Session struct {
	UserID          string
	ProviderName    string
	EncryptedKey    []byte
	EncryptionKeyID string
	LastInteraction time.Time
	TotalTokensUsed int64
	Metadata        map[string]any
	ExpiresAt       *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// SessionDetails exposes decrypted data for application consumers.
type SessionDetails struct {
	ProviderName    string         `json:"provider_name"`
	ProviderKey     string         `json:"provider_key"`
	LastInteraction time.Time      `json:"last_interaction"`
	TotalTokensUsed int64          `json:"total_tokens_used"`
	ExpiresAt       *time.Time     `json:"expires_at,omitempty"`
	Metadata        map[string]any `json:"metadata"`
}

// UpsertParams is used to insert or update a provider session secret.
type UpsertParams struct {
	UserID          string
	ProviderName    string
	EncryptedKey    []byte
	EncryptionKeyID string
	Metadata        map[string]any
	ExpiresAt       *time.Time
	LastInteraction time.Time
}

// UsageUpdateParams updates rolling metrics for a session.
type UsageUpdateParams struct {
	UserID          string
	ProviderName    string
	TokensDelta     int64
	LastInteraction time.Time
	Metadata        map[string]any
	ExpiresAt       *time.Time
}
