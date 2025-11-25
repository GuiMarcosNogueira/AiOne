//go:build live

package tests

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/midia/aione/internal/providers/dto"
	"github.com/midia/aione/internal/providers/gemini"
	"github.com/midia/aione/internal/providers/openai"
)

func requireEnv(t *testing.T, key string) string {
	t.Helper()
	value := os.Getenv(key)
	if value == "" {
		t.Skipf("environment variable %s is not set; skipping live provider test", key)
	}
	return value
}

func TestOpenAILiveTextGeneration(t *testing.T) {
	apiKey := requireEnv(t, "OPENAI_API_KEY")
	provider, err := openai.NewProvider(openai.Config{APIKey: apiKey, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("create openai provider: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := provider.TextGenerate(ctx, dto.TextReq{Prompt: "Respond with a short greeting."})
	if err != nil {
		t.Fatalf("openai live text generation failed: %v", err)
	}
	if resp.Content == "" {
		t.Fatalf("openai live text generation returned empty content")
	}
}

func TestGeminiLiveTextGeneration(t *testing.T) {
	apiKey := requireEnv(t, "GEMINI_API_KEY")
	provider, err := gemini.NewProvider(gemini.Config{APIKey: apiKey, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("create gemini provider: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := provider.TextGenerate(ctx, dto.TextReq{Prompt: "Respond with a concise greeting."})
	if err != nil {
		t.Fatalf("gemini live text generation failed: %v", err)
	}
	if resp.Content == "" {
		t.Fatalf("gemini live text generation returned empty content")
	}
}
