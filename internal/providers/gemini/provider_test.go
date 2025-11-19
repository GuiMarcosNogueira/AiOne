package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/midia/aione/internal/providers/dto"
)

func TestNewProviderRequiresAPIKey(t *testing.T) {
	_, err := NewProvider(Config{})
	if err == nil {
		t.Fatalf("expected error when api key missing")
	}
}

func TestTextGenerateChoosesModels(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []any{
				map[string]any{
					"content": map[string]any{
						"parts": []any{map[string]any{"text": "ok"}},
					},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	provider, err := NewProvider(Config{APIKey: "key", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	if _, err := provider.TextGenerate(context.Background(), dto.TextReq{Prompt: "hello"}); err != nil {
		t.Fatalf("text generate: %v", err)
	}
	if _, err := provider.TextGenerate(context.Background(), dto.TextReq{
		Prompt: "describe",
		Media:  []dto.MediaInput{{Type: "image", URL: "https://cdn/test.png", MIMEType: "image/png"}},
	}); err != nil {
		t.Fatalf("text generate vision: %v", err)
	}

	if len(paths) != 2 {
		t.Fatalf("expected two requests, got %d", len(paths))
	}
	if paths[0] != "/models/gemini-2.5-flash:generateContent" {
		t.Fatalf("unexpected path %s", paths[0])
	}
	if paths[1] != "/models/gemini-2.5-pro:generateContent" {
		t.Fatalf("vision path mismatch: %s", paths[1])
	}
}

func TestImageGenerateReturnsDataURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models/imagen-3.0-generate:generateImage" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		resp := map[string]any{
			"images": []any{
				map[string]any{"mimeType": "image/png", "data": "YmFzZTY0"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	provider, err := NewProvider(Config{APIKey: "key", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	resp, err := provider.ImageGenerate(context.Background(), dto.ImageReq{Prompt: "sunset"})
	if err != nil {
		t.Fatalf("image generate: %v", err)
	}
	if resp.URL == "" {
		t.Fatal("expected data url")
	}
}

func TestVideoGenerateReturnsURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"media": []any{
				map[string]any{"uri": "https://video.example/video.mp4"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	provider, err := NewProvider(Config{APIKey: "key", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	resp, err := provider.VideoGenerate(context.Background(), dto.VideoReq{Prompt: "animate"})
	if err != nil {
		t.Fatalf("video generate: %v", err)
	}
	if resp.URL != "https://video.example/video.mp4" {
		t.Fatalf("unexpected video url %s", resp.URL)
	}
}

func TestSpeechToTextDownloadsAudio(t *testing.T) {
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"candidates": []any{
				map[string]any{
					"content": map[string]any{
						"parts": []any{map[string]any{"text": "transcript"}},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(apiSrv.Close)

	audioSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write([]byte("RIFFDATA"))
	}))
	t.Cleanup(audioSrv.Close)

	provider, err := NewProvider(Config{APIKey: "key", BaseURL: apiSrv.URL})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	resp, err := provider.SpeechToText(context.Background(), dto.STTReq{AudioURL: audioSrv.URL + "/audio.wav"})
	if err != nil {
		t.Fatalf("stt: %v", err)
	}
	if resp.Transcript != "transcript" {
		t.Fatalf("unexpected transcript %s", resp.Transcript)
	}
}

func TestEmbeddingsBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"embeddings": []any{
				map[string]any{"values": []any{0.1, 0.2}},
				map[string]any{"values": []any{0.3}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	provider, err := NewProvider(Config{APIKey: "key", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	resp, err := provider.Embeddings(context.Background(), dto.EmbeddingsReq{Inputs: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("embeddings: %v", err)
	}
	if len(resp.Vectors) != 2 {
		t.Fatalf("expected vectors for each input")
	}
}
