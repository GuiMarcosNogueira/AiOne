package mock

import (
	"context"
	"fmt"
	"time"

	"github.com/midia/aione/internal/providers"
	"github.com/midia/aione/internal/providers/dto"
)

// Provider is a lightweight mock used while real provider SDKs are not wired.
type Provider struct {
	name string
}

// New creates a mock provider that always reports healthy.
func New(name string) providers.Provider {
	return &Provider{name: name}
}

// Name returns the provider identifier.
func (p *Provider) Name() string {
	return p.name
}

// Capabilities reports that the mock provider supports every modality.
func (p *Provider) Capabilities() providers.Capabilities {
	return providers.Capabilities{
		TextGeneration:  true,
		ImageGeneration: true,
		ImageEditing:    true,
		VideoGeneration: true,
		SpeechToText:    true,
		TextToSpeech:    true,
		Embeddings:      true,
		Moderation:      true,
		Attributes: providers.CapabilityAttributes{
			CostScore:    3,
			LatencyScore: 3,
			QualityScore: 4,
			RateLimitRPS: 100,
		},
	}
}

// Health indicates whether the provider is reachable. Mock always succeeds.
func (p *Provider) Health(ctx context.Context) error {
	return nil
}

func (p *Provider) TextGenerate(ctx context.Context, req dto.TextReq) (dto.TextResp, error) {
	return dto.TextResp{Content: fmt.Sprintf("[%s] mock text response for: %s", p.name, req.Prompt)}, nil
}

func (p *Provider) ImageGenerate(ctx context.Context, req dto.ImageReq) (dto.ImageResp, error) {
	return dto.ImageResp{URL: fmt.Sprintf("https://mock.%s/images/%d", p.name, time.Now().Unix())}, nil
}

func (p *Provider) ImageEdit(ctx context.Context, req dto.ImageEditReq) (dto.ImageResp, error) {
	return dto.ImageResp{URL: fmt.Sprintf("https://mock.%s/images/edited/%d", p.name, time.Now().Unix())}, nil
}

func (p *Provider) VideoGenerate(ctx context.Context, req dto.VideoReq) (dto.VideoResp, error) {
	return dto.VideoResp{URL: fmt.Sprintf("https://mock.%s/videos/%d", p.name, time.Now().Unix())}, nil
}

func (p *Provider) SpeechToText(ctx context.Context, req dto.STTReq) (dto.STTResp, error) {
	return dto.STTResp{Transcript: fmt.Sprintf("[%s] transcript for %s", p.name, req.AudioURL)}, nil
}

func (p *Provider) TextToSpeech(ctx context.Context, req dto.TTSReq) (dto.TTSResp, error) {
	return dto.TTSResp{AudioURL: fmt.Sprintf("https://mock.%s/audio/%d", p.name, time.Now().Unix())}, nil
}

func (p *Provider) Embeddings(ctx context.Context, req dto.EmbeddingsReq) (dto.EmbeddingsResp, error) {
	vectors := make([][]float32, len(req.Inputs))
	for i := range req.Inputs {
		vectors[i] = []float32{float32(len(req.Inputs[i]))}
	}
	return dto.EmbeddingsResp{Vectors: vectors}, nil
}

func (p *Provider) Moderation(ctx context.Context, req dto.ModerationReq) (dto.ModerationResp, error) {
	return dto.ModerationResp{Flagged: false}, nil
}
