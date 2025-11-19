package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/midia/aione/internal/providers/dto"
)

type fakeHTTPClient struct {
	handler func(*http.Request) (*http.Response, error)
}

func (f fakeHTTPClient) Do(r *http.Request) (*http.Response, error) {
	if f.handler == nil {
		return nil, nil
	}
	return f.handler(r)
}

func newJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}

func testConfig() Config {
	return Config{
		APIKey:             "token",
		BaseURL:            "https://example.com/v1",
		ChatModel:          "chat-model",
		ImageModel:         "image-model",
		TranscriptionModel: "stt-model",
		EmbeddingsModel:    "embed-model",
		ModerationModel:    "mod-model",
		Timeout:            time.Second,
	}
}

func TestNewProviderRequiresAPIKey(t *testing.T) {
	if _, err := NewProvider(Config{}); err == nil {
		t.Fatal("expected error for missing API key")
	}
}

func TestAuthorizeAndEndpointHelpers(t *testing.T) {
	prov, err := NewProvider(testConfig())
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example", nil)
	prov.(*Provider).authorize(req)
	if got := req.Header.Get("Authorization"); got != "Bearer token" {
		t.Fatalf("unexpected auth header %s", got)
	}
	if prov.(*Provider).endpoint("https://override") != "https://override" {
		t.Fatalf("expected passthrough endpoint")
	}
}

func TestTextGenerate(t *testing.T) {
	client := fakeHTTPClient{handler: func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		return newJSONResponse(http.StatusOK, `{"choices":[{"message":{"content":"hi"}}]}`), nil
	}}
	provider, err := NewProvider(testConfig(), WithHTTPClient(client))
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	resp, err := provider.TextGenerate(context.Background(), dto.TextReq{Prompt: "hello"})
	if err != nil {
		t.Fatalf("text generate: %v", err)
	}
	if resp.Content != "hi" {
		t.Fatalf("unexpected content %s", resp.Content)
	}
}

func TestTextGenerateMissingContent(t *testing.T) {
	client := fakeHTTPClient{handler: func(r *http.Request) (*http.Response, error) {
		return newJSONResponse(http.StatusOK, `{"choices":[]}`), nil
	}}
	provider, err := NewProvider(testConfig(), WithHTTPClient(client))
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	if _, err := provider.TextGenerate(context.Background(), dto.TextReq{Prompt: "hi"}); err == nil {
		t.Fatal("expected error when response missing content")
	}
}

func TestImageGeneratePrefersBase64(t *testing.T) {
	client := fakeHTTPClient{handler: func(r *http.Request) (*http.Response, error) {
		payload := `{"data":[{"b64_json":"ZmFrZS1pbWFnZQ=="}]}`
		return newJSONResponse(http.StatusOK, payload), nil
	}}
	provider, err := NewProvider(testConfig(), WithHTTPClient(client))
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	resp, err := provider.ImageGenerate(context.Background(), dto.ImageReq{Prompt: "draw"})
	if err != nil {
		t.Fatalf("image generate: %v", err)
	}
	expected := "data:image/png;base64,ZmFrZS1pbWFnZQ=="
	if resp.URL != expected {
		t.Fatalf("expected %s got %s", expected, resp.URL)
	}
}

func TestNameAndCapabilities(t *testing.T) {
	provider, err := NewProvider(testConfig())
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	if provider.Name() != "openai" {
		t.Fatalf("unexpected provider name %s", provider.Name())
	}
	caps := provider.Capabilities()
	if !caps.TextGeneration || !caps.SpeechToText || caps.VideoGeneration {
		t.Fatalf("unexpected capabilities %+v", caps)
	}
}

func TestHealthChecksAPI(t *testing.T) {
	client := fakeHTTPClient{handler: func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			t.Fatalf("unexpected health path: %s", r.URL.Path)
		}
		return newJSONResponse(http.StatusOK, "{}"), nil
	}}
	provider, err := NewProvider(testConfig(), WithHTTPClient(client))
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	if err := provider.Health(context.Background()); err != nil {
		t.Fatalf("health expected success: %v", err)
	}
	client = fakeHTTPClient{handler: func(r *http.Request) (*http.Response, error) {
		return newJSONResponse(http.StatusBadGateway, "unavailable"), nil
	}}
	provider, _ = NewProvider(testConfig(), WithHTTPClient(client))
	if err := provider.Health(context.Background()); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("expected health error, got %v", err)
	}
}

func TestEmbeddingsUsesOverride(t *testing.T) {
	client := fakeHTTPClient{handler: func(r *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["model"] != "custom" {
			t.Fatalf("expected model override, got %v", payload["model"])
		}
		body := `{"data":[{"index":0,"embedding":[0.1,0.2]}]}`
		return newJSONResponse(http.StatusOK, body), nil
	}}
	provider, err := NewProvider(testConfig(), WithHTTPClient(client))
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	resp, err := provider.Embeddings(context.Background(), dto.EmbeddingsReq{
		Inputs: []string{"one"},
		Model:  "custom",
	})
	if err != nil {
		t.Fatalf("embeddings: %v", err)
	}
	if len(resp.Vectors) != 1 || len(resp.Vectors[0]) != 2 {
		t.Fatalf("unexpected vectors %+v", resp.Vectors)
	}
}

func TestModerationAggregatesReasons(t *testing.T) {
	client := fakeHTTPClient{handler: func(r *http.Request) (*http.Response, error) {
		payload := `{"results":[{"flagged":true,"categories":{"violence":true,"hate":false,"self-harm":true}}]}`
		return newJSONResponse(http.StatusOK, payload), nil
	}}
	provider, err := NewProvider(testConfig(), WithHTTPClient(client))
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	resp, err := provider.Moderation(context.Background(), dto.ModerationReq{Input: "text"})
	if err != nil {
		t.Fatalf("moderation: %v", err)
	}
	expected := "self-harm,violence"
	if resp.Reason != expected {
		t.Fatalf("expected reason %s got %s", expected, resp.Reason)
	}
	if !resp.Flagged {
		t.Fatal("expected flagged response")
	}
}

func TestSpeechToTextDataURL(t *testing.T) {
	client := fakeHTTPClient{handler: func(r *http.Request) (*http.Response, error) {
		return newJSONResponse(http.StatusOK, `{"text":"transcript"}`), nil
	}}
	provider, err := NewProvider(testConfig(), WithHTTPClient(client))
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	resp, err := provider.SpeechToText(context.Background(), dto.STTReq{AudioURL: "data:audio/wav;base64,ZmFrZQ=="})
	if err != nil {
		t.Fatalf("speech to text: %v", err)
	}
	if resp.Transcript != "transcript" {
		t.Fatalf("unexpected transcript %s", resp.Transcript)
	}
}

func TestSpeechToTextValidatesInput(t *testing.T) {
	provider, err := NewProvider(testConfig())
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	if _, err := provider.SpeechToText(context.Background(), dto.STTReq{}); err == nil {
		t.Fatal("expected validation error when audio url missing")
	}
}

func TestSpeechToTextHTTPError(t *testing.T) {
	client := fakeHTTPClient{handler: func(r *http.Request) (*http.Response, error) {
		return newJSONResponse(http.StatusBadRequest, "denied"), nil
	}}
	downloader := fakeHTTPClient{handler: func(r *http.Request) (*http.Response, error) {
		return newJSONResponse(http.StatusOK, "audio"), nil
	}}
	provider, err := NewProvider(testConfig(), WithHTTPClient(client), WithDownloadClient(downloader))
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	if _, err := provider.SpeechToText(context.Background(), dto.STTReq{AudioURL: "http://audio"}); err == nil || !strings.Contains(err.Error(), "transcription failed") {
		t.Fatalf("expected transcription error, got %v", err)
	}
}

func TestDoJSONHandlesErrors(t *testing.T) {
	client := fakeHTTPClient{handler: func(r *http.Request) (*http.Response, error) {
		return newJSONResponse(http.StatusInternalServerError, "boom"), nil
	}}
	provider, err := NewProvider(testConfig(), WithHTTPClient(client))
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	if err := provider.(*Provider).doJSON(context.Background(), http.MethodGet, "/test", nil, nil); err == nil {
		t.Fatal("expected error for 500 response")
	}
	client = fakeHTTPClient{handler: func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("network")
	}}
	provider, _ = NewProvider(testConfig(), WithHTTPClient(client))
	if err := provider.(*Provider).doJSON(context.Background(), http.MethodGet, "/test", nil, nil); err == nil || !strings.Contains(err.Error(), "network") {
		t.Fatalf("expected network error, got %v", err)
	}
}

func TestFetchAudioHandlesHTTP(t *testing.T) {
	client := fakeHTTPClient{handler: func(r *http.Request) (*http.Response, error) {
		return newJSONResponse(http.StatusOK, "audio"), nil
	}}
	provider, err := NewProvider(testConfig(), WithDownloadClient(client))
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	data, err := provider.(*Provider).fetchAudio(context.Background(), "http://audio")
	if err != nil || string(data) != "audio" {
		t.Fatalf("expected audio download, got %v %v", data, err)
	}
	client = fakeHTTPClient{handler: func(r *http.Request) (*http.Response, error) {
		return newJSONResponse(http.StatusBadGateway, "fail"), nil
	}}
	provider, _ = NewProvider(testConfig(), WithDownloadClient(client))
	if _, err := provider.(*Provider).fetchAudio(context.Background(), "http://audio"); err == nil {
		t.Fatal("expected download error")
	}
}

func TestDecodeDataURLErrors(t *testing.T) {
	if _, err := decodeDataURL("invalid"); err == nil {
		t.Fatal("expected error for invalid data url")
	}
}

func TestVideoAndTextToSpeechUnsupported(t *testing.T) {
	provider, err := NewProvider(testConfig())
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	if _, err := provider.VideoGenerate(context.Background(), dto.VideoReq{}); err == nil {
		t.Fatal("expected video unsupported error")
	}
	if _, err := provider.TextToSpeech(context.Background(), dto.TTSReq{Text: "hi"}); err == nil {
		t.Fatal("expected tts unsupported error")
	}
}

func TestFlaggedCategoriesEmpty(t *testing.T) {
	if reason := flaggedCategories(map[string]bool{"spam": false}); reason != "" {
		t.Fatalf("expected empty reason, got %s", reason)
	}
}

func TestEmbeddingsReordersVectors(t *testing.T) {
	client := fakeHTTPClient{handler: func(r *http.Request) (*http.Response, error) {
		body := `{"data":[{"index":1,"embedding":[0.2]},{"index":0,"embedding":[0.1]}]}`
		return newJSONResponse(http.StatusOK, body), nil
	}}
	provider, err := NewProvider(testConfig(), WithHTTPClient(client))
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	resp, err := provider.Embeddings(context.Background(), dto.EmbeddingsReq{Inputs: []string{"first", "second"}})
	if err != nil {
		t.Fatalf("embeddings failed: %v", err)
	}
	if resp.Vectors[0][0] != 0.1 || resp.Vectors[1][0] != 0.2 {
		t.Fatalf("expected vectors to be reordered, got %+v", resp.Vectors)
	}
}
