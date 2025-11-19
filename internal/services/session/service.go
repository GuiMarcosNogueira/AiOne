package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"log/slog"

	"github.com/midia/aione/internal/providers"
	"github.com/midia/aione/internal/providers/dto"
	"github.com/midia/aione/internal/services/history"
	providermanager "github.com/midia/aione/internal/services/provider"
	"github.com/midia/aione/internal/services/providersessions"
)

var (
	ErrUserIDRequired      = errors.New("session: user id required")
	ErrProviderRequired    = errors.New("session: provider required")
	ErrPromptRequired      = errors.New("session: prompt required")
	ErrAudioURLRequired    = errors.New("session: audio_url required")
	ErrProviderKeyRequired = errors.New("session: provider key required")
)

// ProviderClient abstracts the provider manager dependency for easier testing.
type ProviderClient interface {
	CapabilitiesFor(name string) (providers.Capabilities, bool)
	TextGenerate(ctx context.Context, req dto.TextReq) (providermanager.Result[dto.TextResp], error)
	ImageGenerate(ctx context.Context, req dto.ImageReq) (providermanager.Result[dto.ImageResp], error)
	VideoGenerate(ctx context.Context, req dto.VideoReq) (providermanager.Result[dto.VideoResp], error)
	SpeechToText(ctx context.Context, req dto.STTReq) (providermanager.Result[dto.STTResp], error)
}

// SessionStore exposes provider session operations used by the orchestrator.
type SessionStore interface {
	GetSession(ctx context.Context, userID, provider string) (providersessions.SessionDetails, error)
	SetProviderKey(ctx context.Context, input providersessions.SetKeyInput) (providersessions.SessionDetails, error)
	RecordUsage(ctx context.Context, input providersessions.UsageInput) (providersessions.SessionDetails, error)
}

// HistoryStore supplies the subset of history features required for session flows.
type HistoryStore interface {
	TruncateHistoryToTokenLimit(ctx context.Context, userID, provider string, limit int) (int, error)
	ListHistory(ctx context.Context, userID, provider string) ([]history.Entry, error)
	SaveMessage(ctx context.Context, input history.SaveMessageInput) (history.Entry, error)
}

// Service coordinates session-aware interactions with providers.
type Service struct {
	log       *slog.Logger
	providers ProviderClient
	sessions  SessionStore
	history   HistoryStore
}

// NewService wires a new session interaction service.
func NewService(log *slog.Logger, providers ProviderClient, sessions SessionStore, historyStore HistoryStore) (*Service, error) {
	if providers == nil {
		return nil, errors.New("session: provider client required")
	}
	if sessions == nil {
		return nil, errors.New("session: session store required")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{log: log, providers: providers, sessions: sessions, history: historyStore}, nil
}

// SessionInput carries the shared metadata required for session-scoped calls.
type SessionInput struct {
	UserID          string
	Provider        string
	ProviderKey     string
	SessionMetadata map[string]any
	ExpiresAt       *time.Time
}

// MessageInput captures a chat-style interaction.
type MessageInput struct {
	SessionInput
	Prompt      string
	MaxTokens   int
	Temperature float32
	Media       []dto.MediaInput
}

// ImageInput describes an image-generation interaction.
type ImageInput struct {
	SessionInput
	Prompt string
	Size   string
	Media  []dto.MediaInput
}

// VideoInput represents a video-generation interaction.
type VideoInput struct {
	SessionInput
	Prompt          string
	DurationSeconds int
	Media           []dto.MediaInput
}

// AudioInput wraps a speech-to-text request.
type AudioInput struct {
	SessionInput
	AudioURL string
	Language string
}

// Result exposes the provider payload plus refreshed session metadata.
type Result[T any] struct {
	Provider string
	Payload  T
	Session  providersessions.SessionDetails
}

// SendMessage sends a text message to the requested provider while maintaining history and usage.
func (s *Service) SendMessage(ctx context.Context, input MessageInput) (Result[dto.TextResp], error) {
	if err := validateSessionInput(input.SessionInput); err != nil {
		return Result[dto.TextResp]{}, err
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return Result[dto.TextResp]{}, ErrPromptRequired
	}
	session, providerName, normalizedProvider, err := s.ensureSession(ctx, input.SessionInput)
	if err != nil {
		return Result[dto.TextResp]{}, err
	}
	limit := s.historyBudget(providerName, input.MaxTokens)
	s.truncateHistory(ctx, input.UserID, normalizedProvider, limit)
	prompt := s.mergeHistoryContext(ctx, input.UserID, normalizedProvider, input.Prompt)

	req := dto.TextReq{
		Prompt:      prompt,
		MaxTokens:   input.MaxTokens,
		Temperature: input.Temperature,
		Media:       input.Media,
	}
	reqCtx := s.decorateContext(ctx, providerName, session.ProviderKey)
	res, err := s.providers.TextGenerate(reqCtx, req)
	if err != nil {
		return Result[dto.TextResp]{}, err
	}

	s.persistHistoryPair(ctx, normalizedProvider, input.UserID, input.Prompt, res.Data.Content)
	tokens := estimateTokens(input.Prompt, res.Data.Content)
	updated, err := s.recordUsage(ctx, input.SessionInput, normalizedProvider, tokens)
	if err != nil {
		return Result[dto.TextResp]{}, err
	}

	return Result[dto.TextResp]{
		Provider: res.Provider,
		Payload:  res.Data,
		Session:  updated,
	}, nil
}

// GenerateImage proxies an authenticated image generation call with history tracking.
func (s *Service) GenerateImage(ctx context.Context, input ImageInput) (Result[dto.ImageResp], error) {
	if err := validateSessionInput(input.SessionInput); err != nil {
		return Result[dto.ImageResp]{}, err
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return Result[dto.ImageResp]{}, ErrPromptRequired
	}
	session, providerName, normalizedProvider, err := s.ensureSession(ctx, input.SessionInput)
	if err != nil {
		return Result[dto.ImageResp]{}, err
	}
	limit := s.historyBudget(providerName, 0)
	s.truncateHistory(ctx, input.UserID, normalizedProvider, limit)

	req := dto.ImageReq{Prompt: input.Prompt, Size: input.Size, Media: input.Media}
	reqCtx := s.decorateContext(ctx, providerName, session.ProviderKey)
	res, err := s.providers.ImageGenerate(reqCtx, req)
	if err != nil {
		return Result[dto.ImageResp]{}, err
	}

	s.persistHistoryPair(ctx, normalizedProvider, input.UserID, input.Prompt, res.Data.URL)
	tokens := estimateTokens(input.Prompt, res.Data.URL)
	updated, err := s.recordUsage(ctx, input.SessionInput, normalizedProvider, tokens)
	if err != nil {
		return Result[dto.ImageResp]{}, err
	}

	return Result[dto.ImageResp]{Provider: res.Provider, Payload: res.Data, Session: updated}, nil
}

// GenerateVideo proxies a video generation request.
func (s *Service) GenerateVideo(ctx context.Context, input VideoInput) (Result[dto.VideoResp], error) {
	if err := validateSessionInput(input.SessionInput); err != nil {
		return Result[dto.VideoResp]{}, err
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return Result[dto.VideoResp]{}, ErrPromptRequired
	}
	session, providerName, normalizedProvider, err := s.ensureSession(ctx, input.SessionInput)
	if err != nil {
		return Result[dto.VideoResp]{}, err
	}
	limit := s.historyBudget(providerName, 0)
	s.truncateHistory(ctx, input.UserID, normalizedProvider, limit)

	req := dto.VideoReq{Prompt: input.Prompt, DurationSeconds: input.DurationSeconds, Media: input.Media}
	reqCtx := s.decorateContext(ctx, providerName, session.ProviderKey)
	res, err := s.providers.VideoGenerate(reqCtx, req)
	if err != nil {
		return Result[dto.VideoResp]{}, err
	}

	s.persistHistoryPair(ctx, normalizedProvider, input.UserID, input.Prompt, res.Data.URL)
	tokens := estimateTokens(input.Prompt, res.Data.URL)
	updated, err := s.recordUsage(ctx, input.SessionInput, normalizedProvider, tokens)
	if err != nil {
		return Result[dto.VideoResp]{}, err
	}

	return Result[dto.VideoResp]{Provider: res.Provider, Payload: res.Data, Session: updated}, nil
}

// TranscribeAudio forwards an audio transcription with history + usage tracking.
func (s *Service) TranscribeAudio(ctx context.Context, input AudioInput) (Result[dto.STTResp], error) {
	if err := validateSessionInput(input.SessionInput); err != nil {
		return Result[dto.STTResp]{}, err
	}
	if strings.TrimSpace(input.AudioURL) == "" {
		return Result[dto.STTResp]{}, ErrAudioURLRequired
	}
	session, providerName, normalizedProvider, err := s.ensureSession(ctx, input.SessionInput)
	if err != nil {
		return Result[dto.STTResp]{}, err
	}
	limit := s.historyBudget(providerName, 0)
	s.truncateHistory(ctx, input.UserID, normalizedProvider, limit)

	req := dto.STTReq{AudioURL: input.AudioURL, Language: input.Language}
	reqCtx := s.decorateContext(ctx, providerName, session.ProviderKey)
	res, err := s.providers.SpeechToText(reqCtx, req)
	if err != nil {
		return Result[dto.STTResp]{}, err
	}

	s.persistHistoryPair(ctx, normalizedProvider, input.UserID, fmt.Sprintf("audio:%s", input.AudioURL), res.Data.Transcript)
	tokens := estimateTokens(input.AudioURL, res.Data.Transcript)
	updated, err := s.recordUsage(ctx, input.SessionInput, normalizedProvider, tokens)
	if err != nil {
		return Result[dto.STTResp]{}, err
	}

	return Result[dto.STTResp]{Provider: res.Provider, Payload: res.Data, Session: updated}, nil
}

func (s *Service) ensureSession(ctx context.Context, input SessionInput) (providersessions.SessionDetails, string, string, error) {
	provider := strings.TrimSpace(input.Provider)
	if provider == "" {
		return providersessions.SessionDetails{}, "", "", ErrProviderRequired
	}
	normalized := strings.ToLower(provider)
	session, err := s.sessions.GetSession(ctx, input.UserID, normalized)
	if err == nil {
		return session, provider, normalized, nil
	}
	if !errors.Is(err, providersessions.ErrSessionNotFound) {
		return providersessions.SessionDetails{}, "", "", err
	}
	if strings.TrimSpace(input.ProviderKey) == "" {
		return providersessions.SessionDetails{}, "", "", ErrProviderKeyRequired
	}
	params := providersessions.SetKeyInput{
		UserID:       input.UserID,
		ProviderName: normalized,
		ProviderKey:  input.ProviderKey,
		Metadata:     cloneMetadata(input.SessionMetadata),
		ExpiresAt:    input.ExpiresAt,
	}
	session, err = s.sessions.SetProviderKey(ctx, params)
	if err != nil {
		return providersessions.SessionDetails{}, "", "", err
	}
	return session, provider, normalized, nil
}

func (s *Service) recordUsage(ctx context.Context, input SessionInput, provider string, tokens int) (providersessions.SessionDetails, error) {
	if tokens <= 0 {
		tokens = 1
	}
	usage := providersessions.UsageInput{
		UserID:          input.UserID,
		ProviderName:    provider,
		TokensDelta:     int64(tokens),
		LastInteraction: time.Now().UTC(),
	}
	return s.sessions.RecordUsage(ctx, usage)
}

func (s *Service) truncateHistory(ctx context.Context, userID, provider string, limit int) {
	if s.history == nil || limit <= 0 {
		return
	}
	removed, err := s.history.TruncateHistoryToTokenLimit(ctx, userID, provider, limit)
	if err != nil {
		s.log.Warn("session history truncate failed", slog.Any("error", err), slog.String("provider", provider), slog.String("user", userID))
		return
	}
	if removed == 0 {
		return
	}
	message := fmt.Sprintf("system trimmed %d entries to honor token budget (%d)", removed, limit)
	if _, err := s.history.SaveMessage(ctx, history.SaveMessageInput{UserID: userID, ProviderName: provider, Role: "system", Message: message}); err != nil {
		s.log.Warn("session history truncate log failed", slog.Any("error", err))
	}
}

func (s *Service) mergeHistoryContext(ctx context.Context, userID, provider string, prompt string) string {
	if s.history == nil {
		return prompt
	}
	entries, err := s.history.ListHistory(ctx, userID, provider)
	if err != nil {
		s.log.Warn("session history load failed", slog.Any("error", err), slog.String("provider", provider))
		return prompt
	}
	contextBlock := history.FormatContext(entries)
	if strings.TrimSpace(contextBlock) == "" {
		return prompt
	}
	return contextBlock + "\nUSER: " + prompt
}

func (s *Service) persistHistoryPair(ctx context.Context, provider, userID, prompt, reply string) {
	if s.history == nil {
		return
	}
	if strings.TrimSpace(prompt) != "" {
		if _, err := s.history.SaveMessage(ctx, history.SaveMessageInput{UserID: userID, ProviderName: provider, Role: "user", Message: prompt}); err != nil {
			s.log.Warn("session history save (user) failed", slog.Any("error", err))
		}
	}
	if strings.TrimSpace(reply) != "" {
		if _, err := s.history.SaveMessage(ctx, history.SaveMessageInput{UserID: userID, ProviderName: provider, Role: "assistant", Message: reply}); err != nil {
			s.log.Warn("session history save (assistant) failed", slog.Any("error", err))
		}
	}
}

func (s *Service) decorateContext(ctx context.Context, provider, apiKey string) context.Context {
	reqCtx := providermanager.ContextWithProvider(ctx, provider)
	return providers.ContextWithAPIKey(reqCtx, apiKey)
}

func (s *Service) historyBudget(provider string, reserved int) int {
	if s.providers == nil {
		return 0
	}
	caps, ok := s.providers.CapabilitiesFor(provider)
	if !ok || caps.Limits.MaxTextTokens <= 0 {
		return 0
	}
	budget := caps.Limits.MaxTextTokens
	if reserved > 0 && reserved < budget {
		budget -= reserved
	}
	return budget
}

func validateSessionInput(input SessionInput) error {
	if strings.TrimSpace(input.UserID) == "" {
		return ErrUserIDRequired
	}
	if strings.TrimSpace(input.Provider) == "" {
		return ErrProviderRequired
	}
	return nil
}

func estimateTokens(parts ...string) int {
	total := 0
	for _, part := range parts {
		total += history.EstimateTokens(part)
	}
	if total <= 0 {
		return 1
	}
	return total
}

func cloneMetadata(meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(meta))
	for k, v := range meta {
		cloned[k] = v
	}
	return cloned
}
