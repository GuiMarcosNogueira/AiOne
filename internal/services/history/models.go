package history

import (
	"io"
	"time"
)

// Entry represents a persisted chat turn.
type Entry struct {
	ID              int64     `json:"id"`
	UserID          string    `json:"user_id"`
	ProviderName    string    `json:"provider_name"`
	Role            string    `json:"role"`
	Message         string    `json:"message,omitempty"`
	MediaType       string    `json:"media_type,omitempty"`
	MediaPath       string    `json:"media_path,omitempty"`
	TokensEstimated int       `json:"tokens_estimated"`
	CreatedAt       time.Time `json:"created_at"`
}

// SaveMessageInput captures the data required to store a text message.
type SaveMessageInput struct {
	UserID       string
	ProviderName string
	Role         string
	Message      string
	Tokens       int
}

// SaveMediaInput captures the metadata required to store a media reference.
type SaveMediaInput struct {
	UserID       string
	ProviderName string
	Role         string
	MediaType    string
	FileName     string
	Tokens       int
	Content      io.Reader
}
