package gemini

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/genai"

	"github.com/midia/aione/internal/providers"
	"github.com/midia/aione/internal/providers/dto"
)

const (
	defaultBaseURL               = "https://generativelanguage.googleapis.com/v1beta"
	defaultTextModel             = "gemini-2.5-flash"
	defaultVisionModel           = "gemini-2.5-pro"
	defaultImageModel            = "imagen-3.0-generate"
	defaultImageEditPreviewModel = "gemini-3-pro-image-preview"
	defaultVideoModel            = "gemini-2.5-flash"
	defaultTranscriptionModel    = "gemini-2.5-flash"
	defaultEmbeddingsModel       = "text-embedding-004"
	defaultTimeout               = 30 * time.Second
	defaultMaxRetries            = 2
	defaultMaxUploadMB           = 50
)

var defaultAllowedMIME = []string{
	"image/png",
	"image/jpeg",
	"video/mp4",
	"audio/wav",
	"audio/mpeg",
	"audio/mp3",
}

var geminiActionCapabilities = map[string]providers.CapabilityName{
	"generate_content":            providers.CapabilityText,
	"generatecontent":             providers.CapabilityText,
	"generate_content_stream":     providers.CapabilityText,
	"generatecontentstream":       providers.CapabilityText,
	"generate_images":             providers.CapabilityImage,
	"generateimages":              providers.CapabilityImage,
	"generate_videos":             providers.CapabilityVideo,
	"generatevideos":              providers.CapabilityVideo,
	"generate_videos_from_source": providers.CapabilityVideo,
	"generatevideosfromsource":    providers.CapabilityVideo,
	"embed_content":               providers.CapabilityEmbeddings,
	"embedcontent":                providers.CapabilityEmbeddings,
	"speech_to_text":              providers.CapabilitySpeechToText,
	"speechtotext":                providers.CapabilitySpeechToText,
	"transcribe":                  providers.CapabilitySpeechToText,
	"audio_to_text":               providers.CapabilitySpeechToText,
	"audiototext":                 providers.CapabilitySpeechToText,
}

var geminiAspectRatios = []struct {
	label string
	value float64
}{
	{label: "1:1", value: 1},
	{label: "3:4", value: 3.0 / 4.0},
	{label: "4:3", value: 4.0 / 3.0},
	{label: "9:16", value: 9.0 / 16.0},
	{label: "16:9", value: 16.0 / 9.0},
}

// Config captures provider wiring.
type Config struct {
	APIKey                string
	BaseURL               string
	TextModel             string
	VisionModel           string
	ImageModel            string
	ImageEditPromptModel  string
	ImageEditPreviewModel string
	VideoModel            string
	TranscriptionModel    string
	EmbeddingsModel       string
	Timeout               time.Duration
	MaxRetries            int
	MaxUploadMB           int
	AllowedMIMETypes      []string
}

type httpClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Option customises provider internals.
type Option func(*Provider)

// WithHTTPClient overrides the HTTP client used for Gemini API calls.
func WithHTTPClient(client httpClient) Option {
	return func(p *Provider) {
		if client == nil {
			return
		}
		p.downloadClient = client
		if hc, ok := client.(*http.Client); ok {
			p.sdkHTTPClient = hc
		}
	}
}

// WithDownloadClient overrides the fetcher used for user-provided media.
func WithDownloadClient(client httpClient) Option {
	return func(p *Provider) {
		if client != nil {
			p.downloadClient = client
		}
	}
}

type clientFactory func(context.Context, *genai.ClientConfig) (*genai.Client, error)
type modelFactory func(string) generativeModel
type embeddingFactory func(string) embeddingModel
type modelListFn func(context.Context, *genai.Client) ([]*genai.Model, error)

// Provider implements the providers.Provider interface for Gemini.
type Provider struct {
	cfg            Config
	caps           providers.Capabilities
	downloadClient httpClient
	sdkHTTPClient  *http.Client

	allowedMIME    map[string]struct{}
	maxUploadBytes int64

	clientFactory clientFactory
	modelFactory  modelFactory
	embedFactory  embeddingFactory
	healthCheck   func(context.Context) error

	client        *genai.Client
	clientConfig  genai.ClientConfig
	retryAttempts int
	modelListFn   modelListFn
}

var _ providers.Provider = (*Provider)(nil)

// NewProvider builds a Gemini provider backed by the google.golang.org/genai SDK.
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
	if cfg.ImageEditPromptModel == "" {
		cfg.ImageEditPromptModel = cfg.VisionModel
	}
	if cfg.ImageEditPreviewModel == "" {
		cfg.ImageEditPreviewModel = defaultImageEditPreviewModel
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
	retryAttempts := cfg.MaxRetries
	if retryAttempts == 0 {
		retryAttempts = defaultMaxRetries
	}
	if cfg.MaxUploadMB <= 0 {
		cfg.MaxUploadMB = defaultMaxUploadMB
	}

	prov := &Provider{
		cfg:           cfg,
		retryAttempts: retryAttempts,
		clientFactory: genai.NewClient,
		caps: providers.Capabilities{
			TextGeneration:  true,
			ImageGeneration: true,
			ImageEditing:    true,
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

	for _, opt := range opts {
		opt(prov)
	}

	if prov.sdkHTTPClient == nil {
		prov.sdkHTTPClient = &http.Client{Timeout: cfg.Timeout}
	}
	if prov.downloadClient == nil {
		prov.downloadClient = prov.sdkHTTPClient
	}

	baseURL, apiVersion := splitBaseAndVersion(cfg.BaseURL)
	timeoutCopy := cfg.Timeout
	prov.clientConfig = genai.ClientConfig{
		APIKey:     cfg.APIKey,
		HTTPClient: prov.sdkHTTPClient,
		HTTPOptions: genai.HTTPOptions{
			BaseURL:    baseURL,
			APIVersion: apiVersion,
		},
	}
	if timeoutCopy > 0 {
		prov.clientConfig.HTTPOptions.Timeout = &timeoutCopy
	}

	client, err := prov.clientFactory(context.Background(), prov.cloneClientConfig())
	if err != nil {
		return nil, err
	}
	prov.client = client
	if prov.modelListFn == nil {
		prov.modelListFn = defaultGeminiModelList
	}

	if prov.modelFactory == nil {
		prov.modelFactory = prov.defaultModelFactory
	}
	if prov.embedFactory == nil {
		prov.embedFactory = prov.defaultEmbeddingFactory
	}
	if prov.healthCheck == nil {
		prov.healthCheck = prov.defaultHealthCheck
	}

	allowed := cfg.AllowedMIMETypes
	if len(allowed) == 0 {
		allowed = defaultAllowedMIME
	}
	for _, entry := range allowed {
		mt := strings.TrimSpace(entry)
		if mt == "" {
			continue
		}
		prov.allowedMIME[strings.ToLower(mt)] = struct{}{}
	}

	return prov, nil
}

func (p *Provider) Name() string {
	return "gemini"
}

func (p *Provider) Capabilities() providers.Capabilities {
	return p.caps
}

func (p *Provider) ListModels(ctx context.Context) ([]providers.ModelDescriptor, error) {
	client, cleanup, err := p.clientForContext(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	listFn := p.modelListFn
	if listFn == nil {
		listFn = defaultGeminiModelList
	}
	models, err := listFn(ctx, client)
	if err != nil {
		return nil, err
	}
	descriptors := make([]providers.ModelDescriptor, 0, len(models))
	seen := make(map[string]struct{})
	for _, model := range models {
		if model == nil {
			continue
		}
		name := canonicalGeminiModelName(model.Name)
		if name == "" {
			continue
		}
		actions := normalizeGeminiActions(model.SupportedActions)
		caps := geminiCapabilitiesForModel(name, actions)
		if len(caps) == 0 {
			caps = []providers.CapabilityName{providers.CapabilityText}
		}
		metadata := geminiModelMetadata(model)
		for _, capability := range caps {
			key := name + "|" + string(capability)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			descriptors = append(descriptors, providers.ModelDescriptor{
				Provider:    p.Name(),
				Name:        name,
				Capability:  capability,
				Description: model.Description,
				Default:     p.isDefaultGeminiModel(name, capability),
				Tags:        append([]string(nil), actions...),
				Metadata:    metadata,
			})
		}
	}
	sort.Slice(descriptors, func(i, j int) bool {
		if descriptors[i].Capability == descriptors[j].Capability {
			return descriptors[i].Name < descriptors[j].Name
		}
		return descriptors[i].Capability < descriptors[j].Capability
	})
	return descriptors, nil
}

func (p *Provider) Health(ctx context.Context) error {
	if p.healthCheck == nil {
		return nil
	}
	return p.healthCheck(ctx)
}

func (p *Provider) TextGenerate(ctx context.Context, req dto.TextReq) (dto.TextResp, error) {
	parts, err := p.buildParts(req.Prompt, req.Media)
	if err != nil {
		return dto.TextResp{}, err
	}
	modelName := p.cfg.TextModel
	if len(req.Media) > 0 && p.cfg.VisionModel != "" {
		modelName = p.cfg.VisionModel
	}
	model := p.modelFactory(modelName)
	if req.MaxTokens > 0 {
		model.SetMaxOutputTokens(int32(req.MaxTokens))
	}
	if req.Temperature > 0 {
		model.SetTemperature(req.Temperature)
	}
	var response *genai.GenerateContentResponse
	if err := p.invokeWithRetry(ctx, func(ctx context.Context) error {
		var invokeErr error
		response, invokeErr = model.GenerateContent(ctx, parts...)
		return invokeErr
	}); err != nil {
		return dto.TextResp{}, err
	}
	text := firstText(response)
	if text == "" {
		return dto.TextResp{}, errors.New("gemini: empty text response")
	}
	return dto.TextResp{Content: text}, nil
}

func (p *Provider) ImageGenerate(ctx context.Context, req dto.ImageReq) (dto.ImageResp, error) {
	parts, err := p.buildParts(req.Prompt, req.Media)
	if err != nil {
		return dto.ImageResp{}, err
	}
	model := p.modelFactory(p.cfg.ImageModel)
	var response *genai.GenerateContentResponse
	if err := p.invokeWithRetry(ctx, func(ctx context.Context) error {
		var invokeErr error
		response, invokeErr = model.GenerateContent(ctx, parts...)
		return invokeErr
	}); err != nil {
		return dto.ImageResp{}, err
	}
	if blob := firstBlob(response); blob != nil && len(blob.Data) > 0 {
		mimeType := blob.MIMEType
		if mimeType == "" {
			mimeType = "image/png"
		}
		encoded := base64.StdEncoding.EncodeToString(blob.Data)
		return dto.ImageResp{URL: fmt.Sprintf("data:%s;base64,%s", mimeType, encoded)}, nil
	}
	if uri := firstFileURI(response); uri != "" {
		return dto.ImageResp{URL: uri}, nil
	}
	return dto.ImageResp{}, errors.New("gemini: missing image payload")
}

func (p *Provider) ImageEdit(ctx context.Context, req dto.ImageEditReq) (dto.ImageResp, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return dto.ImageResp{}, errors.New("gemini: prompt is required for image edit")
	}
	baseImage, err := p.loadImageInput(ctx, req.ImageURL, req.ImageBase64, "image")
	if err != nil {
		return dto.ImageResp{}, err
	}
	if baseImage == nil {
		return dto.ImageResp{}, errors.New("gemini: base image is required for edit")
	}
	maskImage, err := p.loadImageInput(ctx, req.MaskURL, req.MaskBase64, "mask")
	if err != nil {
		return dto.ImageResp{}, err
	}
	rewrittenPrompt, err := p.buildImageEditPrompt(ctx, req, baseImage, maskImage)
	if err != nil {
		return dto.ImageResp{}, err
	}
	return p.renderImagePreview(ctx, rewrittenPrompt, req.Size)
}

func (p *Provider) buildImageEditPrompt(ctx context.Context, req dto.ImageEditReq, base, mask *genai.Image) (string, error) {
	modelName := p.cfg.ImageEditPromptModel
	if modelName == "" {
		modelName = p.cfg.VisionModel
	}
	model := p.modelFactory(modelName)
	instruction := imageEditPromptInstruction(req.Prompt, req.Size, mask != nil)
	parts := []genai.Part{
		{Text: instruction},
		{InlineData: &genai.Blob{MIMEType: base.MIMEType, Data: base.ImageBytes}},
	}
	if mask != nil {
		parts = append(parts,
			genai.Part{Text: "The next attachment is a mask that highlights the areas to modify."},
			genai.Part{InlineData: &genai.Blob{MIMEType: mask.MIMEType, Data: mask.ImageBytes}},
		)
	}
	var response *genai.GenerateContentResponse
	if err := p.invokeWithRetry(ctx, func(ctx context.Context) error {
		var invokeErr error
		response, invokeErr = model.GenerateContent(ctx, parts...)
		return invokeErr
	}); err != nil {
		return "", err
	}
	prompt := strings.TrimSpace(firstText(response))
	if prompt == "" {
		return "", errors.New("gemini: failed to build image edit prompt")
	}
	return prompt, nil
}

func imageEditPromptInstruction(userPrompt, size string, hasMask bool) string {
	var sb strings.Builder
	sb.WriteString("You convert image edit requests into detailed prompts for an image generator. Analyze the attached original image and preserve the key subjects, environment, lighting, and camera details while applying the requested change.")
	if hasMask {
		sb.WriteString(" A mask attachment highlights where edits should occur; keep untouched regions consistent with the original image.")
	}
	if ratio := aspectRatioFromSize(size); ratio != "" {
		sb.WriteString(" Target aspect ratio: ")
		sb.WriteString(ratio)
		sb.WriteString(".")
	} else if sz := strings.TrimSpace(size); sz != "" {
		sb.WriteString(" Target approximate size: ")
		sb.WriteString(sz)
		sb.WriteString(".")
	}
	sb.WriteString(" Edit instruction: ")
	sb.WriteString(strings.TrimSpace(userPrompt))
	sb.WriteString(" Respond with a single comprehensive prompt ready for an image model.")
	return sb.String()
}

func (p *Provider) renderImagePreview(ctx context.Context, prompt, size string) (dto.ImageResp, error) {
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		return dto.ImageResp{}, errors.New("gemini: missing prompt for image edit preview")
	}
	modelName := p.cfg.ImageEditPreviewModel
	if modelName == "" {
		modelName = p.cfg.ImageModel
	}
	model := p.modelFactory(modelName)
	previewPrompt := trimmed
	if ratio := aspectRatioFromSize(size); ratio != "" {
		previewPrompt = fmt.Sprintf("%s\n\nAspect ratio: %s", previewPrompt, ratio)
	} else if sz := strings.TrimSpace(size); sz != "" {
		previewPrompt = fmt.Sprintf("%s\n\nTarget size: %s", previewPrompt, sz)
	}
	var response *genai.GenerateContentResponse
	if err := p.invokeWithRetry(ctx, func(ctx context.Context) error {
		var invokeErr error
		response, invokeErr = model.GenerateContent(ctx, genai.Part{Text: previewPrompt})
		return invokeErr
	}); err != nil {
		return dto.ImageResp{}, err
	}
	if blob := firstBlob(response); blob != nil && len(blob.Data) > 0 {
		mimeType := blob.MIMEType
		if mimeType == "" {
			mimeType = "image/png"
		}
		encoded := base64.StdEncoding.EncodeToString(blob.Data)
		return dto.ImageResp{URL: fmt.Sprintf("data:%s;base64,%s", mimeType, encoded)}, nil
	}
	if uri := firstFileURI(response); uri != "" {
		return dto.ImageResp{URL: uri}, nil
	}
	return dto.ImageResp{}, errors.New("gemini: missing image payload")
}

func (p *Provider) VideoGenerate(ctx context.Context, req dto.VideoReq) (dto.VideoResp, error) {
	if strings.TrimSpace(req.Prompt) == "" && len(req.Media) == 0 {
		return dto.VideoResp{}, errors.New("gemini: prompt or media is required")
	}
	parts, err := p.buildParts(req.Prompt, req.Media)
	if err != nil {
		return dto.VideoResp{}, err
	}
	model := p.modelFactory(p.cfg.VideoModel)
	var response *genai.GenerateContentResponse
	if err := p.invokeWithRetry(ctx, func(ctx context.Context) error {
		var invokeErr error
		response, invokeErr = model.GenerateContent(ctx, parts...)
		return invokeErr
	}); err != nil {
		return dto.VideoResp{}, err
	}
	if uri := firstFileURI(response); uri != "" {
		return dto.VideoResp{URL: uri}, nil
	}
	if blob := firstBlob(response); blob != nil && len(blob.Data) > 0 {
		mimeType := blob.MIMEType
		if mimeType == "" {
			mimeType = "video/mp4"
		}
		encoded := base64.StdEncoding.EncodeToString(blob.Data)
		return dto.VideoResp{URL: fmt.Sprintf("data:%s;base64,%s", mimeType, encoded)}, nil
	}
	return dto.VideoResp{}, errors.New("gemini: missing video payload")
}

func (p *Provider) SpeechToText(ctx context.Context, req dto.STTReq) (dto.STTResp, error) {
	if strings.TrimSpace(req.AudioURL) == "" {
		return dto.STTResp{}, errors.New("audio_url is required")
	}
	audioBytes, mimeType, err := p.fetchAudio(ctx, req.AudioURL)
	if err != nil {
		return dto.STTResp{}, err
	}
	part := genai.Part{InlineData: &genai.Blob{MIMEType: mimeType, Data: audioBytes}}
	model := p.modelFactory(p.cfg.TranscriptionModel)
	var response *genai.GenerateContentResponse
	if err := p.invokeWithRetry(ctx, func(ctx context.Context) error {
		var invokeErr error
		response, invokeErr = model.GenerateContent(ctx, part)
		return invokeErr
	}); err != nil {
		return dto.STTResp{}, err
	}
	text := firstText(response)
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
	modelName := p.cfg.EmbeddingsModel
	if strings.TrimSpace(req.Model) != "" {
		modelName = req.Model
	}
	embedder := p.embedFactory(modelName)
	vectors := make([][]float32, 0, len(req.Inputs))
	for _, input := range req.Inputs {
		part := genai.Part{Text: input}
		var response *genai.EmbedContentResponse
		if err := p.invokeWithRetry(ctx, func(ctx context.Context) error {
			var invokeErr error
			response, invokeErr = embedder.EmbedContent(ctx, part)
			return invokeErr
		}); err != nil {
			return dto.EmbeddingsResp{}, err
		}
		if len(response.Embeddings) == 0 || response.Embeddings[0] == nil {
			return dto.EmbeddingsResp{}, errors.New("gemini: empty embeddings response")
		}
		emb := response.Embeddings[0]
		vec := make([]float32, len(emb.Values))
		copy(vec, emb.Values)
		vectors = append(vectors, vec)
	}
	return dto.EmbeddingsResp{Vectors: vectors}, nil
}

func (p *Provider) Moderation(ctx context.Context, req dto.ModerationReq) (dto.ModerationResp, error) {
	return dto.ModerationResp{}, errors.New("gemini: moderation not supported")
}

func (p *Provider) buildParts(prompt string, media []dto.MediaInput) ([]genai.Part, error) {
	parts := make([]genai.Part, 0, 1+len(media))
	if strings.TrimSpace(prompt) != "" {
		parts = append(parts, genai.Part{Text: prompt})
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
		parts = append(parts, genai.Part{Text: ""})
	}
	return parts, nil
}

func (p *Provider) mediaPart(input dto.MediaInput) (genai.Part, bool, error) {
	mimeType := input.MIMEType
	if mimeType == "" {
		mimeType = guessMIMEFromKind(input.Type)
	}
	if mimeType == "" && input.URL != "" {
		mimeType = mime.TypeByExtension(strings.ToLower(extFromURL(input.URL)))
	}
	if mimeType != "" {
		if err := p.validateMIME(mimeType); err != nil {
			return genai.Part{}, false, err
		}
	}
	if input.Data != "" {
		data, err := base64.StdEncoding.DecodeString(input.Data)
		if err != nil {
			return genai.Part{}, false, fmt.Errorf("gemini: invalid base64 media data: %w", err)
		}
		return genai.Part{InlineData: &genai.Blob{MIMEType: mimeType, Data: data}}, true, nil
	}
	if input.URL != "" {
		return genai.Part{FileData: &genai.FileData{MIMEType: mimeType, FileURI: input.URL}}, true, nil
	}
	return genai.Part{}, false, nil
}

func (p *Provider) validateMIME(mimeType string) error {
	if len(p.allowedMIME) == 0 || mimeType == "" {
		return nil
	}
	if _, ok := p.allowedMIME[strings.ToLower(mimeType)]; ok {
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
	reader := io.Reader(resp.Body)
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

func (p *Provider) fetchBinary(ctx context.Context, assetURL, label string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
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
		return nil, "", fmt.Errorf("download %s failed: %s", label, strings.TrimSpace(string(payload)))
	}
	mimeType := resp.Header.Get("Content-Type")
	if mimeType != "" {
		if err := p.validateMIME(mimeType); err != nil {
			return nil, "", err
		}
	}
	reader := io.Reader(resp.Body)
	if p.maxUploadBytes > 0 {
		reader = io.LimitReader(resp.Body, p.maxUploadBytes+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, "", err
	}
	if p.maxUploadBytes > 0 && int64(len(data)) > p.maxUploadBytes {
		return nil, "", fmt.Errorf("gemini: %s exceeds max upload of %d bytes", label, p.maxUploadBytes)
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	return data, mimeType, nil
}

func (p *Provider) loadImageInput(ctx context.Context, url, inline, label string) (*genai.Image, error) {
	inline = strings.TrimSpace(inline)
	url = strings.TrimSpace(url)
	var (
		data     []byte
		mimeType string
		err      error
	)
	if inline != "" {
		data, mimeType, err = decodeImageData(inline)
		if err != nil {
			return nil, fmt.Errorf("gemini: invalid %s data: %w", label, err)
		}
	} else if url != "" {
		data, mimeType, err = p.fetchBinary(ctx, url, label)
		if err != nil {
			return nil, err
		}
	} else {
		return nil, nil
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("gemini: %s data is empty", label)
	}
	if mimeType == "" {
		mimeType = "image/png"
	}
	if err := p.validateMIME(mimeType); err != nil {
		return nil, err
	}
	if p.maxUploadBytes > 0 && int64(len(data)) > p.maxUploadBytes {
		return nil, fmt.Errorf("gemini: %s exceeds max upload of %d bytes", label, p.maxUploadBytes)
	}
	return &genai.Image{ImageBytes: data, MIMEType: mimeType}, nil
}

func decodeImageData(raw string) ([]byte, string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, "", nil
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "data:") {
		return parseDataURL(trimmed)
	}
	data, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, "", err
	}
	mimeType := http.DetectContentType(data)
	return data, mimeType, nil
}

func parseDataURL(raw string) ([]byte, string, error) {
	parts := strings.SplitN(raw, ",", 2)
	if len(parts) != 2 {
		return nil, "", errors.New("invalid data url")
	}
	meta := strings.TrimPrefix(parts[0], "data:")
	mimeType := ""
	for _, section := range strings.Split(meta, ";") {
		if section == "" || strings.EqualFold(section, "base64") {
			continue
		}
		if mimeType == "" {
			mimeType = section
		}
	}
	data, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, "", err
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	return data, mimeType, nil
}

func aspectRatioFromSize(size string) string {
	trimmed := strings.TrimSpace(size)
	if trimmed == "" {
		return ""
	}
	normalized := strings.ToLower(strings.ReplaceAll(trimmed, " ", ""))
	for _, entry := range geminiAspectRatios {
		if normalized == strings.ToLower(entry.label) {
			return entry.label
		}
	}
	parts := strings.Split(normalized, "x")
	if len(parts) != 2 {
		return ""
	}
	width, errW := strconv.Atoi(parts[0])
	height, errH := strconv.Atoi(parts[1])
	if errW != nil || errH != nil || width <= 0 || height <= 0 {
		return ""
	}
	value := float64(width) / float64(height)
	const tolerance = 0.05
	for _, entry := range geminiAspectRatios {
		if math.Abs(value-entry.value) <= tolerance {
			return entry.label
		}
	}
	return ""
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

func splitBaseAndVersion(raw string) (string, string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ""
	}
	trimmed = strings.TrimRight(trimmed, "/")
	idx := strings.LastIndex(trimmed, "/")
	if idx == -1 {
		return trimmed + "/", ""
	}
	suffix := trimmed[idx+1:]
	hasDigit := false
	for _, r := range suffix {
		if r >= '0' && r <= '9' {
			hasDigit = true
			break
		}
	}
	if strings.HasPrefix(suffix, "v") && hasDigit {
		return trimmed[:idx+1], suffix
	}
	return trimmed + "/", ""
}

func (p *Provider) invokeWithRetry(ctx context.Context, fn func(context.Context) error) error {
	attempts := p.retryAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		if err := fn(ctx); err != nil {
			lastErr = err
			if i < attempts-1 {
				time.Sleep(time.Duration(i+1) * 150 * time.Millisecond)
			}
			continue
		}
		return nil
	}
	return lastErr
}

func (p *Provider) defaultModelFactory(name string) generativeModel {
	return &sdkModel{provider: p, name: name}
}

func (p *Provider) defaultEmbeddingFactory(name string) embeddingModel {
	return &sdkEmbedding{provider: p, name: name}
}

func (p *Provider) defaultHealthCheck(ctx context.Context) error {
	if p.client == nil {
		return errors.New("gemini: client not initialised")
	}
	_, err := p.client.Models.Get(ctx, p.cfg.TextModel, nil)
	return err
}

func (p *Provider) cloneClientConfig() *genai.ClientConfig {
	cfg := p.clientConfig
	if cfg.HTTPOptions.Headers != nil {
		headers := make(http.Header, len(cfg.HTTPOptions.Headers))
		for k, v := range cfg.HTTPOptions.Headers {
			headers[k] = append([]string(nil), v...)
		}
		cfg.HTTPOptions.Headers = headers
	}
	return &cfg
}

func (p *Provider) clientForContext(ctx context.Context) (*genai.Client, func(), error) {
	override := strings.TrimSpace(providers.APIKeyFromContext(ctx))
	if override == "" || override == strings.TrimSpace(p.cfg.APIKey) {
		return p.client, func() {}, nil
	}
	cfg := p.cloneClientConfig()
	cfg.APIKey = override
	client, err := p.clientFactory(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {}
	if closer, ok := any(client).(interface{ Close() error }); ok {
		cleanup = func() { _ = closer.Close() }
	}
	return client, cleanup, nil
}

type generativeModel interface {
	GenerateContent(ctx context.Context, parts ...genai.Part) (*genai.GenerateContentResponse, error)
	SetMaxOutputTokens(tokens int32)
	SetTemperature(temp float32)
}

type embeddingModel interface {
	EmbedContent(ctx context.Context, parts ...genai.Part) (*genai.EmbedContentResponse, error)
}

type sdkModel struct {
	provider    *Provider
	name        string
	maxTokens   int32
	temperature float32
}

func (m *sdkModel) SetMaxOutputTokens(tokens int32) {
	m.maxTokens = tokens
}

func (m *sdkModel) SetTemperature(temp float32) {
	m.temperature = temp
}

func (m *sdkModel) GenerateContent(ctx context.Context, parts ...genai.Part) (*genai.GenerateContentResponse, error) {
	client, cleanup, err := m.provider.clientForContext(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	section := toPartPointers(parts)
	if len(section) == 0 {
		section = []*genai.Part{genai.NewPartFromText("")}
	}
	contents := []*genai.Content{{Role: genai.RoleUser, Parts: section}}
	cfg := &genai.GenerateContentConfig{}
	if m.maxTokens > 0 {
		cfg.MaxOutputTokens = m.maxTokens
	}
	if m.temperature != 0 {
		temp := m.temperature
		cfg.Temperature = &temp
	}
	return client.Models.GenerateContent(ctx, m.name, contents, cfg)
}

type sdkEmbedding struct {
	provider *Provider
	name     string
}

func (e *sdkEmbedding) EmbedContent(ctx context.Context, parts ...genai.Part) (*genai.EmbedContentResponse, error) {
	client, cleanup, err := e.provider.clientForContext(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	section := toPartPointers(parts)
	if len(section) == 0 {
		section = []*genai.Part{genai.NewPartFromText("")}
	}
	contents := []*genai.Content{{Role: genai.RoleUser, Parts: section}}
	return client.Models.EmbedContent(ctx, e.name, contents, nil)
}

func toPartPointers(parts []genai.Part) []*genai.Part {
	if len(parts) == 0 {
		return nil
	}
	out := make([]*genai.Part, len(parts))
	for i := range parts {
		part := parts[i]
		out[i] = &part
	}
	return out
}

func firstText(resp *genai.GenerateContentResponse) string {
	if resp == nil {
		return ""
	}
	for _, cand := range resp.Candidates {
		if cand == nil || cand.Content == nil {
			continue
		}
		for _, part := range cand.Content.Parts {
			if part != nil && part.Text != "" {
				return part.Text
			}
		}
	}
	return ""
}

func firstBlob(resp *genai.GenerateContentResponse) *genai.Blob {
	if resp == nil {
		return nil
	}
	for _, cand := range resp.Candidates {
		if cand == nil || cand.Content == nil {
			continue
		}
		for _, part := range cand.Content.Parts {
			if part != nil && part.InlineData != nil && len(part.InlineData.Data) > 0 {
				return part.InlineData
			}
		}
	}
	return nil
}

func firstFileURI(resp *genai.GenerateContentResponse) string {
	if resp == nil {
		return ""
	}
	for _, cand := range resp.Candidates {
		if cand == nil || cand.Content == nil {
			continue
		}
		for _, part := range cand.Content.Parts {
			if part != nil && part.FileData != nil && part.FileData.FileURI != "" {
				return part.FileData.FileURI
			}
		}
	}
	return ""
}

func defaultGeminiModelList(ctx context.Context, client *genai.Client) ([]*genai.Model, error) {
	if client == nil || client.Models == nil {
		return nil, errors.New("gemini: client not initialised")
	}
	seq := client.Models.All(ctx)
	models := make([]*genai.Model, 0, 16)
	for model, err := range seq {
		if err != nil {
			return nil, err
		}
		models = append(models, model)
	}
	return models, nil
}

func canonicalGeminiModelName(name string) string {
	trimmed := strings.TrimSpace(name)
	return strings.TrimPrefix(trimmed, "models/")
}

func normalizeGeminiActions(actions []string) []string {
	if len(actions) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(actions))
	out := make([]string, 0, len(actions))
	for _, action := range actions {
		normalized := strings.ToLower(strings.TrimSpace(action))
		normalized = strings.ReplaceAll(normalized, "-", "_")
		normalized = strings.ReplaceAll(normalized, " ", "_")
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out
}

func geminiCapabilitiesForModel(name string, actions []string) []providers.CapabilityName {
	if name == "" && len(actions) == 0 {
		return nil
	}
	capSet := make(map[providers.CapabilityName]struct{})
	add := func(cap providers.CapabilityName) {
		if cap == "" {
			return
		}
		capSet[cap] = struct{}{}
	}
	for _, action := range actions {
		if cap, ok := geminiActionCapabilities[action]; ok {
			add(cap)
			continue
		}
		if base := strings.TrimSuffix(action, "_stream"); base != action {
			if cap, ok := geminiActionCapabilities[base]; ok {
				add(cap)
			}
		}
	}
	lower := strings.ToLower(name)
	if strings.Contains(lower, "embed") {
		add(providers.CapabilityEmbeddings)
	}
	if strings.Contains(lower, "imagen") || strings.Contains(lower, "image") {
		add(providers.CapabilityImage)
	}
	if strings.Contains(lower, "video") {
		add(providers.CapabilityVideo)
	}
	if strings.Contains(lower, "audio") || strings.Contains(lower, "speech") || strings.Contains(lower, "transcrib") {
		add(providers.CapabilitySpeechToText)
	}
	if strings.Contains(lower, "gemini") || strings.Contains(lower, "flash") || strings.Contains(lower, "pro") || strings.Contains(lower, "text") {
		add(providers.CapabilityText)
	}
	if len(capSet) == 0 {
		return nil
	}
	out := make([]providers.CapabilityName, 0, len(capSet))
	for cap := range capSet {
		out = append(out, cap)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func geminiModelMetadata(model *genai.Model) map[string]string {
	if model == nil {
		return nil
	}
	metadata := make(map[string]string)
	if model.DisplayName != "" {
		metadata["display_name"] = model.DisplayName
	}
	if model.Version != "" {
		metadata["version"] = model.Version
	}
	if model.InputTokenLimit > 0 {
		metadata["input_token_limit"] = strconv.Itoa(int(model.InputTokenLimit))
	}
	if model.OutputTokenLimit > 0 {
		metadata["output_token_limit"] = strconv.Itoa(int(model.OutputTokenLimit))
	}
	if model.MaxTemperature != 0 {
		metadata["max_temperature"] = strconv.FormatFloat(float64(model.MaxTemperature), 'f', -1, 32)
	}
	if model.TopP != 0 {
		metadata["top_p"] = strconv.FormatFloat(float64(model.TopP), 'f', -1, 32)
	}
	if model.TopK != 0 {
		metadata["top_k"] = strconv.Itoa(int(model.TopK))
	}
	if model.Thinking {
		metadata["thinking"] = "true"
	}
	if len(model.Labels) > 0 {
		for k, v := range model.Labels {
			key := strings.TrimSpace(k)
			if key == "" || v == "" {
				continue
			}
			metadata["label_"+key] = v
		}
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func (p *Provider) isDefaultGeminiModel(name string, capability providers.CapabilityName) bool {
	if name == "" {
		return false
	}
	name = canonicalGeminiModelName(name)
	matches := func(candidate string) bool {
		if candidate == "" {
			return false
		}
		return canonicalGeminiModelName(candidate) == name
	}
	switch capability {
	case providers.CapabilityText:
		return matches(p.cfg.TextModel) || matches(p.cfg.VisionModel)
	case providers.CapabilityImage:
		return matches(p.cfg.ImageModel)
	case providers.CapabilityVideo:
		return matches(p.cfg.VideoModel)
	case providers.CapabilitySpeechToText:
		return matches(p.cfg.TranscriptionModel)
	case providers.CapabilityEmbeddings:
		return matches(p.cfg.EmbeddingsModel)
	default:
		return false
	}
}
