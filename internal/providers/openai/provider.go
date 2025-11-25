package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"

	"github.com/midia/aione/internal/providers"
	"github.com/midia/aione/internal/providers/dto"
)

const (
	defaultChatModel          = "gpt-4o-mini"
	defaultImageModel         = "gpt-image-1"
	defaultVideoModel         = "sora-2"
	defaultVideoSize          = "720x1280"
	defaultTranscriptionModel = "gpt-4o-mini-transcribe"
	defaultEmbeddingsModel    = "text-embedding-3-large"
	defaultModerationModel    = "omni-moderation-latest"
	defaultTimeout            = 30 * time.Second
	openAIVideosPath          = "videos"
)

// Config defines how the OpenAI provider should authenticate and which
// reference models to use per modality.
type Config struct {
	APIKey             string
	ChatModel          string
	ImageModel         string
	VideoModel         string
	VideoSize          string
	TranscriptionModel string
	EmbeddingsModel    string
	ModerationModel    string
	Timeout            time.Duration
}

type httpClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type Option func(*providerOptions)

type providerOptions struct {
	requestOptions []option.RequestOption
	downloadClient httpClient
}

type modelListFn func(context.Context, openai.Client, []option.RequestOption) ([]openai.Model, error)

// WithHTTPClient injects a custom HTTP client used for OpenAI API calls.

func WithHTTPClient(client httpClient) Option {
	return func(o *providerOptions) {
		o.requestOptions = append(o.requestOptions, option.WithHTTPClient(client))
		if o.downloadClient == nil {
			o.downloadClient = client
		}
	}
}

// WithDownloadClient injects the HTTP client used to download user-provided
// assets (e.g. audio URLs for transcription).
func WithDownloadClient(client httpClient) Option {
	return func(o *providerOptions) {
		o.downloadClient = client
	}
}

// WithRequestOptions forwards custom request options to the OpenAI SDK client.
func WithRequestOptions(opts ...option.RequestOption) Option {
	return func(o *providerOptions) {
		o.requestOptions = append(o.requestOptions, opts...)
	}
}

// Provider implements the providers.Provider interface for OpenAI.
type Provider struct {
	cfg            Config
	client         openai.Client
	downloadClient httpClient
	caps           providers.Capabilities
	modelListFn    modelListFn
}

// NewProvider builds a Provider using the supplied configuration.
func NewProvider(cfg Config, opts ...Option) (providers.Provider, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("openai: missing API key")
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
	if cfg.VideoModel == "" {
		cfg.VideoModel = defaultVideoModel
	}
	if cfg.VideoSize == "" {
		cfg.VideoSize = defaultVideoSize
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
	optState := providerOptions{}
	for _, opt := range opts {
		opt(&optState)
	}
	if optState.downloadClient == nil {
		optState.downloadClient = &http.Client{Timeout: cfg.Timeout}
	}
	requestOptions := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
		option.WithRequestTimeout(cfg.Timeout),
	}
	requestOptions = append(requestOptions, optState.requestOptions...)
	client := openai.NewClient(requestOptions...)
	provider := &Provider{
		cfg:            cfg,
		client:         client,
		downloadClient: optState.downloadClient,
		caps: providers.Capabilities{
			TextGeneration:  true,
			ImageGeneration: true,
			ImageEditing:    true,
			VideoGeneration: true,
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
	if provider.modelListFn == nil {
		provider.modelListFn = defaultOpenAIModelList
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

func (p *Provider) ListModels(ctx context.Context) ([]providers.ModelDescriptor, error) {
	listFn := p.modelListFn
	if listFn == nil {
		listFn = defaultOpenAIModelList
	}
	models, err := listFn(ctx, p.client, p.requestOptions(ctx))
	if err != nil {
		return nil, err
	}
	descriptors := make([]providers.ModelDescriptor, 0, len(models))
	seen := make(map[string]struct{})
	for _, model := range models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		caps := openaiCapabilitiesForModel(id)
		if len(caps) == 0 {
			caps = []providers.CapabilityName{providers.CapabilityText}
		}
		description := ""
		if owner := strings.TrimSpace(model.OwnedBy); owner != "" {
			description = fmt.Sprintf("Owned by %s", owner)
		}
		tags := openaiModelTags(model)
		metadata := openaiModelMetadata(model)
		for _, capability := range caps {
			key := id + "|" + string(capability)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			descriptors = append(descriptors, providers.ModelDescriptor{
				Provider:    p.Name(),
				Name:        id,
				Capability:  capability,
				Description: description,
				Default:     p.isDefaultModel(id, capability),
				Tags:        append([]string(nil), tags...),
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

// Health checks whether the API key can list models.
func (p *Provider) Health(ctx context.Context) error {
	_, err := p.client.Models.List(ctx, p.requestOptions(ctx)...)
	return err
}

func defaultOpenAIModelList(ctx context.Context, client openai.Client, opts []option.RequestOption) ([]openai.Model, error) {
	pager := client.Models.ListAutoPaging(ctx, opts...)
	models := make([]openai.Model, 0, 32)
	for pager.Next() {
		models = append(models, pager.Current())
	}
	if err := pager.Err(); err != nil {
		return nil, err
	}
	return models, nil
}

func openaiCapabilitiesForModel(id string) []providers.CapabilityName {
	name := strings.ToLower(strings.TrimSpace(id))
	if name == "" {
		return nil
	}
	capSet := make(map[providers.CapabilityName]struct{})
	add := func(cap providers.CapabilityName) {
		if cap == "" {
			return
		}
		capSet[cap] = struct{}{}
	}
	if strings.Contains(name, "embedding") {
		add(providers.CapabilityEmbeddings)
	}
	if strings.Contains(name, "moderation") {
		add(providers.CapabilityModeration)
	}
	if strings.Contains(name, "image") || strings.Contains(name, "dall") {
		add(providers.CapabilityImage)
	}
	if strings.Contains(name, "video") || strings.Contains(name, "sora") {
		add(providers.CapabilityVideo)
	}
	if strings.Contains(name, "transcribe") || strings.Contains(name, "whisper") || strings.Contains(name, "audio") && strings.Contains(name, "trans") {
		add(providers.CapabilitySpeechToText)
	}
	if strings.Contains(name, "tts") {
		add(providers.CapabilityTextToSpeech)
	}
	if strings.Contains(name, "gpt") || strings.Contains(name, "omni") || strings.Contains(name, "chatgpt") || strings.Contains(name, "text") || strings.Contains(name, "o1") {
		add(providers.CapabilityText)
	}
	if len(capSet) == 0 {
		add(providers.CapabilityText)
	}
	out := make([]providers.CapabilityName, 0, len(capSet))
	for cap := range capSet {
		out = append(out, cap)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func openaiModelTags(model openai.Model) []string {
	tags := make([]string, 0, 2)
	if owner := strings.TrimSpace(model.OwnedBy); owner != "" {
		tags = append(tags, "owned_by:"+owner)
	}
	if obj := strings.TrimSpace(string(model.Object)); obj != "" {
		tags = append(tags, "object:"+obj)
	}
	return tags
}

func openaiModelMetadata(model openai.Model) map[string]string {
	metadata := make(map[string]string)
	if owner := strings.TrimSpace(model.OwnedBy); owner != "" {
		metadata["owned_by"] = owner
	}
	if model.Created > 0 {
		metadata["created"] = strconv.FormatInt(model.Created, 10)
		metadata["created_iso"] = time.Unix(model.Created, 0).UTC().Format(time.RFC3339)
	}
	if raw := strings.TrimSpace(model.RawJSON()); raw != "" {
		metadata["raw"] = raw
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func (p *Provider) isDefaultModel(name string, capability providers.CapabilityName) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	matches := func(candidate string) bool {
		return strings.EqualFold(strings.TrimSpace(candidate), name)
	}
	switch capability {
	case providers.CapabilityText:
		return matches(p.cfg.ChatModel)
	case providers.CapabilityImage:
		return matches(p.cfg.ImageModel)
	case providers.CapabilityVideo:
		return matches(p.cfg.VideoModel)
	case providers.CapabilitySpeechToText:
		return matches(p.cfg.TranscriptionModel)
	case providers.CapabilityEmbeddings:
		return matches(p.cfg.EmbeddingsModel)
	case providers.CapabilityModeration:
		return matches(p.cfg.ModerationModel)
	default:
		return false
	}
}
func (p *Provider) TextGenerate(ctx context.Context, req dto.TextReq) (dto.TextResp, error) {
	params := openai.ChatCompletionNewParams{
		Model: shared.ChatModel(p.cfg.ChatModel),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You are a helpful AI assistant."),
			openai.UserMessage(req.Prompt),
		},
	}
	if req.MaxTokens > 0 {
		params.MaxTokens = openai.Int(int64(req.MaxTokens))
	}
	if req.Temperature > 0 {
		params.Temperature = openai.Float(float64(req.Temperature))
	}
	resp, err := p.client.Chat.Completions.New(ctx, params, p.requestOptions(ctx)...)
	if err != nil {
		return dto.TextResp{}, err
	}
	if len(resp.Choices) == 0 || strings.TrimSpace(resp.Choices[0].Message.Content) == "" {
		return dto.TextResp{}, errors.New("openai: empty chat response")
	}
	return dto.TextResp{Content: resp.Choices[0].Message.Content}, nil
}

func (p *Provider) ImageGenerate(ctx context.Context, req dto.ImageReq) (dto.ImageResp, error) {
	params := openai.ImageGenerateParams{
		Model:  openai.ImageModel(p.cfg.ImageModel),
		Prompt: req.Prompt,
	}
	if req.Size != "" {
		params.Size = openai.ImageGenerateParamsSize(req.Size)
	}
	resp, err := p.client.Images.Generate(ctx, params, p.requestOptions(ctx)...)
	if err != nil {
		return dto.ImageResp{}, err
	}
	if len(resp.Data) == 0 {
		return dto.ImageResp{}, errors.New("openai: empty image response")
	}
	entry := resp.Data[0]
	url := entry.URL
	if url == "" && entry.B64JSON != "" {
		url = "data:image/png;base64," + entry.B64JSON
	}
	if url == "" {
		return dto.ImageResp{}, errors.New("openai: missing image payload")
	}
	return dto.ImageResp{URL: url}, nil
}

func (p *Provider) ImageEdit(ctx context.Context, req dto.ImageEditReq) (dto.ImageResp, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return dto.ImageResp{}, errors.New("prompt is required")
	}
	imageBytes, imageMIME, imageName, err := p.loadBinary(ctx, req.ImageURL, req.ImageBase64, "image")
	if err != nil {
		return dto.ImageResp{}, fmt.Errorf("load image: %w", err)
	}
	if len(imageBytes) == 0 {
		return dto.ImageResp{}, errors.New("image payload required")
	}
	maskBytes, maskMIME, maskName, err := p.loadBinary(ctx, req.MaskURL, req.MaskBase64, "mask")
	if err != nil {
		return dto.ImageResp{}, fmt.Errorf("load mask: %w", err)
	}
	params := openai.ImageEditParams{
		Model:  openai.ImageModel(p.cfg.ImageModel),
		Prompt: req.Prompt,
		Image:  openai.ImageEditParamsImageUnion{OfFile: newUploadReader(imageBytes, imageName, imageMIME)},
	}
	if req.Size != "" {
		params.Size = openai.ImageEditParamsSize(req.Size)
	}
	if len(maskBytes) > 0 {
		params.Mask = newUploadReader(maskBytes, maskName, maskMIME)
	}
	resp, err := p.client.Images.Edit(ctx, params, p.requestOptions(ctx)...)
	if err != nil {
		return dto.ImageResp{}, err
	}
	if len(resp.Data) == 0 {
		return dto.ImageResp{}, errors.New("openai: empty image edit response")
	}
	entry := resp.Data[0]
	url := entry.URL
	if url == "" && entry.B64JSON != "" {
		url = "data:image/png;base64," + entry.B64JSON
	}
	if url == "" {
		return dto.ImageResp{}, errors.New("openai: missing image payload")
	}
	return dto.ImageResp{URL: url}, nil
}

func (p *Provider) VideoGenerate(ctx context.Context, req dto.VideoReq) (dto.VideoResp, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return dto.VideoResp{}, errors.New("prompt is required")
	}
	seconds, err := normalizeVideoSeconds(req.DurationSeconds)
	if err != nil {
		return dto.VideoResp{}, err
	}
	payload := videoRequest{
		Model:   p.cfg.VideoModel,
		Prompt:  req.Prompt,
		Seconds: seconds,
		Size:    p.cfg.VideoSize,
	}
	ref, err := p.prepareVideoReference(ctx, req.Media)
	if err != nil {
		return dto.VideoResp{}, err
	}
	job, err := p.createVideoJob(ctx, payload, ref)
	if err != nil {
		return dto.VideoResp{}, err
	}
	if strings.TrimSpace(job.ID) == "" {
		return dto.VideoResp{}, errors.New("openai: missing video job id")
	}
	if job.Status == "failed" {
		return dto.VideoResp{}, job.videoError()
	}
	if job.Status != "completed" {
		if job, err = p.pollVideoJob(ctx, job.ID); err != nil {
			return dto.VideoResp{}, err
		}
	}
	videoBytes, mimeType, err := p.downloadVideoContent(ctx, job.ID)
	if err != nil {
		return dto.VideoResp{}, err
	}
	encoded := base64.StdEncoding.EncodeToString(videoBytes)
	if mimeType == "" {
		mimeType = "video/mp4"
	}
	url := fmt.Sprintf("data:%s;base64,%s", mimeType, encoded)
	return dto.VideoResp{URL: url}, nil
}

func normalizeVideoSeconds(requested int) (string, error) {
	if requested == 0 {
		requested = 4
	}
	switch requested {
	case 4, 8, 12:
		return strconv.Itoa(requested), nil
	default:
		return "", fmt.Errorf("openai: unsupported video duration %d (allowed: 4, 8, 12)", requested)
	}
}

type videoReferenceUpload struct {
	Data     []byte
	MIMEType string
	Filename string
}

func (p *Provider) prepareVideoReference(ctx context.Context, media []dto.MediaInput) (*videoReferenceUpload, error) {
	for _, input := range media {
		data, inlineMIME, inlineName, err := p.loadBinary(ctx, input.URL, input.Data, "reference")
		if err != nil {
			return nil, fmt.Errorf("load video reference: %w", err)
		}
		if len(data) == 0 {
			continue
		}
		mimeType := strings.TrimSpace(input.MIMEType)
		if mimeType == "" {
			mimeType = inlineMIME
		}
		if mimeType == "" {
			mimeType = http.DetectContentType(data)
		}
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		filename := inlineName
		if filename == "" {
			filename = guessFilename(input.URL, mimeType, "reference")
		}
		return &videoReferenceUpload{
			Data:     data,
			MIMEType: mimeType,
			Filename: filename,
		}, nil
	}
	return nil, nil
}

func (p *Provider) createVideoJob(ctx context.Context, payload videoRequest, ref *videoReferenceUpload) (videoJob, error) {
	path := openAIVideosPath
	opts := p.requestOptions(ctx)
	var job videoJob
	if ref == nil {
		if err := p.client.Post(ctx, path, payload, &job, opts...); err != nil {
			return videoJob{}, err
		}
		return job, nil
	}
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	fileWriter, err := createFormFileWithContentType(writer, "input_reference", ref.Filename, ref.MIMEType)
	if err != nil {
		return videoJob{}, err
	}
	if _, err := fileWriter.Write(ref.Data); err != nil {
		return videoJob{}, err
	}
	fields := map[string]string{
		"prompt": payload.Prompt,
	}
	if payload.Model != "" {
		fields["model"] = payload.Model
	}
	if payload.Seconds != "" {
		fields["seconds"] = payload.Seconds
	}
	if payload.Size != "" {
		fields["size"] = payload.Size
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return videoJob{}, err
		}
	}
	if err := writer.Close(); err != nil {
		return videoJob{}, err
	}
	reqOpts := append([]option.RequestOption{
		option.WithRequestBody(writer.FormDataContentType(), body.Bytes()),
	}, opts...)
	if err := p.client.Post(ctx, path, nil, &job, reqOpts...); err != nil {
		return videoJob{}, err
	}
	return job, nil
}

func (p *Provider) pollVideoJob(ctx context.Context, videoID string) (videoJob, error) {
	interval := time.Second
	for {
		job, pollAfter, err := p.fetchVideoJob(ctx, videoID)
		if err != nil {
			return videoJob{}, err
		}
		switch job.Status {
		case "completed":
			return job, nil
		case "failed":
			return videoJob{}, job.videoError()
		}
		if pollAfter > 0 {
			interval = pollAfter
		}
		select {
		case <-ctx.Done():
			return videoJob{}, ctx.Err()
		case <-time.After(interval):
		}
	}
}

func (p *Provider) fetchVideoJob(ctx context.Context, videoID string) (videoJob, time.Duration, error) {
	path := p.videoJobPath(videoID)
	var job videoJob
	var raw *http.Response
	opts := append([]option.RequestOption{option.WithResponseInto(&raw)}, p.requestOptions(ctx)...)
	if err := p.client.Get(ctx, path, nil, &job, opts...); err != nil {
		return videoJob{}, 0, err
	}
	if job.Status == "failed" {
		return job, 0, job.videoError()
	}
	var pollAfter time.Duration
	if raw != nil {
		if header := raw.Header.Get("openai-poll-after-ms"); header != "" {
			if ms, err := strconv.Atoi(header); err == nil && ms > 0 {
				pollAfter = time.Duration(ms) * time.Millisecond
			}
		}
	}
	return job, pollAfter, nil
}

func (p *Provider) downloadVideoContent(ctx context.Context, videoID string) ([]byte, string, error) {
	path := fmt.Sprintf("%s?variant=video", p.videoContentPath(videoID))
	var raw *http.Response
	var body []byte
	opts := append([]option.RequestOption{
		option.WithResponseInto(&raw),
		option.WithResponseBodyInto(&body),
	}, p.requestOptions(ctx)...)
	if err := p.client.Get(ctx, path, nil, nil, opts...); err != nil {
		return nil, "", err
	}
	mimeType := ""
	if raw != nil {
		mimeType = raw.Header.Get("Content-Type")
	}
	return body, mimeType, nil
}

func (job videoJob) videoError() error {
	if job.Error != nil {
		if msg := strings.TrimSpace(job.Error.Message); msg != "" {
			return fmt.Errorf("openai video job failed: %s", msg)
		}
		if job.Error.Code != "" {
			return fmt.Errorf("openai video job failed: %s", job.Error.Code)
		}
	}
	if job.Status != "" {
		return fmt.Errorf("openai video job failed: %s", job.Status)
	}
	return errors.New("openai video job failed")
}

func filenameForMIME(mimeType, prefix string) string {
	if prefix == "" {
		prefix = "upload"
	}
	if ext := extensionForMIME(mimeType); ext != "" {
		return prefix + "." + ext
	}
	return prefix + ".bin"
}

type uploadReader struct {
	*bytes.Reader
	filename    string
	contentType string
}

func (r *uploadReader) Filename() string {
	return r.filename
}

func (r *uploadReader) ContentType() string {
	return r.contentType
}

func newUploadReader(data []byte, filename, mimeType string) io.Reader {
	if len(data) == 0 {
		return bytes.NewReader(nil)
	}
	if strings.TrimSpace(mimeType) == "" {
		mimeType = http.DetectContentType(data)
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	filename = strings.TrimSpace(filename)
	if filename == "" {
		filename = filenameForMIME(mimeType, "upload")
	}
	return &uploadReader{
		Reader:      bytes.NewReader(data),
		filename:    filename,
		contentType: mimeType,
	}
}

func guessFilename(sourceURL, mimeType, prefix string) string {
	if name := filenameFromURL(sourceURL); name != "" {
		return name
	}
	return filenameForMIME(mimeType, prefix)
}

func filenameFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	base := path.Base(parsed.Path)
	if base == "." || base == "/" {
		return ""
	}
	return base
}

func filenameFromContentDisposition(header string) string {
	if header == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(header)
	if err != nil {
		return ""
	}
	if name := params["filename"]; name != "" {
		return name
	}
	if name := params["filename*"]; name != "" {
		return name
	}
	return ""
}

func extensionForMIME(mimeType string) string {
	switch strings.ToLower(mimeType) {
	case "image/png":
		return "png"
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	case "video/mp4":
		return "mp4"
	default:
		return ""
	}
}

func (p *Provider) SpeechToText(ctx context.Context, req dto.STTReq) (dto.STTResp, error) {
	if req.AudioURL == "" {
		return dto.STTResp{}, errors.New("audio_url is required")
	}
	audioBytes, err := p.fetchAudio(ctx, req.AudioURL)
	if err != nil {
		return dto.STTResp{}, err
	}
	params := openai.AudioTranscriptionNewParams{
		Model: openai.AudioModel(p.cfg.TranscriptionModel),
		File:  bytes.NewReader(audioBytes),
	}
	if req.Language != "" {
		params.Language = openai.String(req.Language)
	}
	resp, err := p.client.Audio.Transcriptions.New(ctx, params, p.requestOptions(ctx)...)
	if err != nil {
		return dto.STTResp{}, err
	}
	return dto.STTResp{Transcript: resp.Text}, nil
}

func (p *Provider) TextToSpeech(ctx context.Context, req dto.TTSReq) (dto.TTSResp, error) {
	return dto.TTSResp{}, errors.New("openai: text-to-speech not supported")
}

func (p *Provider) Embeddings(ctx context.Context, req dto.EmbeddingsReq) (dto.EmbeddingsResp, error) {
	model := p.cfg.EmbeddingsModel
	if req.Model != "" {
		model = req.Model
	}
	params := openai.EmbeddingNewParams{
		Model: openai.EmbeddingModel(model),
		Input: openai.EmbeddingNewParamsInputUnion{OfArrayOfStrings: req.Inputs},
	}
	resp, err := p.client.Embeddings.New(ctx, params, p.requestOptions(ctx)...)
	if err != nil {
		return dto.EmbeddingsResp{}, err
	}
	vectors := make([][]float32, len(req.Inputs))
	for _, item := range resp.Data {
		idx := int(item.Index)
		if idx >= 0 && idx < len(vectors) {
			vec := make([]float32, len(item.Embedding))
			for i, val := range item.Embedding {
				vec[i] = float32(val)
			}
			vectors[idx] = vec
		}
	}
	return dto.EmbeddingsResp{Vectors: vectors}, nil
}

func (p *Provider) Moderation(ctx context.Context, req dto.ModerationReq) (dto.ModerationResp, error) {
	if strings.TrimSpace(req.Input) == "" {
		return dto.ModerationResp{}, errors.New("openai: missing input")
	}
	params := openai.ModerationNewParams{
		Model: openai.ModerationModel(p.cfg.ModerationModel),
		Input: openai.ModerationNewParamsInputUnion{OfString: openai.String(req.Input)},
	}
	resp, err := p.client.Moderations.New(ctx, params, p.requestOptions(ctx)...)
	if err != nil {
		return dto.ModerationResp{}, err
	}
	if len(resp.Results) == 0 {
		return dto.ModerationResp{}, errors.New("openai: empty moderation response")
	}
	result := resp.Results[0]
	return dto.ModerationResp{
		Flagged: result.Flagged,
		Reason:  flaggedCategories(moderationCategoriesMap(result.Categories)),
	}, nil
}

func (p *Provider) requestOptions(ctx context.Context) []option.RequestOption {
	key := strings.TrimSpace(providers.APIKeyFromContext(ctx))
	if key == "" {
		return nil
	}
	return []option.RequestOption{option.WithAPIKey(key)}
}

func (p *Provider) videoJobPath(videoID string) string {
	segment := strings.Trim(videoID, "/")
	if segment == "" {
		return openAIVideosPath
	}
	return fmt.Sprintf("%s/%s", openAIVideosPath, segment)
}

func (p *Provider) videoContentPath(videoID string) string {
	segment := strings.Trim(videoID, "/")
	if segment == "" {
		return fmt.Sprintf("%s/content", openAIVideosPath)
	}
	return fmt.Sprintf("%s/%s/content", openAIVideosPath, segment)
}

func (p *Provider) fetchAudio(ctx context.Context, audioURL string) ([]byte, error) {
	if strings.HasPrefix(audioURL, "data:") {
		data, _, err := decodeDataURL(audioURL)
		return data, err
	}
	data, _, _, err := p.fetchAsset(ctx, audioURL, "audio")
	return data, err
}

func (p *Provider) fetchAsset(ctx context.Context, assetURL string, label string) ([]byte, string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return nil, "", "", err
	}
	resp, err := p.downloadClient.Do(req)
	if err != nil {
		return nil, "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		payload, _ := io.ReadAll(resp.Body)
		return nil, "", "", fmt.Errorf("download %s failed: %s", label, strings.TrimSpace(string(payload)))
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", "", err
	}
	filename := filenameFromContentDisposition(resp.Header.Get("Content-Disposition"))
	if filename == "" {
		filename = filenameFromURL(assetURL)
	}
	return data, resp.Header.Get("Content-Type"), filename, nil
}

func decodeDataURL(raw string) ([]byte, string, error) {
	if !strings.HasPrefix(raw, "data:") {
		return nil, "", errors.New("invalid data url")
	}
	parts := strings.SplitN(raw, ",", 2)
	if len(parts) != 2 {
		return nil, "", errors.New("invalid data url")
	}
	meta := strings.TrimPrefix(parts[0], "data:")
	mimeType := ""
	for _, section := range strings.Split(meta, ";") {
		if section == "base64" || section == "" {
			continue
		}
		if mimeType == "" {
			mimeType = section
		}
	}
	data, err := base64.StdEncoding.DecodeString(parts[1])
	return data, mimeType, err
}

func createFormFileWithContentType(writer *multipart.Writer, fieldName, filename, contentType string) (io.Writer, error) {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	headers := make(textproto.MIMEHeader)
	headers.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, fieldName, filename))
	headers.Set("Content-Type", contentType)
	return writer.CreatePart(headers)
}

func moderationCategoriesMap(cats openai.ModerationCategories) map[string]bool {
	return map[string]bool{
		"harassment":             cats.Harassment,
		"harassment/threatening": cats.HarassmentThreatening,
		"hate":                   cats.Hate,
		"hate/threatening":       cats.HateThreatening,
		"illicit":                cats.Illicit,
		"illicit/violent":        cats.IllicitViolent,
		"self-harm":              cats.SelfHarm,
		"self-harm/instructions": cats.SelfHarmInstructions,
		"self-harm/intent":       cats.SelfHarmIntent,
		"sexual":                 cats.Sexual,
		"sexual/minors":          cats.SexualMinors,
		"violence":               cats.Violence,
		"violence/graphic":       cats.ViolenceGraphic,
	}
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

func (p *Provider) loadBinary(ctx context.Context, url, base64Data, label string) ([]byte, string, string, error) {
	url = strings.TrimSpace(url)
	base64Data = strings.TrimSpace(base64Data)
	if url == "" && base64Data == "" {
		return nil, "", "", nil
	}
	fallback := strings.TrimSpace(label)
	if fallback == "" {
		fallback = "asset"
	}
	if base64Data != "" {
		if strings.HasPrefix(base64Data, "data:") {
			data, mimeType, err := decodeDataURL(base64Data)
			if err != nil {
				return nil, "", "", err
			}
			return data, mimeType, filenameForMIME(mimeType, fallback), nil
		}
		data, err := base64.StdEncoding.DecodeString(base64Data)
		if err != nil {
			return nil, "", "", err
		}
		mimeType := http.DetectContentType(data)
		return data, mimeType, filenameForMIME(mimeType, fallback), nil
	}
	data, mimeType, filename, err := p.fetchAsset(ctx, url, label)
	if err != nil {
		return nil, "", "", err
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	if filename == "" {
		filename = guessFilename(url, mimeType, fallback)
	}
	return data, mimeType, filename, nil
}

type videoRequest struct {
	Model   string `json:"model"`
	Prompt  string `json:"prompt"`
	Seconds string `json:"seconds,omitempty"`
	Size    string `json:"size,omitempty"`
}

type videoJob struct {
	ID     string         `json:"id"`
	Status string         `json:"status"`
	Error  *videoJobError `json:"error"`
}

type videoJobError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
