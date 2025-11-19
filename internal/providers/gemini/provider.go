package gemini

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/midia/aione/internal/providers"
	"github.com/midia/aione/internal/providers/dto"
)

const (
	defaultBaseURL            = "https://generativelanguage.googleapis.com/v1beta"
	defaultTextModel          = "gemini-2.5-flash"
	defaultVisionModel        = "gemini-2.5-pro"
	defaultImageModel         = "imagen-3.0-generate"
	defaultVideoModel         = "gemini-2.5-flash"
	defaultTranscriptionModel = "gemini-2.5-flash"
	defaultEmbeddingsModel    = "text-embedding-004"
	defaultTimeout            = 30 * time.Second
	defaultMaxRetries         = 2
	defaultMaxUploadMB        = 50
)

var defaultAllowedMIME = []string{
	"image/png",
	"image/jpeg",
	"video/mp4",
	"audio/wav",
	"audio/mpeg",
	"audio/mp3",
}

type Config struct {
	APIKey             string
	BaseURL            string
	TextModel          string
	VisionModel        string
	ImageModel         string
	VideoModel         string
	TranscriptionModel string
	EmbeddingsModel    string
	Timeout            time.Duration
	MaxRetries         int
	MaxUploadMB        int
	AllowedMIMETypes   []string
}

type httpClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type Option func(*Provider)

func WithHTTPClient(client httpClient) Option {
	return func(p *Provider) {
		p.httpClient = client
	}
}

func WithDownloadClient(client httpClient) Option {
	return func(p *Provider) {
		p.downloadClient = client
	}
}

type Provider struct {
	cfg            Config
	httpClient     httpClient
	downloadClient httpClient
	baseURL        string
	caps           providers.Capabilities
	allowedMIME    map[string]struct{}
	maxUploadBytes int64
}

func NewProvider(cfg Config, opts ...Option) (providers.Provider, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("gemini: missing api key")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.TextModel == "" {
		cfg.TextModel = defaultTextModel
	}
	if cfg.VisionModel == "" {
		cfg.VisionModel = defaultVisionModel
	}
	if cfg.ImageModel == "" {
		cfg.ImageModel = defaultImageModel
	}
	if cfg.VideoModel == "" {
		cfg.VideoModel = defaultVideoModel
	}
	if cfg.TranscriptionModel == "" {
		cfg.TranscriptionModel = defaultTranscriptionModel
	}
	if cfg.EmbeddingsModel == "" {
		cfg.EmbeddingsModel = defaultEmbeddingsModel
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = defaultMaxRetries
	}
	if cfg.MaxUploadMB <= 0 {
		cfg.MaxUploadMB = defaultMaxUploadMB
	}
	allowed := cfg.AllowedMIMETypes
	if len(allowed) == 0 {
		allowed = defaultAllowedMIME
	}
	provider := &Provider{
		cfg:     cfg,
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		caps: providers.Capabilities{
			TextGeneration:  true,
			ImageGeneration: true,
			VideoGeneration: true,
			SpeechToText:    true,
			TextToSpeech:    false,
			Embeddings:      true,
			Moderation:      false,
			Limits: providers.Limits{
				MaxTextTokens:          200000,
				MaxImageResolution:     "4096x4096",
				MaxEmbeddingDimensions: 3072,
			},
			Attributes: providers.CapabilityAttributes{
				CostScore:    6,
				LatencyScore: 5,
				QualityScore: 8,
				RateLimitRPS: 8,
			},
		},
		allowedMIME:    make(map[string]struct{}),
		maxUploadBytes: int64(cfg.MaxUploadMB) * 1024 * 1024,
	}
	for _, mimeType := range allowed {
		mt := strings.TrimSpace(mimeType)
		if mt != "" {
			provider.allowedMIME[strings.ToLower(mt)] = struct{}{}
		}
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

func (p *Provider) Name() string {
	return "gemini"
}

func (p *Provider) Capabilities() providers.Capabilities {
	return p.caps
}

func (p *Provider) Health(ctx context.Context) error {
	path := "/models"
	query := url.Values{}
	query.Set("pageSize", "1")
	return p.doJSON(ctx, http.MethodGet, path, query, nil, &struct{}{})
}

func (p *Provider) TextGenerate(ctx context.Context, req dto.TextReq) (dto.TextResp, error) {
	contents, err := p.buildContents(req.Prompt, req.Media)
	if err != nil {
		return dto.TextResp{}, err
	}
	payload := generateContentRequest{
		Contents: contents,
		GenerationConfig: generationConfig{
			MaxOutputTokens: req.MaxTokens,
			Temperature:     req.Temperature,
		},
	}
	var response generateContentResponse
	model := p.cfg.TextModel
	if len(req.Media) > 0 && p.cfg.VisionModel != "" {
		model = p.cfg.VisionModel
	}
	path := fmt.Sprintf("/models/%s:generateContent", model)
	if err := p.doJSON(ctx, http.MethodPost, path, nil, payload, &response); err != nil {
		return dto.TextResp{}, err
	}
	text := response.FirstText()
	if text == "" {
		return dto.TextResp{}, errors.New("gemini: empty text response")
	}
	return dto.TextResp{Content: text}, nil
}

func (p *Provider) ImageGenerate(ctx context.Context, req dto.ImageReq) (dto.ImageResp, error) {
	payload := imageRequest{Prompt: req.Prompt}
	if req.Size != "" {
		payload.Size = req.Size
	}
	if len(req.Media) > 0 {
		refs, err := p.buildContents("", req.Media)
		if err != nil {
			return dto.ImageResp{}, err
		}
		payload.Reference = refs
	}
	var response imageResponse
	path := fmt.Sprintf("/models/%s:generateImage", p.cfg.ImageModel)
	if err := p.doJSON(ctx, http.MethodPost, path, nil, payload, &response); err != nil {
		return dto.ImageResp{}, err
	}
	if len(response.Images) == 0 {
		return dto.ImageResp{}, errors.New("gemini: empty image response")
	}
	img := response.Images[0]
	url := img.URI
	if url == "" && img.Data != "" {
		mimeType := img.MIMEType
		if mimeType == "" {
			mimeType = "image/png"
		}
		url = fmt.Sprintf("data:%s;base64,%s", mimeType, img.Data)
	}
	if url == "" {
		return dto.ImageResp{}, errors.New("gemini: missing image payload")
	}
	return dto.ImageResp{URL: url}, nil
}

func (p *Provider) VideoGenerate(ctx context.Context, req dto.VideoReq) (dto.VideoResp, error) {
	contents, err := p.buildContents(req.Prompt, req.Media)
	if err != nil {
		return dto.VideoResp{}, err
	}
	payload := generateContentRequest{Contents: contents}
	if req.DurationSeconds > 0 {
		payload.GenerationConfig = generationConfig{DurationSeconds: req.DurationSeconds}
	}
	var response mediaResponse
	path := fmt.Sprintf("/models/%s:generateContent", p.cfg.VideoModel)
	if err := p.doJSON(ctx, http.MethodPost, path, nil, payload, &response); err != nil {
		return dto.VideoResp{}, err
	}
	if len(response.Media) == 0 {
		return dto.VideoResp{}, errors.New("gemini: empty video response")
	}
	media := response.Media[0]
	url := media.URI
	if url == "" && media.Data != "" {
		mimeType := media.MIMEType
		if mimeType == "" {
			mimeType = "video/mp4"
		}
		url = fmt.Sprintf("data:%s;base64,%s", mimeType, media.Data)
	}
	if url == "" {
		return dto.VideoResp{}, errors.New("gemini: missing video payload")
	}
	return dto.VideoResp{URL: url}, nil
}

func (p *Provider) SpeechToText(ctx context.Context, req dto.STTReq) (dto.STTResp, error) {
	if strings.TrimSpace(req.AudioURL) == "" {
		return dto.STTResp{}, errors.New("audio_url is required")
	}
	audioBytes, mimeType, err := p.fetchAudio(ctx, req.AudioURL)
	if err != nil {
		return dto.STTResp{}, err
	}
	inline := inlineData{
		MIMEType: mimeType,
		Data:     base64.StdEncoding.EncodeToString(audioBytes),
	}
	part := geminiPart{InlineData: &inline}
	payload := generateContentRequest{
		Contents: []geminiContent{{Role: "user", Parts: []geminiPart{part}}},
	}
	if req.Language != "" {
		payload.GenerationConfig = generationConfig{LanguageCode: req.Language}
	}
	var response generateContentResponse
	path := fmt.Sprintf("/models/%s:transcribeContent", p.cfg.TranscriptionModel)
	if err := p.doJSON(ctx, http.MethodPost, path, nil, payload, &response); err != nil {
		return dto.STTResp{}, err
	}
	text := response.FirstText()
	if text == "" {
		return dto.STTResp{}, errors.New("gemini: empty transcription response")
	}
	return dto.STTResp{Transcript: text}, nil
}

func (p *Provider) TextToSpeech(ctx context.Context, req dto.TTSReq) (dto.TTSResp, error) {
	return dto.TTSResp{}, errors.New("gemini: text-to-speech not supported")
}

func (p *Provider) Embeddings(ctx context.Context, req dto.EmbeddingsReq) (dto.EmbeddingsResp, error) {
	if len(req.Inputs) == 0 {
		return dto.EmbeddingsResp{}, errors.New("gemini: missing inputs")
	}
	payload := batchEmbedRequest{Model: p.cfg.EmbeddingsModel}
	for _, input := range req.Inputs {
		payload.Inputs = append(payload.Inputs, embedContent{
			Contents: []geminiContent{{Role: "user", Parts: []geminiPart{{Text: input}}}},
		})
	}
	var response batchEmbedResponse
	path := fmt.Sprintf("/models/%s:batchEmbedContents", p.cfg.EmbeddingsModel)
	if err := p.doJSON(ctx, http.MethodPost, path, nil, payload, &response); err != nil {
		return dto.EmbeddingsResp{}, err
	}
	if len(response.Embeddings) == 0 {
		return dto.EmbeddingsResp{}, errors.New("gemini: empty embeddings response")
	}
	vectors := make([][]float32, len(response.Embeddings))
	for idx, emb := range response.Embeddings {
		vectors[idx] = emb.Values
	}
	return dto.EmbeddingsResp{Vectors: vectors}, nil
}

func (p *Provider) Moderation(ctx context.Context, req dto.ModerationReq) (dto.ModerationResp, error) {
	return dto.ModerationResp{}, errors.New("gemini: moderation not supported")
}

func (p *Provider) endpoint(path string) string {
	if strings.HasPrefix(path, "http") {
		return path
	}
	if strings.HasPrefix(path, "/") {
		return p.baseURL + path
	}
	return p.baseURL + "/" + path
}

func (p *Provider) doJSON(ctx context.Context, method, relPath string, query url.Values, payload any, target any) error {
	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return err
		}
	}
	endpoint := p.endpoint(relPath)
	if len(query) > 0 {
		encoded := query.Encode()
		if strings.Contains(endpoint, "?") {
			endpoint += "&" + encoded
		} else {
			endpoint += "?" + encoded
		}
	}
	attempts := p.cfg.MaxRetries + 1
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
		if err != nil {
			return err
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("X-Goog-Api-Key", p.cfg.APIKey)
		resp, err := p.httpClient.Do(req)
		if err != nil {
			lastErr = err
		} else {
			func() {
				defer resp.Body.Close()
				if resp.StatusCode >= http.StatusBadRequest {
					raw, _ := io.ReadAll(resp.Body)
					lastErr = fmt.Errorf("gemini %s %s failed: %s", method, relPath, strings.TrimSpace(string(raw)))
					return
				}
				if target == nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					lastErr = nil
					return
				}
				decoder := json.NewDecoder(resp.Body)
				lastErr = decoder.Decode(target)
			}()
		}
		if lastErr == nil {
			return nil
		}
		if attempt < attempts-1 {
			time.Sleep(time.Duration(attempt+1) * 150 * time.Millisecond)
		}
	}
	return lastErr
}

func (p *Provider) buildContents(prompt string, media []dto.MediaInput) ([]geminiContent, error) {
	parts := make([]geminiPart, 0, 1+len(media))
	if strings.TrimSpace(prompt) != "" {
		parts = append(parts, geminiPart{Text: prompt})
	}
	for _, item := range media {
		part, ok, err := p.mediaPart(item)
		if err != nil {
			return nil, err
		}
		if ok {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return []geminiContent{{Role: "user", Parts: []geminiPart{{Text: ""}}}}, nil
	}
	return []geminiContent{{Role: "user", Parts: parts}}, nil
}

func (p *Provider) mediaPart(input dto.MediaInput) (geminiPart, bool, error) {
	mt := input.MIMEType
	if mt == "" {
		mt = guessMIMEFromKind(input.Type)
	}
	if mt == "" && input.URL != "" {
		mt = mime.TypeByExtension(strings.ToLower(extFromURL(input.URL)))
	}
	if mt != "" {
		if err := p.validateMIME(mt); err != nil {
			return geminiPart{}, false, err
		}
	}
	if input.Data != "" {
		return geminiPart{InlineData: &inlineData{MIMEType: mt, Data: input.Data}}, true, nil
	}
	if input.URL != "" {
		return geminiPart{FileData: &fileData{MIMEType: mt, FileURI: input.URL}}, true, nil
	}
	return geminiPart{}, false, nil
}

func (p *Provider) validateMIME(mimeType string) error {
	if len(p.allowedMIME) == 0 || mimeType == "" {
		return nil
	}
	_, ok := p.allowedMIME[strings.ToLower(mimeType)]
	if ok {
		return nil
	}
	return fmt.Errorf("gemini: mime type %s not allowed", mimeType)
}

func (p *Provider) fetchAudio(ctx context.Context, audioURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, audioURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := p.downloadClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		payload, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("download audio failed: %s", strings.TrimSpace(string(payload)))
	}
	mimeType := resp.Header.Get("Content-Type")
	if mimeType != "" {
		if err := p.validateMIME(mimeType); err != nil {
			return nil, "", err
		}
	}
	var reader io.Reader = resp.Body
	if p.maxUploadBytes > 0 {
		reader = io.LimitReader(resp.Body, p.maxUploadBytes+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, "", err
	}
	if p.maxUploadBytes > 0 && int64(len(data)) > p.maxUploadBytes {
		return nil, "", fmt.Errorf("gemini: audio exceeds max upload of %d bytes", p.maxUploadBytes)
	}
	if mimeType == "" {
		mimeType = "audio/wav"
	}
	return data, mimeType, nil
}

func guessMIMEFromKind(kind string) string {
	switch strings.ToLower(kind) {
	case "image":
		return "image/png"
	case "video":
		return "video/mp4"
	case "audio":
		return "audio/wav"
	}
	return ""
}

func extFromURL(raw string) string {
	idx := strings.LastIndex(raw, ".")
	if idx == -1 {
		return ""
	}
	return raw[idx:]
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text       string      `json:"text,omitempty"`
	InlineData *inlineData `json:"inline_data,omitempty"`
	FileData   *fileData   `json:"file_data,omitempty"`
}

type inlineData struct {
	MIMEType string `json:"mime_type,omitempty"`
	Data     string `json:"data,omitempty"`
}

type fileData struct {
	MIMEType string `json:"mime_type,omitempty"`
	FileURI  string `json:"file_uri,omitempty"`
}

type generateContentRequest struct {
	Contents         []geminiContent  `json:"contents"`
	GenerationConfig generationConfig `json:"generationConfig,omitempty"`
}

type generationConfig struct {
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
	Temperature     float32 `json:"temperature,omitempty"`
	DurationSeconds int     `json:"durationSeconds,omitempty"`
	LanguageCode    string  `json:"languageCode,omitempty"`
}

type generateContentResponse struct {
	Candidates []candidate `json:"candidates"`
}

type candidate struct {
	Content geminiContent `json:"content"`
}

func (resp generateContentResponse) FirstText() string {
	for _, cand := range resp.Candidates {
		for _, part := range cand.Content.Parts {
			if part.Text != "" {
				return part.Text
			}
		}
	}
	return ""
}

type imageRequest struct {
	Prompt    string          `json:"prompt,omitempty"`
	Size      string          `json:"size,omitempty"`
	Reference []geminiContent `json:"reference,omitempty"`
}

type imageResponse struct {
	Images []mediaPayload `json:"images"`
}

type mediaResponse struct {
	Media []mediaPayload `json:"media"`
}

type mediaPayload struct {
	URI      string `json:"uri,omitempty"`
	Data     string `json:"data,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
}

type batchEmbedRequest struct {
	Model  string         `json:"model"`
	Inputs []embedContent `json:"requests"`
}

type embedContent struct {
	Contents []geminiContent `json:"contents"`
}

type batchEmbedResponse struct {
	Embeddings []embedding `json:"embeddings"`
}

type embedding struct {
	Values []float32 `json:"values"`
}
