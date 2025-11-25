package gemini

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/genai"

	"github.com/midia/aione/internal/providers"
	"github.com/midia/aione/internal/providers/dto"
)

func TestNewProviderRequiresAPIKey(t *testing.T) {
	if _, err := NewProvider(Config{}); err == nil {
		t.Fatal("expected error for missing api key")
	}
}

func TestTextGenerateChoosesVisionModel(t *testing.T) {
	textModel := &stubModel{response: textResponse("hello")}
	visionModel := &stubModel{response: textResponse("vision")}
	prov := newStubProvider(t, Config{APIKey: "key"}, map[string]*stubModel{
		defaultTextModel:   textModel,
		defaultVisionModel: visionModel,
	}, &stubEmbedding{})

	resp, err := prov.TextGenerate(context.Background(), dto.TextReq{Prompt: "hi", MaxTokens: 99})
	if err != nil {
		t.Fatalf("text generate: %v", err)
	}
	if resp.Content != "hello" {
		t.Fatalf("unexpected content %s", resp.Content)
	}
	if textModel.maxTokens != 99 {
		t.Fatalf("expected max tokens propagated")
	}

	resp, err = prov.TextGenerate(context.Background(), dto.TextReq{
		Prompt: "describe",
		Media:  []dto.MediaInput{{Data: base64.StdEncoding.EncodeToString([]byte("img")), MIMEType: "image/png"}},
	})
	if err != nil {
		t.Fatalf("vision text generate: %v", err)
	}
	if resp.Content != "vision" {
		t.Fatalf("unexpected vision content %s", resp.Content)
	}
	if len(visionModel.parts) != 2 {
		t.Fatalf("expected prompt + media parts, got %d", len(visionModel.parts))
	}
}

func TestImageGenerateReturnsDataURL(t *testing.T) {
	imageBlob := genai.Blob{MIMEType: "image/png", Data: []byte("fake")}
	imgModel := &stubModel{response: blobResponse(imageBlob)}
	prov := newStubProvider(t, Config{APIKey: "key", ImageModel: "image"}, map[string]*stubModel{
		"image": imgModel,
	}, &stubEmbedding{})

	resp, err := prov.ImageGenerate(context.Background(), dto.ImageReq{Prompt: "draw"})
	if err != nil {
		t.Fatalf("image generate: %v", err)
	}
	if resp.URL == "" || resp.URL[:5] != "data:" {
		t.Fatalf("expected data url, got %s", resp.URL)
	}
}

func TestImageEditUsesBase64Inputs(t *testing.T) {
	baseBytes := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00}
	maskBytes := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x01}
	promptModel := &stubModel{response: textResponse("describe the blue car")}
	previewModel := &stubModel{response: blobResponse(genai.Blob{MIMEType: "image/png", Data: []byte("edited")})}
	provIface := newStubProvider(t, Config{
		APIKey:                "key",
		ImageModel:            "imagen",
		ImageEditPromptModel:  "prompt-model",
		ImageEditPreviewModel: "preview-model",
	}, map[string]*stubModel{
		"prompt-model":  promptModel,
		"preview-model": previewModel,
	}, &stubEmbedding{})
	prov := provIface.(*Provider)
	resp, err := prov.ImageEdit(context.Background(), dto.ImageEditReq{
		Prompt:      "Troque o carro vermelho por um azul",
		ImageBase64: base64.StdEncoding.EncodeToString(baseBytes),
		MaskBase64:  base64.StdEncoding.EncodeToString(maskBytes),
		Size:        "1024x1024",
	})
	if err != nil {
		t.Fatalf("image edit: %v", err)
	}
	expected := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("edited"))
	if resp.URL != expected {
		t.Fatalf("unexpected url %s", resp.URL)
	}
	if len(promptModel.parts) != 4 {
		t.Fatalf("expected text + base + mask text + mask inline, got %d", len(promptModel.parts))
	}
	if promptModel.parts[1].InlineData == nil || !bytes.Equal(promptModel.parts[1].InlineData.Data, baseBytes) {
		t.Fatalf("base image bytes were not forwarded to prompt stage")
	}
	if promptModel.parts[3].InlineData == nil || !bytes.Equal(promptModel.parts[3].InlineData.Data, maskBytes) {
		t.Fatalf("mask image bytes were not forwarded to prompt stage")
	}
	if got := promptModel.parts[0].Text; !strings.Contains(got, "Troque o carro vermelho") {
		t.Fatalf("prompt builder text missing user instruction: %s", got)
	}
	if len(previewModel.parts) != 1 {
		t.Fatalf("expected preview model to receive a single prompt part")
	}
	previewPrompt := previewModel.parts[0].Text
	if !strings.Contains(previewPrompt, "describe the blue car") {
		t.Fatalf("preview prompt missing rewritten text: %s", previewPrompt)
	}
	if !strings.Contains(previewPrompt, "Aspect ratio: 1:1") {
		t.Fatalf("preview prompt missing aspect ratio hint: %s", previewPrompt)
	}
}

func TestImageEditDownloadsImageFromURL(t *testing.T) {
	baseData := []byte("img-bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(baseData)
	}))
	t.Cleanup(server.Close)
	promptModel := &stubModel{response: textResponse("detailed prompt")}
	previewModel := &stubModel{response: blobResponse(genai.Blob{MIMEType: "image/png", Data: []byte("result")})}
	provIface := newStubProvider(t, Config{
		APIKey:                "key",
		ImageModel:            "model",
		ImageEditPromptModel:  "prompt-model",
		ImageEditPreviewModel: "preview-model",
	}, map[string]*stubModel{
		"prompt-model":  promptModel,
		"preview-model": previewModel,
	}, &stubEmbedding{})
	prov := provIface.(*Provider)
	resp, err := prov.ImageEdit(context.Background(), dto.ImageEditReq{
		Prompt:   "touch up",
		ImageURL: server.URL,
	})
	if err != nil {
		t.Fatalf("image edit: %v", err)
	}
	if resp.URL == "" {
		t.Fatal("expected resulting image url")
	}
	raw := promptModel.parts[1].InlineData
	if raw == nil || !bytes.Equal(raw.Data, baseData) {
		t.Fatalf("downloaded image bytes not passed to prompt stage: %#v", raw)
	}
	if promptModel.parts[0].Text == "" {
		t.Fatalf("missing prompt engineering instructions")
	}
	if !strings.Contains(previewModel.parts[0].Text, "detailed prompt") {
		t.Fatalf("preview prompt missing rewritten text: %s", previewModel.parts[0].Text)
	}
}

func TestImageEditValidatesInput(t *testing.T) {
	provIface := newStubProvider(t, Config{APIKey: "key"}, nil, &stubEmbedding{})
	prov := provIface.(*Provider)
	if _, err := prov.ImageEdit(context.Background(), dto.ImageEditReq{Prompt: ""}); err == nil {
		t.Fatal("expected prompt validation error")
	}
	if _, err := prov.ImageEdit(context.Background(), dto.ImageEditReq{Prompt: "hi"}); err == nil || !strings.Contains(err.Error(), "base image") {
		t.Fatalf("expected base image error, got %v", err)
	}
}

func TestImageEditFailsWhenPromptRewriteEmpty(t *testing.T) {
	promptModel := &stubModel{response: textResponse("")}
	provIface := newStubProvider(t, Config{
		APIKey:               "key",
		ImageEditPromptModel: "prompt-model",
	}, map[string]*stubModel{
		"prompt-model": promptModel,
	}, &stubEmbedding{})
	prov := provIface.(*Provider)
	baseBytes := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00}
	_, err := prov.ImageEdit(context.Background(), dto.ImageEditReq{
		Prompt:      "paint",
		ImageBase64: base64.StdEncoding.EncodeToString(baseBytes),
	})
	if err == nil || !strings.Contains(err.Error(), "failed to build image edit prompt") {
		t.Fatalf("expected prompt rewrite error, got %v", err)
	}
}

func TestVideoGenerateRequiresPrompt(t *testing.T) {
	prov := newStubProvider(t, Config{APIKey: "key"}, map[string]*stubModel{}, &stubEmbedding{})
	if _, err := prov.VideoGenerate(context.Background(), dto.VideoReq{}); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestSpeechToTextDownloadsAudio(t *testing.T) {
	model := &stubModel{response: textResponse("transcript")}
	audioSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write([]byte("RIFFDATA"))
	}))
	t.Cleanup(audioSrv.Close)

	prov := newStubProvider(t, Config{APIKey: "key", TranscriptionModel: "stt"}, map[string]*stubModel{
		"stt": model,
	}, &stubEmbedding{})

	resp, err := prov.SpeechToText(context.Background(), dto.STTReq{AudioURL: audioSrv.URL})
	if err != nil {
		t.Fatalf("speech to text: %v", err)
	}
	if resp.Transcript != "transcript" {
		t.Fatalf("unexpected transcript %s", resp.Transcript)
	}
	if len(model.parts) != 1 {
		t.Fatalf("expected audio blob part")
	}
}

func TestEmbeddingsReturnsVectors(t *testing.T) {
	emb := &stubEmbedding{responses: []*genai.EmbedContentResponse{
		{Embeddings: []*genai.ContentEmbedding{{Values: []float32{0.1, 0.2}}}},
		{Embeddings: []*genai.ContentEmbedding{{Values: []float32{0.3}}}},
	}}
	prov := newStubProvider(t, Config{APIKey: "key", EmbeddingsModel: "embed"}, map[string]*stubModel{}, emb)

	resp, err := prov.Embeddings(context.Background(), dto.EmbeddingsReq{Inputs: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("embeddings: %v", err)
	}
	if len(resp.Vectors) != 2 || len(resp.Vectors[0]) != 2 || len(resp.Vectors[1]) != 1 {
		t.Fatalf("unexpected vectors %+v", resp.Vectors)
	}
}

func TestHealthUsesOverride(t *testing.T) {
	prov := newStubProviderWithHealth(t, Config{APIKey: "key"}, nil, &stubEmbedding{}, func(context.Context) error {
		return errors.New("unhealthy")
	})
	if err := prov.Health(context.Background()); err == nil || err.Error() != "unhealthy" {
		t.Fatalf("expected override error, got %v", err)
	}
}

func TestListModelsUsesCatalogFromAPI(t *testing.T) {
	provIface := newStubProvider(t, Config{
		APIKey:             "key",
		TextModel:          "gemini-2.0-flash",
		VisionModel:        "gemini-2.0-flash",
		ImageModel:         "imagen-3.0-generate",
		VideoModel:         "gemini-video",
		TranscriptionModel: "audio-transcriber",
		EmbeddingsModel:    "text-embedding-004",
	}, nil, &stubEmbedding{})
	prov := provIface.(*Provider)
	sample := []*genai.Model{
		{
			Name:             "models/gemini-2.0-flash",
			Description:      "Multimodal",
			DisplayName:      "Gemini 2.0 Flash",
			Version:          "2",
			InputTokenLimit:  1234,
			OutputTokenLimit: 2048,
			SupportedActions: []string{"Generate_Content", "generate_videos", "embed_content"},
			Labels: map[string]string{
				"tier": "public",
			},
		},
		{
			Name:             "imagen-3.0-generate",
			Description:      "Images",
			SupportedActions: []string{"generate-images"},
		},
		{
			Name:             "audio-transcriber",
			Description:      "Audio",
			SupportedActions: []string{"speech_to_text"},
		},
	}
	prov.modelListFn = func(context.Context, *genai.Client) ([]*genai.Model, error) {
		return sample, nil
	}
	models, err := prov.ListModels(context.Background())
	if err != nil {
		t.Fatalf("list models: %v", err)
	}
	if len(models) != 5 {
		t.Fatalf("expected 5 descriptors, got %d", len(models))
	}
	text := findModel(models, "gemini-2.0-flash", providers.CapabilityText)
	if text == nil || !text.Default {
		t.Fatalf("expected text default flag")
	}
	if text.Metadata["display_name"] != "Gemini 2.0 Flash" || text.Metadata["input_token_limit"] != "1234" {
		t.Fatalf("unexpected metadata %#v", text.Metadata)
	}
	if got := strings.Join(text.Tags, ","); got != "embed_content,generate_content,generate_videos" {
		t.Fatalf("unexpected tags %s", got)
	}
	video := findModel(models, "gemini-2.0-flash", providers.CapabilityVideo)
	if video == nil || video.Default {
		t.Fatalf("video should not be default")
	}
	embed := findModel(models, "gemini-2.0-flash", providers.CapabilityEmbeddings)
	if embed == nil {
		t.Fatalf("expected embeddings capability")
	}
	image := findModel(models, "imagen-3.0-generate", providers.CapabilityImage)
	if image == nil || !image.Default {
		t.Fatalf("expected default image model")
	}
	stt := findModel(models, "audio-transcriber", providers.CapabilitySpeechToText)
	if stt == nil {
		t.Fatalf("expected speech to text model")
	}
}

func TestListModelsPropagatesErrors(t *testing.T) {
	provIface := newStubProvider(t, Config{APIKey: "key"}, nil, &stubEmbedding{})
	prov := provIface.(*Provider)
	prov.modelListFn = func(context.Context, *genai.Client) ([]*genai.Model, error) {
		return nil, errors.New("catalog failure")
	}
	if _, err := prov.ListModels(context.Background()); err == nil || err.Error() != "catalog failure" {
		t.Fatalf("expected catalog failure, got %v", err)
	}
}

func newStubProvider(t *testing.T, cfg Config, models map[string]*stubModel, emb *stubEmbedding) providers.Provider {
	return newStubProviderWithHealth(t, cfg, models, emb, func(context.Context) error { return nil })
}

func newStubProviderWithHealth(t *testing.T, cfg Config, models map[string]*stubModel, emb *stubEmbedding, health func(context.Context) error) providers.Provider {
	t.Helper()
	if cfg.APIKey == "" {
		cfg.APIKey = "key"
	}
	factory := func(name string) generativeModel {
		if models == nil {
			return &stubModel{response: textResponse("ok")}
		}
		if m, ok := models[name]; ok {
			return m
		}
		return &stubModel{response: textResponse("ok")}
	}
	if emb == nil {
		emb = &stubEmbedding{}
	}
	prov, err := NewProvider(
		cfg,
		withClientFactory(func(context.Context, *genai.ClientConfig) (*genai.Client, error) { return &genai.Client{}, nil }),
		withModelFactories(factory, func(string) embeddingModel { return emb }, health),
	)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	return prov
}

type stubModel struct {
	response    *genai.GenerateContentResponse
	err         error
	parts       []genai.Part
	maxTokens   int32
	temperature float32
}

func (s *stubModel) GenerateContent(ctx context.Context, parts ...genai.Part) (*genai.GenerateContentResponse, error) {
	s.parts = append([]genai.Part(nil), parts...)
	return s.response, s.err
}

func (s *stubModel) SetMaxOutputTokens(tokens int32) {
	s.maxTokens = tokens
}

func (s *stubModel) SetTemperature(temp float32) {
	s.temperature = temp
}

type stubEmbedding struct {
	responses []*genai.EmbedContentResponse
	err       error
	calls     int
}

func (s *stubEmbedding) EmbedContent(ctx context.Context, parts ...genai.Part) (*genai.EmbedContentResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.calls < len(s.responses) {
		resp := s.responses[s.calls]
		s.calls++
		return resp, nil
	}
	return &genai.EmbedContentResponse{Embeddings: []*genai.ContentEmbedding{{Values: []float32{}}}}, nil
}

func textResponse(text string) *genai.GenerateContentResponse {
	return &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{Content: &genai.Content{Parts: []*genai.Part{genai.NewPartFromText(text)}}},
		},
	}
}

func blobResponse(blob genai.Blob) *genai.GenerateContentResponse {
	return &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{Content: &genai.Content{Parts: []*genai.Part{genai.NewPartFromBytes(blob.Data, blob.MIMEType)}}},
		},
	}
}

func findModel(models []providers.ModelDescriptor, name string, capability providers.CapabilityName) *providers.ModelDescriptor {
	for i := range models {
		if models[i].Name == name && models[i].Capability == capability {
			return &models[i]
		}
	}
	return nil
}

func withClientFactory(factory clientFactory) Option {
	return func(p *Provider) {
		if factory != nil {
			p.clientFactory = factory
		}
	}
}

func withModelFactories(mFactory func(string) generativeModel, eFactory func(string) embeddingModel, health func(context.Context) error) Option {
	return func(p *Provider) {
		if mFactory != nil {
			p.modelFactory = mFactory
		}
		if eFactory != nil {
			p.embedFactory = eFactory
		}
		if health != nil {
			p.healthCheck = health
		}
	}
}
