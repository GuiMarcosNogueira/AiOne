package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/midia/aione/internal/providers"
	"github.com/midia/aione/internal/providers/dto"
)

const (
	defaultBaseURL            = "https://api.openai.com/v1"
	defaultChatModel          = "gpt-4o-mini"
	defaultImageModel         = "gpt-image-1"
	defaultTranscriptionModel = "gpt-4o-mini-transcribe"
	defaultEmbeddingsModel    = "text-embedding-3-large"
	defaultModerationModel    = "omni-moderation-latest"
	defaultTimeout            = 30 * time.Second
)

// Config defines how the OpenAI provider should authenticate and which
// reference models to use per modality.
type Config struct {
	APIKey             string
	BaseURL            string
	ChatModel          string
	ImageModel         string
	TranscriptionModel string
	EmbeddingsModel    string
	ModerationModel    string
	Timeout            time.Duration
}

type httpClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Option allows customizing the provider for testing.
type Option func(*Provider)

// WithHTTPClient injects a custom HTTP client used for OpenAI API calls.
func WithHTTPClient(client httpClient) Option {
	return func(p *Provider) {
		p.httpClient = client
	}
}

// WithDownloadClient injects the HTTP client used to download user-provided
// assets (e.g. audio URLs for transcription).
func WithDownloadClient(client httpClient) Option {
	return func(p *Provider) {
		p.downloadClient = client
	}
}

// Provider implements the providers.Provider interface for OpenAI.
type Provider struct {
	cfg            Config
	httpClient     httpClient
	downloadClient httpClient
	caps           providers.Capabilities
	baseURL        string
}

// NewProvider builds a Provider using the supplied configuration.
func NewProvider(cfg Config, opts ...Option) (providers.Provider, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("openai: missing API key")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.ChatModel == "" {
		cfg.ChatModel = defaultChatModel
	}
	if cfg.ImageModel == "" {
		cfg.ImageModel = defaultImageModel
	}
	if cfg.TranscriptionModel == "" {
		cfg.TranscriptionModel = defaultTranscriptionModel
	}
	if cfg.EmbeddingsModel == "" {
		cfg.EmbeddingsModel = defaultEmbeddingsModel
	}
	if cfg.ModerationModel == "" {
		cfg.ModerationModel = defaultModerationModel
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	provider := &Provider{
		cfg:     cfg,
		baseURL: cfg.BaseURL,
		caps: providers.Capabilities{
			TextGeneration:  true,
			ImageGeneration: true,
			VideoGeneration: false,
			SpeechToText:    true,
			TextToSpeech:    false,
			Embeddings:      true,
			Moderation:      true,
			Limits: providers.Limits{
				MaxTextTokens:          128000,
				MaxImageResolution:     "2048x2048",
				MaxEmbeddingDimensions: 3072,
			},
			Attributes: providers.CapabilityAttributes{
				CostScore:    7,
				LatencyScore: 4,
				QualityScore: 9,
				RateLimitRPS: 12,
			},
		},
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.httpClient == nil {
		provider.httpClient = &http.Client{Timeout: cfg.Timeout}
	}
	if provider.downloadClient == nil {
		provider.downloadClient = provider.httpClient
	}
	return provider, nil
}

// Name returns "openai" for logging and routing.
func (p *Provider) Name() string {
	return "openai"
}

// Capabilities reports which modalities the provider supports.
func (p *Provider) Capabilities() providers.Capabilities {
	return p.caps
}

// Health checks whether the API key can list models.
func (p *Provider) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint("/models"), nil)
	if err != nil {
		return err
	}
	p.authorize(req)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("openai health check failed: %s", strings.TrimSpace(string(body)))
	}
	return nil
}

func (p *Provider) TextGenerate(ctx context.Context, req dto.TextReq) (dto.TextResp, error) {
	payload := chatRequest{
		Model: p.cfg.ChatModel,
		Messages: []chatMessage{
			{Role: "system", Content: "You are a helpful AI assistant."},
			{Role: "user", Content: req.Prompt},
		},
	}
	if req.MaxTokens > 0 {
		payload.MaxTokens = req.MaxTokens
	}
	if req.Temperature > 0 {
		payload.Temperature = req.Temperature
	}
	var response chatResponse
	if err := p.doJSON(ctx, http.MethodPost, "/chat/completions", payload, &response); err != nil {
		return dto.TextResp{}, err
	}
	if len(response.Choices) == 0 || response.Choices[0].Message.Content == "" {
		return dto.TextResp{}, errors.New("openai: empty chat response")
	}
	return dto.TextResp{Content: response.Choices[0].Message.Content}, nil
}

func (p *Provider) ImageGenerate(ctx context.Context, req dto.ImageReq) (dto.ImageResp, error) {
	payload := imageRequest{
		Model:  p.cfg.ImageModel,
		Prompt: req.Prompt,
	}
	if req.Size != "" {
		payload.Size = req.Size
	}
	var response imageResponse
	if err := p.doJSON(ctx, http.MethodPost, "/images/generations", payload, &response); err != nil {
		return dto.ImageResp{}, err
	}
	if len(response.Data) == 0 {
		return dto.ImageResp{}, errors.New("openai: empty image response")
	}
	url := response.Data[0].URL
	if url == "" && response.Data[0].B64JSON != "" {
		url = "data:image/png;base64," + response.Data[0].B64JSON
	}
	if url == "" {
		return dto.ImageResp{}, errors.New("openai: missing image payload")
	}
	return dto.ImageResp{URL: url}, nil
}

func (p *Provider) VideoGenerate(ctx context.Context, req dto.VideoReq) (dto.VideoResp, error) {
	return dto.VideoResp{}, errors.New("openai: video generation not supported")
}

func (p *Provider) SpeechToText(ctx context.Context, req dto.STTReq) (dto.STTResp, error) {
	if req.AudioURL == "" {
		return dto.STTResp{}, errors.New("audio_url is required")
	}
	audioBytes, err := p.fetchAudio(ctx, req.AudioURL)
	if err != nil {
		return dto.STTResp{}, err
	}
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	fileWriter, err := writer.CreateFormFile("file", "audio.wav")
	if err != nil {
		return dto.STTResp{}, err
	}
	if _, err := fileWriter.Write(audioBytes); err != nil {
		return dto.STTResp{}, err
	}
	if err := writer.WriteField("model", p.cfg.TranscriptionModel); err != nil {
		return dto.STTResp{}, err
	}
	if req.Language != "" {
		if err := writer.WriteField("language", req.Language); err != nil {
			return dto.STTResp{}, err
		}
	}
	if err := writer.Close(); err != nil {
		return dto.STTResp{}, err
	}
	reqBody, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint("/audio/transcriptions"), body)
	if err != nil {
		return dto.STTResp{}, err
	}
	p.authorize(reqBody)
	reqBody.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := p.httpClient.Do(reqBody)
	if err != nil {
		return dto.STTResp{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		payload, _ := io.ReadAll(resp.Body)
		return dto.STTResp{}, fmt.Errorf("openai transcription failed: %s", strings.TrimSpace(string(payload)))
	}
	var transcription transcriptionResponse
	if err := json.NewDecoder(resp.Body).Decode(&transcription); err != nil {
		return dto.STTResp{}, err
	}
	return dto.STTResp{Transcript: transcription.Text}, nil
}

func (p *Provider) TextToSpeech(ctx context.Context, req dto.TTSReq) (dto.TTSResp, error) {
	return dto.TTSResp{}, errors.New("openai: text-to-speech not supported")
}

func (p *Provider) Embeddings(ctx context.Context, req dto.EmbeddingsReq) (dto.EmbeddingsResp, error) {
	model := p.cfg.EmbeddingsModel
	if req.Model != "" {
		model = req.Model
	}
	payload := embeddingsRequest{
		Model: model,
		Input: req.Inputs,
	}
	var response embeddingsResponse
	if err := p.doJSON(ctx, http.MethodPost, "/embeddings", payload, &response); err != nil {
		return dto.EmbeddingsResp{}, err
	}
	vectors := make([][]float32, len(req.Inputs))
	for _, item := range response.Data {
		if item.Index >= 0 && item.Index < len(vectors) {
			vectors[item.Index] = item.Embedding
		}
	}
	return dto.EmbeddingsResp{Vectors: vectors}, nil
}

func (p *Provider) Moderation(ctx context.Context, req dto.ModerationReq) (dto.ModerationResp, error) {
	payload := moderationRequest{
		Model: p.cfg.ModerationModel,
		Input: req.Input,
	}
	var response moderationResponse
	if err := p.doJSON(ctx, http.MethodPost, "/moderations", payload, &response); err != nil {
		return dto.ModerationResp{}, err
	}
	if len(response.Results) == 0 {
		return dto.ModerationResp{}, errors.New("openai: empty moderation response")
	}
	result := response.Results[0]
	return dto.ModerationResp{
		Flagged: result.Flagged,
		Reason:  flaggedCategories(result.Categories),
	}, nil
}

func (p *Provider) endpoint(path string) string {
	if strings.HasPrefix(path, "http") {
		return path
	}
	return p.baseURL + path
}

func (p *Provider) authorize(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
}

func (p *Provider) doJSON(ctx context.Context, method, relPath string, payload any, target any) error {
	var body io.Reader
	if payload != nil {
		buf := &bytes.Buffer{}
		if err := json.NewEncoder(buf).Encode(payload); err != nil {
			return err
		}
		body = buf
	}
	req, err := http.NewRequestWithContext(ctx, method, p.endpoint(relPath), body)
	if err != nil {
		return err
	}
	p.authorize(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("openai %s %s failed: %s", method, relPath, strings.TrimSpace(string(msg)))
	}
	if target == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

func (p *Provider) fetchAudio(ctx context.Context, audioURL string) ([]byte, error) {
	if strings.HasPrefix(audioURL, "data:") {
		return decodeDataURL(audioURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, audioURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.downloadClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		payload, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("download audio failed: %s", strings.TrimSpace(string(payload)))
	}
	return io.ReadAll(resp.Body)
}

func decodeDataURL(raw string) ([]byte, error) {
	parts := strings.SplitN(raw, ",", 2)
	if len(parts) != 2 {
		return nil, errors.New("invalid data url")
	}
	return base64.StdEncoding.DecodeString(parts[1])
}

func flaggedCategories(categories map[string]bool) string {
	if len(categories) == 0 {
		return ""
	}
	var flagged []string
	for name, flaggedVal := range categories {
		if flaggedVal {
			flagged = append(flagged, name)
		}
	}
	sort.Strings(flagged)
	return strings.Join(flagged, ",")
}

// DTOs for OpenAI responses.
type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float32       `json:"temperature,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type imageRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Size   string `json:"size,omitempty"`
}

type imageResponse struct {
	Data []struct {
		URL     string `json:"url"`
		B64JSON string `json:"b64_json"`
	} `json:"data"`
}

type transcriptionResponse struct {
	Text string `json:"text"`
}

type embeddingsRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingsResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

type moderationRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type moderationResponse struct {
	Results []struct {
		Flagged    bool            `json:"flagged"`
		Categories map[string]bool `json:"categories"`
	} `json:"results"`
}
