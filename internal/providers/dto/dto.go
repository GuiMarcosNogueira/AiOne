package dto

import "strings"

// ProviderAware exposes the preferred provider for a request.
type ProviderAware interface {
	PreferredProvider() string
}

// ProviderOverride embeds the optional provider hint into DTOs.
type ProviderOverride struct {
	Provider string `json:"provider,omitempty"`
}

// PreferredProvider returns the normalized provider hint.
func (p ProviderOverride) PreferredProvider() string {
	return strings.TrimSpace(p.Provider)
}

// TextReq represents a text generation prompt.
type TextReq struct {
	ProviderOverride
	Prompt      string       `json:"prompt"`
	MaxTokens   int          `json:"max_tokens,omitempty"`
	Temperature float32      `json:"temperature,omitempty"`
	Media       []MediaInput `json:"media,omitempty"`
}

// TextResp contains the generated text result.
type TextResp struct {
	Content string `json:"content"`
}

// ImageReq defines a basic image generation request.
type ImageReq struct {
	ProviderOverride
	Prompt string       `json:"prompt"`
	Size   string       `json:"size,omitempty"`
	Media  []MediaInput `json:"media,omitempty"`
}

// ImageResp returns an URL or base64 payload for the generated image.
type ImageResp struct {
	URL string `json:"url"`
}

// VideoReq captures inputs for requesting video content.
type VideoReq struct {
	ProviderOverride
	Prompt          string       `json:"prompt"`
	DurationSeconds int          `json:"duration_seconds,omitempty"`
	Media           []MediaInput `json:"media,omitempty"`
}

// VideoResp returns a video asset URL or reference.
type VideoResp struct {
	URL string `json:"url"`
}

// STTReq is the speech-to-text request payload.
type STTReq struct {
	ProviderOverride
	AudioURL string `json:"audio_url"`
	Language string `json:"language,omitempty"`
}

// STTResp carries the transcription output.
type STTResp struct {
	Transcript string `json:"transcript"`
}

// TTSReq represents text-to-speech inputs.
type TTSReq struct {
	ProviderOverride
	Text     string `json:"text"`
	Voice    string `json:"voice,omitempty"`
	Language string `json:"language,omitempty"`
}

// TTSResp contains the generated audio artifact reference.
type TTSResp struct {
	AudioURL string `json:"audio_url"`
}

// EmbeddingsReq requests vector embeddings for given inputs.
type EmbeddingsReq struct {
	ProviderOverride
	Inputs []string `json:"inputs"`
	Model  string   `json:"model,omitempty"`
}

// EmbeddingsResp returns the vectorized representation per input.
type EmbeddingsResp struct {
	Vectors [][]float32 `json:"vectors"`
}

// ModerationReq wraps the text to be moderated.
type ModerationReq struct {
	ProviderOverride
	Input string `json:"input"`
}

// ModerationResp reports whether the input violates policy.
type ModerationResp struct {
	Flagged bool   `json:"flagged"`
	Reason  string `json:"reason,omitempty"`
}

// MediaInput represents multimodal input references (inline/base64 or remote URLs).
type MediaInput struct {
	Type     string `json:"type"`
	URL      string `json:"url,omitempty"`
	Data     string `json:"data,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
}
