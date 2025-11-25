package providers

import (
	"context"

	"github.com/midia/aione/internal/providers/dto"
)

// Provider represents an upstream AI provider capable of responding to health
// probes and future inference requests.
type Provider interface {
	Name() string
	Capabilities() Capabilities
	Health(ctx context.Context) error
	TextGenerate(ctx context.Context, req dto.TextReq) (dto.TextResp, error)
	ImageGenerate(ctx context.Context, req dto.ImageReq) (dto.ImageResp, error)
	ImageEdit(ctx context.Context, req dto.ImageEditReq) (dto.ImageResp, error)
	VideoGenerate(ctx context.Context, req dto.VideoReq) (dto.VideoResp, error)
	SpeechToText(ctx context.Context, req dto.STTReq) (dto.STTResp, error)
	TextToSpeech(ctx context.Context, req dto.TTSReq) (dto.TTSResp, error)
	Embeddings(ctx context.Context, req dto.EmbeddingsReq) (dto.EmbeddingsResp, error)
	Moderation(ctx context.Context, req dto.ModerationReq) (dto.ModerationResp, error)
}

// CapabilityName standardizes capability identifiers shared across APIs.
type CapabilityName string

const (
	CapabilityText         CapabilityName = "text"
	CapabilityImage        CapabilityName = "image"
	CapabilityImageEdit    CapabilityName = "image_edit"
	CapabilityVideo        CapabilityName = "video"
	CapabilitySpeechToText CapabilityName = "speech_to_text"
	CapabilityTextToSpeech CapabilityName = "text_to_speech"
	CapabilityEmbeddings   CapabilityName = "embeddings"
	CapabilityModeration   CapabilityName = "moderation"
)

// Capabilities describe which modalities a provider supports along with
// optional soft limits and qualitative attributes used for routing decisions.
type Capabilities struct {
	TextGeneration  bool                 `json:"text_generation"`
	ImageGeneration bool                 `json:"image_generation"`
	ImageEditing    bool                 `json:"image_editing"`
	VideoGeneration bool                 `json:"video_generation"`
	SpeechToText    bool                 `json:"speech_to_text"`
	TextToSpeech    bool                 `json:"text_to_speech"`
	Embeddings      bool                 `json:"embeddings"`
	Moderation      bool                 `json:"moderation"`
	Limits          Limits               `json:"limits"`
	Attributes      CapabilityAttributes `json:"attributes"`
}

// Limits express provider-specific ceilings.
type Limits struct {
	MaxTextTokens          int    `json:"max_text_tokens,omitempty"`
	MaxImageResolution     string `json:"max_image_resolution,omitempty"`
	MaxEmbeddingDimensions int    `json:"max_embedding_dimensions,omitempty"`
}

// CapabilityAttributes capture qualitative routing guidance.
type CapabilityAttributes struct {
	CostScore    int     `json:"cost_score,omitempty"`
	LatencyScore int     `json:"latency_score,omitempty"`
	QualityScore int     `json:"quality_score,omitempty"`
	RateLimitRPS float64 `json:"rate_limit_rps,omitempty"`
}

// ModelDescriptor describes a provider model and the capability it serves.
type ModelDescriptor struct {
	Provider    string            `json:"provider"`
	Name        string            `json:"name"`
	Capability  CapabilityName    `json:"capability"`
	Description string            `json:"description,omitempty"`
	Default     bool              `json:"default,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// ModelLister exposes the list of models available for a provider.
type ModelLister interface {
	ListModels(ctx context.Context) ([]ModelDescriptor, error)
}
