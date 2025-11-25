package server

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"

	apiv1 "github.com/midia/aione/api/grpc/aione/v1"
	"github.com/midia/aione/internal/providers"
	"github.com/midia/aione/internal/providers/dto"
	providermanager "github.com/midia/aione/internal/services/provider"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *publicService) Chat(ctx context.Context, req *apiv1.ChatRequest) (*apiv1.ChatResponse, error) {
	manager, err := s.requireProviders()
	if err != nil {
		return nil, err
	}
	if req == nil || strings.TrimSpace(req.GetPrompt()) == "" {
		return nil, status.Error(codes.InvalidArgument, "prompt required")
	}
	dtoReq := dto.TextReq{
		Prompt:      req.GetPrompt(),
		MaxTokens:   int(req.GetMaxTokens()),
		Temperature: req.GetTemperature(),
		Media:       mediaToDTO(req.GetMedia()),
	}
	dtoReq.ProviderOverride.Provider = overrideProvider(req.GetRouting())
	ctx = s.applyRouting(ctx, req.GetRouting())
	res, err := manager.TextGenerate(ctx, dtoReq)
	if err != nil {
		return nil, providerStatus(err)
	}
	return &apiv1.ChatResponse{Provider: res.Provider, Content: res.Data.Content}, nil
}

func (s *publicService) GenerateImage(ctx context.Context, req *apiv1.ImageRequest) (*apiv1.ImageResponse, error) {
	manager, err := s.requireProviders()
	if err != nil {
		return nil, err
	}
	if req == nil || strings.TrimSpace(req.GetPrompt()) == "" {
		return nil, status.Error(codes.InvalidArgument, "prompt required")
	}
	dtoReq := dto.ImageReq{
		ProviderOverride: dto.ProviderOverride{Provider: overrideProvider(req.GetRouting())},
		Prompt:           req.GetPrompt(),
		Size:             req.GetSize(),
		Media:            mediaToDTO(req.GetMedia()),
	}
	ctx = s.applyRouting(ctx, req.GetRouting())
	res, err := manager.ImageGenerate(ctx, dtoReq)
	if err != nil {
		return nil, providerStatus(err)
	}
	image, err := s.normalizeImage(ctx, res.Data)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "normalize image: %v", err)
	}
	return &apiv1.ImageResponse{Provider: res.Provider, Image: &apiv1.ImageAsset{Url: image.URL}}, nil
}

func (s *publicService) EditImage(ctx context.Context, req *apiv1.ImageEditRequest) (*apiv1.ImageResponse, error) {
	manager, err := s.requireProviders()
	if err != nil {
		return nil, err
	}
	if req == nil || strings.TrimSpace(req.GetPrompt()) == "" {
		return nil, status.Error(codes.InvalidArgument, "prompt required")
	}
	if strings.TrimSpace(req.GetImageUrl()) == "" && len(req.GetImageData()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "image source required")
	}
	dtoReq := dto.ImageEditReq{
		ProviderOverride: dto.ProviderOverride{Provider: overrideProvider(req.GetRouting())},
		Prompt:           req.GetPrompt(),
		ImageURL:         req.GetImageUrl(),
		ImageBase64:      encodeBytes(req.GetImageData()),
		MaskURL:          req.GetMaskUrl(),
		MaskBase64:       encodeBytes(req.GetMaskData()),
		Size:             req.GetSize(),
		Media:            mediaToDTO(req.GetMedia()),
	}
	ctx = s.applyRouting(ctx, req.GetRouting())
	res, err := manager.ImageEdit(ctx, dtoReq)
	if err != nil {
		return nil, providerStatus(err)
	}
	image, err := s.normalizeImage(ctx, res.Data)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "normalize image: %v", err)
	}
	return &apiv1.ImageResponse{Provider: res.Provider, Image: &apiv1.ImageAsset{Url: image.URL}}, nil
}

func (s *publicService) GenerateVideo(ctx context.Context, req *apiv1.VideoRequest) (*apiv1.VideoResponse, error) {
	manager, err := s.requireProviders()
	if err != nil {
		return nil, err
	}
	if req == nil || strings.TrimSpace(req.GetPrompt()) == "" {
		return nil, status.Error(codes.InvalidArgument, "prompt required")
	}
	dtoReq := dto.VideoReq{
		ProviderOverride: dto.ProviderOverride{Provider: overrideProvider(req.GetRouting())},
		Prompt:           req.GetPrompt(),
		DurationSeconds:  int(req.GetDurationSeconds()),
		Media:            mediaToDTO(req.GetMedia()),
	}
	ctx = s.applyRouting(ctx, req.GetRouting())
	res, err := manager.VideoGenerate(ctx, dtoReq)
	if err != nil {
		return nil, providerStatus(err)
	}
	video, err := s.normalizeVideo(ctx, res.Data)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "normalize video: %v", err)
	}
	return &apiv1.VideoResponse{Provider: res.Provider, Video: &apiv1.VideoAsset{Url: video.URL}}, nil
}

func (s *publicService) SpeechToText(ctx context.Context, req *apiv1.SpeechToTextRequest) (*apiv1.SpeechToTextResponse, error) {
	manager, err := s.requireProviders()
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request required")
	}
	if strings.TrimSpace(req.GetAudioUrl()) == "" {
		if len(req.GetAudioData()) > 0 {
			return nil, status.Error(codes.InvalidArgument, "audio_url required for now")
		}
		return nil, status.Error(codes.InvalidArgument, "audio_url required")
	}
	dtoReq := dto.STTReq{
		ProviderOverride: dto.ProviderOverride{Provider: overrideProvider(req.GetRouting())},
		AudioURL:         req.GetAudioUrl(),
		Language:         req.GetLanguage(),
	}
	ctx = s.applyRouting(ctx, req.GetRouting())
	res, err := manager.SpeechToText(ctx, dtoReq)
	if err != nil {
		return nil, providerStatus(err)
	}
	return &apiv1.SpeechToTextResponse{Provider: res.Provider, Transcript: res.Data.Transcript}, nil
}

func (s *publicService) TextToSpeech(ctx context.Context, req *apiv1.TextToSpeechRequest) (*apiv1.TextToSpeechResponse, error) {
	manager, err := s.requireProviders()
	if err != nil {
		return nil, err
	}
	if req == nil || strings.TrimSpace(req.GetText()) == "" {
		return nil, status.Error(codes.InvalidArgument, "text required")
	}
	dtoReq := dto.TTSReq{
		ProviderOverride: dto.ProviderOverride{Provider: overrideProvider(req.GetRouting())},
		Text:             req.GetText(),
		Voice:            req.GetVoice(),
		Language:         req.GetLanguage(),
	}
	ctx = s.applyRouting(ctx, req.GetRouting())
	res, err := manager.TextToSpeech(ctx, dtoReq)
	if err != nil {
		return nil, providerStatus(err)
	}
	return &apiv1.TextToSpeechResponse{Provider: res.Provider, AudioUrl: res.Data.AudioURL}, nil
}

func (s *publicService) Embeddings(ctx context.Context, req *apiv1.EmbeddingsRequest) (*apiv1.EmbeddingsResponse, error) {
	manager, err := s.requireProviders()
	if err != nil {
		return nil, err
	}
	if req == nil || len(req.GetInputs()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "inputs required")
	}
	dtoReq := dto.EmbeddingsReq{
		ProviderOverride: dto.ProviderOverride{Provider: overrideProvider(req.GetRouting())},
		Inputs:           req.GetInputs(),
		Model:            req.GetModel(),
	}
	ctx = s.applyRouting(ctx, req.GetRouting())
	res, err := manager.Embeddings(ctx, dtoReq)
	if err != nil {
		return nil, providerStatus(err)
	}
	vectors := make([]*apiv1.EmbeddingVector, 0, len(res.Data.Vectors))
	for _, vector := range res.Data.Vectors {
		cloned := make([]float32, len(vector))
		copy(cloned, vector)
		vectors = append(vectors, &apiv1.EmbeddingVector{Values: cloned})
	}
	return &apiv1.EmbeddingsResponse{Provider: res.Provider, Vectors: vectors}, nil
}

func (s *publicService) Moderation(ctx context.Context, req *apiv1.ModerationRequest) (*apiv1.ModerationResponse, error) {
	manager, err := s.requireProviders()
	if err != nil {
		return nil, err
	}
	if req == nil || strings.TrimSpace(req.GetInput()) == "" {
		return nil, status.Error(codes.InvalidArgument, "input required")
	}
	dtoReq := dto.ModerationReq{
		ProviderOverride: dto.ProviderOverride{Provider: overrideProvider(req.GetRouting())},
		Input:            req.GetInput(),
	}
	ctx = s.applyRouting(ctx, req.GetRouting())
	res, err := manager.Moderation(ctx, dtoReq)
	if err != nil {
		return nil, providerStatus(err)
	}
	return &apiv1.ModerationResponse{Provider: res.Provider, Flagged: res.Data.Flagged, Reason: res.Data.Reason}, nil
}

func (s *publicService) ListProviders(ctx context.Context, _ *apiv1.ListProvidersRequest) (*apiv1.ListProvidersResponse, error) {
	manager, err := s.requireProviders()
	if err != nil {
		return nil, err
	}
	matrix := manager.CapabilityMatrix()
	entries := make([]*apiv1.CapabilityMatrixEntry, 0, len(matrix))
	for _, entry := range matrix {
		entries = append(entries, &apiv1.CapabilityMatrixEntry{
			Name:         entry.Name,
			Capabilities: convertCapabilities(entry.Capabilities),
		})
	}
	return &apiv1.ListProvidersResponse{Entries: entries}, nil
}

func (s *publicService) ListModels(ctx context.Context, req *apiv1.ListModelsRequest) (*apiv1.ListModelsResponse, error) {
	manager, err := s.requireProviders()
	if err != nil {
		return nil, err
	}
	if provider := strings.TrimSpace(req.GetProvider()); provider != "" {
		models, err := manager.ModelCatalog(ctx, provider)
		if err != nil {
			switch {
			case errors.Is(err, providermanager.ErrUnknownProvider):
				return nil, status.Error(codes.InvalidArgument, err.Error())
			case errors.Is(err, providermanager.ErrModelCatalogUnavailable):
				return nil, status.Error(codes.NotFound, err.Error())
			default:
				return nil, status.Errorf(codes.Internal, "model catalog: %v", err)
			}
		}
		return &apiv1.ListModelsResponse{Catalogs: map[string]*apiv1.ModelCatalog{
			provider: {Models: convertModels(models)},
		}}, nil
	}
	catalogs := manager.AllModelCatalogs(ctx)
	resp := make(map[string]*apiv1.ModelCatalog, len(catalogs))
	for name, models := range catalogs {
		resp[name] = &apiv1.ModelCatalog{Models: convertModels(models)}
	}
	return &apiv1.ListModelsResponse{Catalogs: resp}, nil
}

func convertCapabilities(caps providers.Capabilities) *apiv1.ProviderCapabilities {
	return &apiv1.ProviderCapabilities{
		TextGeneration:  caps.TextGeneration,
		ImageGeneration: caps.ImageGeneration,
		ImageEditing:    caps.ImageEditing,
		VideoGeneration: caps.VideoGeneration,
		SpeechToText:    caps.SpeechToText,
		TextToSpeech:    caps.TextToSpeech,
		Embeddings:      caps.Embeddings,
		Moderation:      caps.Moderation,
		Limits: &apiv1.CapabilityLimits{
			MaxTextTokens:          int32(caps.Limits.MaxTextTokens),
			MaxImageResolution:     caps.Limits.MaxImageResolution,
			MaxEmbeddingDimensions: int32(caps.Limits.MaxEmbeddingDimensions),
		},
		Attributes: &apiv1.CapabilityAttributes{
			CostScore:    int32(caps.Attributes.CostScore),
			LatencyScore: int32(caps.Attributes.LatencyScore),
			QualityScore: int32(caps.Attributes.QualityScore),
			RateLimitRps: caps.Attributes.RateLimitRPS,
		},
	}
}

func convertModels(models []providers.ModelDescriptor) []*apiv1.ModelDescriptor {
	result := make([]*apiv1.ModelDescriptor, 0, len(models))
	for _, model := range models {
		result = append(result, &apiv1.ModelDescriptor{
			Provider:    model.Provider,
			Name:        model.Name,
			Capability:  string(model.Capability),
			Description: model.Description,
			Default:     model.Default,
			Tags:        append([]string(nil), model.Tags...),
			Metadata:    model.Metadata,
		})
	}
	return result
}

func encodeBytes(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(data)
}
