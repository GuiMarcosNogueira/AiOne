package tests

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/midia/aione/internal/providers"
	"github.com/midia/aione/internal/providers/dto"
)

// stubProvider implements providers.Provider for integration-style tests in this package.
type stubProvider struct {
	name      string
	healthErr error
	videoResp dto.VideoResp
	videoErr  error
}

func (s *stubProvider) Name() string { return s.name }

func (s *stubProvider) Capabilities() providers.Capabilities {
	return providers.Capabilities{VideoGeneration: true, Attributes: providers.CapabilityAttributes{RateLimitRPS: 10}}
}

func (s *stubProvider) Health(ctx context.Context) error { return s.healthErr }

func (s *stubProvider) TextGenerate(ctx context.Context, req dto.TextReq) (dto.TextResp, error) {
	return dto.TextResp{}, nil
}

func (s *stubProvider) ImageGenerate(ctx context.Context, req dto.ImageReq) (dto.ImageResp, error) {
	return dto.ImageResp{}, nil
}

func (s *stubProvider) VideoGenerate(ctx context.Context, req dto.VideoReq) (dto.VideoResp, error) {
	if s.videoErr != nil {
		return dto.VideoResp{}, s.videoErr
	}
	// When no explicit response provided, create a predictable URL for assertions.
	if s.videoResp.URL == "" {
		return dto.VideoResp{URL: "https://stub.example/video.mp4"}, nil
	}
	return s.videoResp, nil
}

func (s *stubProvider) SpeechToText(ctx context.Context, req dto.STTReq) (dto.STTResp, error) {
	return dto.STTResp{}, nil
}

func (s *stubProvider) TextToSpeech(ctx context.Context, req dto.TTSReq) (dto.TTSResp, error) {
	return dto.TTSResp{}, nil
}

func (s *stubProvider) Embeddings(ctx context.Context, req dto.EmbeddingsReq) (dto.EmbeddingsResp, error) {
	return dto.EmbeddingsResp{}, nil
}

func (s *stubProvider) Moderation(ctx context.Context, req dto.ModerationReq) (dto.ModerationResp, error) {
	return dto.ModerationResp{}, nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func waitForCondition(timeout time.Duration, fn func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fn()
}
