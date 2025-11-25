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
	ErrUserIDRequired       = errors.New("session: user id required")
	ErrProviderRequired     = errors.New("session: provider required")
	ErrPromptRequired       = errors.New("session: prompt required")
	ErrAudioURLRequired     = errors.New("session: audio_url required")
	ErrProviderKeyRequired  = errors.New("session: provider key required")
	ErrSessionProviderMatch = errors.New("session: provider mismatch")
	ErrImageSourceRequired  = errors.New("session: image source required")
)

// ProviderClient abstracts the provider manager dependency for easier testing.
type ProviderClient interface {
	CapabilitiesFor(name string) (providers.Capabilities, bool)
	TextGenerate(ctx context.Context, req dto.TextReq) (providermanager.Result[dto.TextResp], error)
	ImageGenerate(ctx context.Context, req dto.ImageReq) (providermanager.Result[dto.ImageResp], error)
	ImageEdit(ctx context.Context, req dto.ImageEditReq) (providermanager.Result[dto.ImageResp], error)
	VideoGenerate(ctx context.Context, req dto.VideoReq) (providermanager.Result[dto.VideoResp], error)
	SpeechToText(ctx context.Context, req dto.STTReq) (providermanager.Result[dto.STTResp], error)
}

// SessionStore exposes provider session operations used by the orchestrator.
type SessionStore interface {
	CreateSession(ctx context.Context, input providersessions.CreateSessionInput) (providersessions.SessionDetails, error)
	GetSession(ctx context.Context, userID, sessionID string) (providersessions.SessionDetails, error)
	RecordUsage(ctx context.Context, input providersessions.UsageInput) (providersessions.SessionDetails, error)
}

// HistoryStore supplies the subset of history features required for session flows.
type HistoryStore interface {
	TruncateSessionHistoryToTokenLimit(ctx context.Context, userID, sessionID string, limit int) (int, error)
	ListSessionHistory(ctx context.Context, userID, sessionID string) ([]history.Entry, error)
	SaveMessage(ctx context.Context, input history.SaveMessageInput) (history.Entry, error)
}

// Service coordinates session-aware interactions with providers.
type Service struct {
	log       *slog.Logger
	providers ProviderClient
	sessions  SessionStore
	history   HistoryStore
	assets    MediaNormalizer
}

// NewService wires a new session interaction service.
func NewService(log *slog.Logger, providers ProviderClient, sessions SessionStore, historyStore HistoryStore, assets MediaNormalizer) (*Service, error) {
	if providers == nil {
		return nil, errors.New("session: provider client required")
	}
	if sessions == nil {
		return nil, errors.New("session: session store required")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{log: log, providers: providers, sessions: sessions, history: historyStore, assets: assets}, nil
}

// MediaNormalizer persists inline media data and exposes a public URL.
type MediaNormalizer interface {
	NormalizeImage(ctx context.Context, image dto.ImageResp) (dto.ImageResp, error)
	NormalizeVideo(ctx context.Context, video dto.VideoResp) (dto.VideoResp, error)
}

// SessionInput carries the shared metadata required for session-scoped calls.
type SessionInput struct {
	UserID          string
	Provider        string
	SessionID       string
	ProviderKey     string
	SessionMetadata map[string]any
	ExpiresAt       *time.Time
	SessionTitle    string
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

// ImageEditInput captures an instruction to edit an existing image asset.
type ImageEditInput struct {
	SessionInput
	Prompt      string
	ImageURL    string
	ImageBase64 string
	MaskURL     string
	MaskBase64  string
	Size        string
	Media       []dto.MediaInput
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
	session, providerName, normalizedProvider, err := s.ensureSession(ctx, input.SessionInput, input.Prompt)
	if err != nil {
		return Result[dto.TextResp]{}, err
	}
	limit := s.historyBudget(providerName, input.MaxTokens)
	s.truncateHistory(ctx, input.UserID, session.ID, normalizedProvider, limit)
	prompt := s.mergeHistoryContext(ctx, input.UserID, session.ID, input.Prompt)

	req := dto.TextReq{
		Prompt:      prompt,
		MaxTokens:   input.MaxTokens,
		Temperature: input.Temperature,
		Media:       input.Media,
	}
	reqCtx := s.decorateContext(ctx, providerName, input.ProviderKey)
	res, err := s.providers.TextGenerate(reqCtx, req)
	if err != nil {
		return Result[dto.TextResp]{}, err
	}

	s.persistHistoryPair(ctx, normalizedProvider, session.ID, input.UserID, input.Prompt, res.Data.Content)
	tokens := estimateTokens(input.Prompt, res.Data.Content)
	updated, err := s.recordUsage(ctx, session.ID, input.SessionInput, normalizedProvider, tokens)
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
	session, providerName, normalizedProvider, err := s.ensureSession(ctx, input.SessionInput, input.Prompt)
	if err != nil {
		return Result[dto.ImageResp]{}, err
	}
	limit := s.historyBudget(providerName, 0)
	s.truncateHistory(ctx, input.UserID, session.ID, normalizedProvider, limit)

	req := dto.ImageReq{Prompt: input.Prompt, Size: input.Size, Media: input.Media}
	reqCtx := s.decorateContext(ctx, providerName, input.ProviderKey)
	res, err := s.providers.ImageGenerate(reqCtx, req)
	if err != nil {
		return Result[dto.ImageResp]{}, err
	}
	image, err := s.normalizeImage(ctx, res.Data)
	if err != nil {
		s.log.Error("session image normalization failed", slog.Any("error", err), slog.String("user", input.UserID), slog.String("session", session.ID), slog.String("provider", normalizedProvider))
		return Result[dto.ImageResp]{}, err
	}

	s.persistHistoryPair(ctx, normalizedProvider, session.ID, input.UserID, input.Prompt, image.URL)
	tokens := estimateTokens(input.Prompt, image.URL)
	updated, err := s.recordUsage(ctx, session.ID, input.SessionInput, normalizedProvider, tokens)
	if err != nil {
		return Result[dto.ImageResp]{}, err
	}

	return Result[dto.ImageResp]{Provider: res.Provider, Payload: image, Session: updated}, nil
}

// EditImage proxies an image edit/inpainting request with session tracking.
func (s *Service) EditImage(ctx context.Context, input ImageEditInput) (Result[dto.ImageResp], error) {
	if err := validateSessionInput(input.SessionInput); err != nil {
		return Result[dto.ImageResp]{}, err
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return Result[dto.ImageResp]{}, ErrPromptRequired
	}
	if strings.TrimSpace(input.ImageURL) == "" && strings.TrimSpace(input.ImageBase64) == "" {
		return Result[dto.ImageResp]{}, ErrImageSourceRequired
	}
	session, providerName, normalizedProvider, err := s.ensureSession(ctx, input.SessionInput, input.Prompt)
	if err != nil {
		return Result[dto.ImageResp]{}, err
	}
	limit := s.historyBudget(providerName, 0)
	s.truncateHistory(ctx, input.UserID, session.ID, normalizedProvider, limit)
	req := dto.ImageEditReq{
		Prompt:      input.Prompt,
		ImageURL:    input.ImageURL,
		ImageBase64: input.ImageBase64,
		MaskURL:     input.MaskURL,
		MaskBase64:  input.MaskBase64,
		Size:        input.Size,
		Media:       input.Media,
	}
	reqCtx := s.decorateContext(ctx, providerName, input.ProviderKey)
	res, err := s.providers.ImageEdit(reqCtx, req)
	if err != nil {
		return Result[dto.ImageResp]{}, err
	}
	image, err := s.normalizeImage(ctx, res.Data)
	if err != nil {
		s.log.Error("session image normalization failed", slog.Any("error", err), slog.String("user", input.UserID), slog.String("session", session.ID), slog.String("provider", normalizedProvider))
		return Result[dto.ImageResp]{}, err
	}
	s.persistHistoryPair(ctx, normalizedProvider, session.ID, input.UserID, input.Prompt, image.URL)
	tokens := estimateTokens(input.Prompt, image.URL)
	updated, err := s.recordUsage(ctx, session.ID, input.SessionInput, normalizedProvider, tokens)
	if err != nil {
		return Result[dto.ImageResp]{}, err
	}
	return Result[dto.ImageResp]{Provider: res.Provider, Payload: image, Session: updated}, nil
}

// GenerateVideo proxies a video generation request.
func (s *Service) GenerateVideo(ctx context.Context, input VideoInput) (Result[dto.VideoResp], error) {
	if err := validateSessionInput(input.SessionInput); err != nil {
		return Result[dto.VideoResp]{}, err
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return Result[dto.VideoResp]{}, ErrPromptRequired
	}
	session, providerName, normalizedProvider, err := s.ensureSession(ctx, input.SessionInput, input.Prompt)
	if err != nil {
		return Result[dto.VideoResp]{}, err
	}
	limit := s.historyBudget(providerName, 0)
	s.truncateHistory(ctx, input.UserID, session.ID, normalizedProvider, limit)

	req := dto.VideoReq{Prompt: input.Prompt, DurationSeconds: input.DurationSeconds, Media: input.Media}
	reqCtx := s.decorateContext(ctx, providerName, input.ProviderKey)
	res, err := s.providers.VideoGenerate(reqCtx, req)
	if err != nil {
		return Result[dto.VideoResp]{}, err
	}
	video, err := s.normalizeVideo(ctx, res.Data)
	if err != nil {
		s.log.Error("session video normalization failed", slog.Any("error", err), slog.String("user", input.UserID), slog.String("session", session.ID), slog.String("provider", normalizedProvider))
		return Result[dto.VideoResp]{}, err
	}

	s.persistHistoryPair(ctx, normalizedProvider, session.ID, input.UserID, input.Prompt, video.URL)
	tokens := estimateTokens(input.Prompt, video.URL)
	updated, err := s.recordUsage(ctx, session.ID, input.SessionInput, normalizedProvider, tokens)
	if err != nil {
		return Result[dto.VideoResp]{}, err
	}

	return Result[dto.VideoResp]{Provider: res.Provider, Payload: video, Session: updated}, nil
}

// TranscribeAudio forwards an audio transcription with history + usage tracking.
func (s *Service) TranscribeAudio(ctx context.Context, input AudioInput) (Result[dto.STTResp], error) {
	if err := validateSessionInput(input.SessionInput); err != nil {
		return Result[dto.STTResp]{}, err
	}
	if strings.TrimSpace(input.AudioURL) == "" {
		return Result[dto.STTResp]{}, ErrAudioURLRequired
	}
	session, providerName, normalizedProvider, err := s.ensureSession(ctx, input.SessionInput, input.AudioURL)
	if err != nil {
		return Result[dto.STTResp]{}, err
	}
	limit := s.historyBudget(providerName, 0)
	s.truncateHistory(ctx, input.UserID, session.ID, normalizedProvider, limit)

	req := dto.STTReq{AudioURL: input.AudioURL, Language: input.Language}
	reqCtx := s.decorateContext(ctx, providerName, input.ProviderKey)
	res, err := s.providers.SpeechToText(reqCtx, req)
	if err != nil {
		return Result[dto.STTResp]{}, err
	}

	s.persistHistoryPair(ctx, normalizedProvider, session.ID, input.UserID, fmt.Sprintf("audio:%s", input.AudioURL), res.Data.Transcript)
	tokens := estimateTokens(input.AudioURL, res.Data.Transcript)
	updated, err := s.recordUsage(ctx, session.ID, input.SessionInput, normalizedProvider, tokens)
	if err != nil {
		return Result[dto.STTResp]{}, err
	}

	return Result[dto.STTResp]{Provider: res.Provider, Payload: res.Data, Session: updated}, nil
}

func (s *Service) ensureSession(ctx context.Context, input SessionInput, titleHint string) (providersessions.SessionDetails, string, string, error) {
	provider := strings.TrimSpace(input.Provider)
	if provider == "" {
		return providersessions.SessionDetails{}, "", "", ErrProviderRequired
	}
	normalized := strings.ToLower(provider)
	if sessionID := strings.TrimSpace(input.SessionID); sessionID != "" {
		sess, err := s.sessions.GetSession(ctx, input.UserID, sessionID)
		if err != nil {
			return providersessions.SessionDetails{}, "", "", err
		}
		if slug := strings.ToLower(strings.TrimSpace(sess.ProviderName)); slug != "" && slug != normalized {
			return providersessions.SessionDetails{}, "", "", ErrSessionProviderMatch
		}
		return sess, provider, normalized, nil
	}
	title := strings.TrimSpace(input.SessionTitle)
	if title == "" {
		title = deriveSessionTitle(titleHint, provider)
	}
	sess, err := s.sessions.CreateSession(ctx, providersessions.CreateSessionInput{
		UserID:       input.UserID,
		ProviderName: normalized,
		Title:        title,
		Metadata:     cloneMetadata(input.SessionMetadata),
		ExpiresAt:    input.ExpiresAt,
	})
	if err != nil {
		return providersessions.SessionDetails{}, "", "", err
	}
	return sess, provider, normalized, nil
}

func (s *Service) recordUsage(ctx context.Context, sessionID string, input SessionInput, provider string, tokens int) (providersessions.SessionDetails, error) {
	if tokens <= 0 {
		tokens = 1
	}
	usage := providersessions.UsageInput{
		SessionID:       sessionID,
		UserID:          input.UserID,
		ProviderName:    provider,
		TokensDelta:     int64(tokens),
		LastInteraction: time.Now().UTC(),
		Metadata:        cloneMetadata(input.SessionMetadata),
		ExpiresAt:       input.ExpiresAt,
	}
	return s.sessions.RecordUsage(ctx, usage)
}

func (s *Service) normalizeImage(ctx context.Context, image dto.ImageResp) (dto.ImageResp, error) {
	if s == nil || s.assets == nil {
		return image, nil
	}
	return s.assets.NormalizeImage(ctx, image)
}

func (s *Service) normalizeVideo(ctx context.Context, video dto.VideoResp) (dto.VideoResp, error) {
	if s == nil || s.assets == nil {
		return video, nil
	}
	return s.assets.NormalizeVideo(ctx, video)
}

func (s *Service) truncateHistory(ctx context.Context, userID, sessionID, provider string, limit int) {
	if s.history == nil || limit <= 0 {
		return
	}
	removed, err := s.history.TruncateSessionHistoryToTokenLimit(ctx, userID, sessionID, limit)
	if err != nil {
		s.log.Warn("session history truncate failed", slog.Any("error", err), slog.String("provider", provider), slog.String("user", userID), slog.String("session", sessionID))
		return
	}
	if removed == 0 {
		return
	}
	message := fmt.Sprintf("system trimmed %d entries to honor token budget (%d)", removed, limit)
	if _, err := s.history.SaveMessage(ctx, history.SaveMessageInput{SessionID: sessionID, UserID: userID, ProviderName: provider, Role: "system", Message: message}); err != nil {
		s.log.Warn("session history truncate log failed", slog.Any("error", err))
	}
}

func (s *Service) mergeHistoryContext(ctx context.Context, userID, sessionID string, prompt string) string {
	if s.history == nil {
		return prompt
	}
	entries, err := s.history.ListSessionHistory(ctx, userID, sessionID)
	if err != nil {
		s.log.Warn("session history load failed", slog.Any("error", err), slog.String("session", sessionID))
		return prompt
	}
	contextBlock := history.FormatContext(entries)
	if strings.TrimSpace(contextBlock) == "" {
		return prompt
	}
	return contextBlock + "\nUSER: " + prompt
}

func (s *Service) persistHistoryPair(ctx context.Context, provider, sessionID, userID, prompt, reply string) {
	if s.history == nil {
		return
	}
	if strings.TrimSpace(prompt) != "" {
		if _, err := s.history.SaveMessage(ctx, history.SaveMessageInput{SessionID: sessionID, UserID: userID, ProviderName: provider, Role: "user", Message: prompt}); err != nil {
			s.log.Warn("session history save (user) failed", slog.Any("error", err))
		}
	}
	if strings.TrimSpace(reply) != "" {
		if _, err := s.history.SaveMessage(ctx, history.SaveMessageInput{SessionID: sessionID, UserID: userID, ProviderName: provider, Role: "assistant", Message: reply}); err != nil {
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

func deriveSessionTitle(hint, provider string) string {
	trimmed := strings.TrimSpace(hint)
	if trimmed == "" {
		fallback := strings.TrimSpace(provider)
		if fallback == "" {
			fallback = "session"
		}
		runes := []rune(fallback)
		if len(runes) > 0 {
			runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
			fallback = string(runes)
		}
		return fmt.Sprintf("%s session", fallback)
	}
	runes := []rune(trimmed)
	if len(runes) > 60 {
		runes = runes[:60]
	}
	return strings.TrimSpace(string(runes))
}
