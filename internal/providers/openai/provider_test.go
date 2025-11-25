package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/midia/aione/internal/providers"
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
	resp := &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
	resp.Header.Set("Content-Type", "application/json")
	return resp
}

func testConfig() Config {
	return Config{
		APIKey:             "token",
		ChatModel:          "chat-model",
		ImageModel:         "image-model",
		VideoModel:         "video-model",
		VideoSize:          "1024x1792",
		TranscriptionModel: "stt-model",
		EmbeddingsModel:    "embed-model",
		ModerationModel:    "mod-model",
		Timeout:            time.Second,
	}
}

func findDescriptor(models []providers.ModelDescriptor, name string, capability providers.CapabilityName) *providers.ModelDescriptor {
	for i := range models {
		if models[i].Name == name && models[i].Capability == capability {
			return &models[i]
		}
	}
	return nil
}

func TestNewProviderRequiresAPIKey(t *testing.T) {
	if _, err := NewProvider(Config{}); err == nil {
		t.Fatal("expected error for missing API key")
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

func TestVideoGenerateReturnsURL(t *testing.T) {
	client := fakeHTTPClient{handler: func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/videos"):
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			if payload["seconds"] != "4" {
				t.Fatalf("expected default seconds, got %v", payload["seconds"])
			}
			if payload["size"] != "1024x1792" {
				t.Fatalf("expected configured size, got %v", payload["size"])
			}
			return newJSONResponse(http.StatusOK, `{"id":"vid_1","status":"queued"}`), nil
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/videos/vid_1"):
			return newJSONResponse(http.StatusOK, `{"id":"vid_1","status":"completed"}`), nil
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/videos/vid_1/content"):
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewBufferString("video-bytes")),
			}
			resp.Header.Set("Content-Type", "video/mp4")
			return resp, nil
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		return nil, nil
	}}
	provider, err := NewProvider(testConfig(), WithHTTPClient(client))
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	resp, err := provider.VideoGenerate(context.Background(), dto.VideoReq{Prompt: "make a video"})
	if err != nil {
		t.Fatalf("video generate: %v", err)
	}
	expected := "data:video/mp4;base64," + base64.StdEncoding.EncodeToString([]byte("video-bytes"))
	if resp.URL != expected {
		t.Fatalf("unexpected video url %s", resp.URL)
	}
}

func TestVideoGenerateSendsDurationAndReferences(t *testing.T) {
	client := fakeHTTPClient{handler: func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/videos"):
			ct := r.Header.Get("Content-Type")
			mediaType, params, err := mime.ParseMediaType(ct)
			if err != nil || mediaType != "multipart/form-data" {
				t.Fatalf("expected multipart content, got %s", ct)
			}
			reader := multipart.NewReader(r.Body, params["boundary"])
			parts := map[string][]byte{}
			for {
				part, err := reader.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("read part: %v", err)
				}
				data, err := io.ReadAll(part)
				if err != nil {
					t.Fatalf("read part data: %v", err)
				}
				parts[part.FormName()] = data
			}
			if string(parts["seconds"]) != "8" {
				t.Fatalf("expected seconds=8, got %s", parts["seconds"])
			}
			if _, ok := parts["input_reference"]; !ok {
				t.Fatalf("missing input_reference part")
			}
			return newJSONResponse(http.StatusOK, `{"id":"vid_ref","status":"completed"}`), nil
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/videos/vid_ref/content"):
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewBuffer([]byte("clip"))),
			}
			resp.Header.Set("Content-Type", "video/mp4")
			return resp, nil
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		return nil, nil
	}}
	provider, err := NewProvider(testConfig(), WithHTTPClient(client))
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	req := dto.VideoReq{
		Prompt:          "animate",
		DurationSeconds: 8,
		Media: []dto.MediaInput{{
			Type:     "image",
			Data:     base64.StdEncoding.EncodeToString([]byte("img")),
			MIMEType: "image/png",
		}},
	}
	resp, err := provider.VideoGenerate(context.Background(), req)
	if err != nil {
		t.Fatalf("video generate: %v", err)
	}
	if !strings.HasPrefix(resp.URL, "data:video/mp4;base64,") {
		t.Fatalf("expected data url, got %s", resp.URL)
	}
}

func TestVideoGenerateRejectsInvalidDuration(t *testing.T) {
	called := false
	client := fakeHTTPClient{handler: func(r *http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("should not be called")
	}}
	provider, err := NewProvider(testConfig(), WithHTTPClient(client))
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	_, err = provider.VideoGenerate(context.Background(), dto.VideoReq{Prompt: "hi", DurationSeconds: 5})
	if err == nil || !strings.Contains(err.Error(), "unsupported video duration") {
		t.Fatalf("expected duration error, got %v", err)
	}
	if called {
		t.Fatal("unexpected http call for invalid duration")
	}
}

func TestVideoGenerateRequiresPrompt(t *testing.T) {
	provider, err := NewProvider(testConfig())
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	if _, err := provider.VideoGenerate(context.Background(), dto.VideoReq{}); err == nil {
		t.Fatal("expected validation error")
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
	if !caps.TextGeneration || !caps.SpeechToText || !caps.VideoGeneration {
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
	if err := provider.Health(context.Background()); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("expected health error, got %v", err)
	}
}

func TestListModelsUsesAPIResponse(t *testing.T) {
	cfg := testConfig()
	cfg.ChatModel = "gpt-4o-mini"
	cfg.ImageModel = "gpt-image-1"
	cfg.VideoModel = "sora-2"
	cfg.TranscriptionModel = "gpt-4o-mini-transcribe"
	cfg.EmbeddingsModel = "text-embedding-3-large"
	cfg.ModerationModel = "omni-moderation-latest"
	provider, err := NewProvider(cfg)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	impl := provider.(*Provider)
	sample := []openai.Model{
		{ID: "gpt-4o-mini", OwnedBy: "openai", Created: 123},
		{ID: "gpt-image-1", OwnedBy: "openai"},
		{ID: "sora-2", OwnedBy: "openai"},
		{ID: "text-embedding-3-large", OwnedBy: "openai"},
		{ID: "gpt-4o-mini-transcribe", OwnedBy: "openai"},
		{ID: "omni-moderation-latest", OwnedBy: "system"},
	}
	impl.modelListFn = func(context.Context, openai.Client, []option.RequestOption) ([]openai.Model, error) {
		return sample, nil
	}
	models, err := impl.ListModels(context.Background())
	if err != nil {
		t.Fatalf("list models: %v", err)
	}
	text := findDescriptor(models, "gpt-4o-mini", providers.CapabilityText)
	if text == nil || !text.Default {
		t.Fatalf("expected default text model: %+v", text)
	}
	if text.Metadata["created"] != "123" {
		t.Fatalf("expected created metadata, got %#v", text.Metadata)
	}
	image := findDescriptor(models, "gpt-image-1", providers.CapabilityImage)
	if image == nil || !image.Default {
		t.Fatalf("expected default image model")
	}
	video := findDescriptor(models, "sora-2", providers.CapabilityVideo)
	if video == nil || !video.Default {
		t.Fatalf("expected default video model")
	}
	embed := findDescriptor(models, "text-embedding-3-large", providers.CapabilityEmbeddings)
	if embed == nil || !embed.Default {
		t.Fatalf("expected default embeddings model")
	}
	stt := findDescriptor(models, "gpt-4o-mini-transcribe", providers.CapabilitySpeechToText)
	if stt == nil || !stt.Default {
		t.Fatalf("expected default transcription model")
	}
	mod := findDescriptor(models, "omni-moderation-latest", providers.CapabilityModeration)
	if mod == nil || !mod.Default {
		t.Fatalf("expected default moderation model")
	}
}

func TestListModelsPropagatesErrors(t *testing.T) {
	provider, err := NewProvider(testConfig())
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	impl := provider.(*Provider)
	impl.modelListFn = func(context.Context, openai.Client, []option.RequestOption) ([]openai.Model, error) {
		return nil, errors.New("catalog")
	}
	if _, err := impl.ListModels(context.Background()); err == nil || !strings.Contains(err.Error(), "catalog") {
		t.Fatalf("expected catalog error, got %v", err)
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
	if _, err := provider.SpeechToText(context.Background(), dto.STTReq{AudioURL: "http://audio"}); err == nil || !strings.Contains(err.Error(), "audio/transcriptions") {
		t.Fatalf("expected transcription error, got %v", err)
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
	if _, _, err := decodeDataURL("invalid"); err == nil {
		t.Fatal("expected error for invalid data url")
	}
}

func TestImageEditUploadsMimeTypes(t *testing.T) {
	imgHeader := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	encoded := base64.StdEncoding.EncodeToString(imgHeader)
	imagePayload := "data:image/png;base64," + encoded
	maskPayload := imagePayload
	var imageCT, maskCT string
	client := fakeHTTPClient{handler: func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/images/edits") {
			return newJSONResponse(http.StatusNotFound, "missing"), nil
		}
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" {
			t.Fatalf("expected multipart request, got %s", r.Header.Get("Content-Type"))
		}
		reader := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("read part: %v", err)
			}
			switch part.FormName() {
			case "image":
				imageCT = part.Header.Get("Content-Type")
			case "mask":
				maskCT = part.Header.Get("Content-Type")
			}
			_, _ = io.Copy(io.Discard, part)
		}
		return newJSONResponse(http.StatusOK, `{"data":[{"url":"http://example/result.png"}]}`), nil
	}}
	provider, err := NewProvider(testConfig(), WithHTTPClient(client))
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	req := dto.ImageEditReq{
		Prompt:      "edit",
		ImageBase64: imagePayload,
		MaskBase64:  maskPayload,
	}
	if _, err := provider.ImageEdit(context.Background(), req); err != nil {
		t.Fatalf("image edit failed: %v", err)
	}
	if imageCT != "image/png" {
		t.Fatalf("expected image/png for image part, got %s", imageCT)
	}
	if maskCT != "image/png" {
		t.Fatalf("expected image/png for mask part, got %s", maskCT)
	}
}

func TestLoadBinaryReturnsMetadata(t *testing.T) {
	assetData := []byte("png-bytes")
	download := fakeHTTPClient{handler: func(r *http.Request) (*http.Response, error) {
		resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(assetData))}
		resp.Header.Set("Content-Type", "image/png")
		return resp, nil
	}}
	provider, err := NewProvider(testConfig(), WithDownloadClient(download), WithHTTPClient(fakeHTTPClient{}))
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	data, mimeType, filename, err := provider.(*Provider).loadBinary(context.Background(), "http://cdn.example/test.png", "", "image")
	if err != nil {
		t.Fatalf("loadBinary failed: %v", err)
	}
	if mimeType != "image/png" {
		t.Fatalf("expected image/png mime type, got %s", mimeType)
	}
	if filename != "test.png" {
		t.Fatalf("expected filename from url, got %s", filename)
	}
	if !bytes.Equal(data, assetData) {
		t.Fatalf("expected data round trip")
	}
}

func TestTextToSpeechUnsupported(t *testing.T) {
	provider, err := NewProvider(testConfig())
	if err != nil {
		t.Fatalf("new provider: %v", err)
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
