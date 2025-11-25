package providersessions

import "time"

// Session represents a persisted conversational session per user/provider.
type Session struct {
	ID              string
	UserID          string
	ProviderName    string
	Title           string
	Metadata        map[string]any
	ExpiresAt       *time.Time
	LastInteraction time.Time
	TotalTokensUsed int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ArchivedAt      *time.Time
}

// SessionDetails exposes session metadata for API consumers.
type SessionDetails struct {
	ID              string         `json:"id"`
	ProviderName    string         `json:"provider_name"`
	Title           string         `json:"title"`
	LastInteraction time.Time      `json:"last_interaction"`
	TotalTokensUsed int64          `json:"total_tokens_used"`
	ExpiresAt       *time.Time     `json:"expires_at,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	ArchivedAt      *time.Time     `json:"archived_at,omitempty"`
}

// CreateParams defines the payload required to persist a new session.
type CreateParams struct {
	ID           string
	UserID       string
	ProviderName string
	Title        string
	Metadata     map[string]any
	ExpiresAt    *time.Time
}

// UsageUpdateParams updates rolling metrics for a session.
type UsageUpdateParams struct {
	SessionID       string
	UserID          string
	ProviderName    string
	TokensDelta     int64
	LastInteraction time.Time
	Metadata        map[string]any
	ExpiresAt       *time.Time
}

// ListParams filters session lookups.
type ListParams struct {
	UserID          string
	ProviderName    string
	Limit           int
	IncludeArchived bool
}
