package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"log/slog"

	"github.com/midia/aione/internal/providers/dto"
	"github.com/midia/aione/internal/services/assets"
	"github.com/midia/aione/internal/services/auth"
	providermanager "github.com/midia/aione/internal/services/provider"
	"github.com/midia/aione/internal/services/providersessions"
	"github.com/midia/aione/internal/services/session"
)

// SessionService abstracts the session orchestration layer for easier testing.
type SessionService interface {
	SendMessage(ctx context.Context, input session.MessageInput) (session.Result[dto.TextResp], error)
	GenerateImage(ctx context.Context, input session.ImageInput) (session.Result[dto.ImageResp], error)
	EditImage(ctx context.Context, input session.ImageEditInput) (session.Result[dto.ImageResp], error)
	GenerateVideo(ctx context.Context, input session.VideoInput) (session.Result[dto.VideoResp], error)
	TranscribeAudio(ctx context.Context, input session.AudioInput) (session.Result[dto.STTResp], error)
}

// SessionAPI exposes session-scoped endpoints that proxy messages and media to providers.
type SessionAPI struct {
	log     *slog.Logger
	service SessionService
}

// SessionMessageRequest describes a text message routed through a conversation session.
type SessionMessageRequest struct {
	Prompt          string           `json:"prompt"`
	MaxTokens       int              `json:"max_tokens,omitempty"`
	Temperature     float32          `json:"temperature,omitempty"`
	Media           []dto.MediaInput `json:"media,omitempty"`
	ProviderKey     string           `json:"provider_key,omitempty"`
	SessionID       string           `json:"session_id,omitempty"`
	SessionTitle    string           `json:"session_title,omitempty"`
	SessionMetadata map[string]any   `json:"session_metadata,omitempty"`
	ExpiresAt       *time.Time       `json:"expires_at,omitempty"`
}

// SessionImageRequest wraps image generation parameters inside a provider session.
type SessionImageRequest struct {
	Prompt          string           `json:"prompt"`
	Size            string           `json:"size,omitempty"`
	Media           []dto.MediaInput `json:"media,omitempty"`
	ProviderKey     string           `json:"provider_key,omitempty"`
	SessionID       string           `json:"session_id,omitempty"`
	SessionTitle    string           `json:"session_title,omitempty"`
	SessionMetadata map[string]any   `json:"session_metadata,omitempty"`
	ExpiresAt       *time.Time       `json:"expires_at,omitempty"`
}

// SessionImageEditRequest mixes image edit payloads with session metadata.
type SessionImageEditRequest struct {
	Prompt          string           `json:"prompt"`
	ImageURL        string           `json:"image_url,omitempty"`
	ImageBase64     string           `json:"image_base64,omitempty"`
	MaskURL         string           `json:"mask_url,omitempty"`
	MaskBase64      string           `json:"mask_base64,omitempty"`
	Size            string           `json:"size,omitempty"`
	Media           []dto.MediaInput `json:"media,omitempty"`
	ProviderKey     string           `json:"provider_key,omitempty"`
	SessionID       string           `json:"session_id,omitempty"`
	SessionTitle    string           `json:"session_title,omitempty"`
	SessionMetadata map[string]any   `json:"session_metadata,omitempty"`
	ExpiresAt       *time.Time       `json:"expires_at,omitempty"`
}

// SessionVideoRequest captures video prompts scoped to a session.
type SessionVideoRequest struct {
	Prompt          string           `json:"prompt"`
	DurationSeconds int              `json:"duration_seconds,omitempty"`
	Media           []dto.MediaInput `json:"media,omitempty"`
	ProviderKey     string           `json:"provider_key,omitempty"`
	SessionID       string           `json:"session_id,omitempty"`
	SessionTitle    string           `json:"session_title,omitempty"`
	SessionMetadata map[string]any   `json:"session_metadata,omitempty"`
	ExpiresAt       *time.Time       `json:"expires_at,omitempty"`
}

// SessionAudioRequest represents streaming audio transcription within a session.
type SessionAudioRequest struct {
	AudioURL        string         `json:"audio_url"`
	Language        string         `json:"language,omitempty"`
	ProviderKey     string         `json:"provider_key,omitempty"`
	SessionID       string         `json:"session_id,omitempty"`
	SessionTitle    string         `json:"session_title,omitempty"`
	SessionMetadata map[string]any `json:"session_metadata,omitempty"`
	ExpiresAt       *time.Time     `json:"expires_at,omitempty"`
}

// NewSessionAPI builds a new handler bundle.
func NewSessionAPI(log *slog.Logger, service SessionService) *SessionAPI {
	return &SessionAPI{log: log, service: service}
}

// Handler returns an http.Handler responsible for /session/* routes.
func (a *SessionAPI) Handler() http.Handler {
	return http.HandlerFunc(a.handle)
}

func (a *SessionAPI) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.methodNotAllowed(w, http.MethodPost)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/session/")
	provider, tail := splitSessionPath(path)
	if provider == "" {
		http.NotFound(w, r)
		return
	}
	switch tail {
	case "message":
		a.handleMessage(w, r, provider)
	case "image":
		a.handleImage(w, r, provider)
	case "image/edit":
		a.handleImageEdit(w, r, provider)
	case "video":
		a.handleVideo(w, r, provider)
	case "audio":
		a.handleAudio(w, r, provider)
	default:
		http.NotFound(w, r)
	}
}

// handleMessage proxies POST /session/{provider}/message requests.
// @Summary        Session chat message
// @Tags           session
// @Security       BearerAuth
// @Accept         json
// @Produce        json
// @Param          provider path string true "Provider name"
// @Param          request body SessionMessageRequest true "Message payload"
// @Success        200 {object} ResponseEnvelope
// @Failure        400 {object} ResponseEnvelope
// @Failure        401 {object} ResponseEnvelope
// @Failure        404 {object} ResponseEnvelope
// @Failure        429 {object} ResponseEnvelope
// @Failure        500 {object} ResponseEnvelope
// @Router         /session/{provider}/message [post]
func (a *SessionAPI) handleMessage(w http.ResponseWriter, r *http.Request, provider string) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "missing auth context", http.StatusUnauthorized)
		return
	}
	var payload SessionMessageRequest
	if err := decodeBody(r, &payload); err != nil {
		a.writeJSON(w, http.StatusBadRequest, responseEnvelope{Error: err.Error()})
		return
	}
	res, err := a.service.SendMessage(r.Context(), session.MessageInput{
		SessionInput: session.SessionInput{
			UserID:          claims.UserID,
			Provider:        provider,
			SessionID:       payload.SessionID,
			SessionTitle:    payload.SessionTitle,
			ProviderKey:     payload.ProviderKey,
			SessionMetadata: payload.SessionMetadata,
			ExpiresAt:       payload.ExpiresAt,
		},
		Prompt:      payload.Prompt,
		MaxTokens:   payload.MaxTokens,
		Temperature: payload.Temperature,
		Media:       payload.Media,
	})
	if err != nil {
		a.handleSessionError(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, responseEnvelope{Provider: res.Provider, Data: res.Payload, Session: &res.Session})
}

// handleImage proxies POST /session/{provider}/image requests.
// @Summary        Session image generation
// @Tags           session
// @Security       BearerAuth
// @Accept         json
// @Produce        json
// @Param          provider path string true "Provider name"
// @Param          request body SessionImageRequest true "Image payload"
// @Success        200 {object} ResponseEnvelope
// @Failure        400 {object} ResponseEnvelope
// @Failure        401 {object} ResponseEnvelope
// @Failure        404 {object} ResponseEnvelope
// @Failure        429 {object} ResponseEnvelope
// @Failure        500 {object} ResponseEnvelope
// @Router         /session/{provider}/image [post]
func (a *SessionAPI) handleImage(w http.ResponseWriter, r *http.Request, provider string) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "missing auth context", http.StatusUnauthorized)
		return
	}
	var payload SessionImageRequest
	if err := decodeBody(r, &payload); err != nil {
		a.writeJSON(w, http.StatusBadRequest, responseEnvelope{Error: err.Error()})
		return
	}
	res, err := a.service.GenerateImage(r.Context(), session.ImageInput{
		SessionInput: session.SessionInput{
			UserID:          claims.UserID,
			Provider:        provider,
			SessionID:       payload.SessionID,
			SessionTitle:    payload.SessionTitle,
			ProviderKey:     payload.ProviderKey,
			SessionMetadata: payload.SessionMetadata,
			ExpiresAt:       payload.ExpiresAt,
		},
		Prompt: payload.Prompt,
		Size:   payload.Size,
		Media:  payload.Media,
	})
	if err != nil {
		a.log.Error("session image request failed", slog.Any("error", err), slog.String("provider", provider), slog.String("user", claims.UserID))
		a.handleSessionError(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, responseEnvelope{Provider: res.Provider, Data: res.Payload, Session: &res.Session})
}

// handleImageEdit proxies POST /session/{provider}/image/edit requests.
// @Summary        Session image edit
// @Tags           session
// @Security       BearerAuth
// @Accept         json
// @Accept         mpfd
// @Produce        json
// @Param          provider path string true "Provider name"
// @Param          request body SessionImageEditRequest true "Image edit payload"
// @Success        200 {object} ResponseEnvelope
// @Failure        400 {object} ResponseEnvelope
// @Failure        401 {object} ResponseEnvelope
// @Failure        404 {object} ResponseEnvelope
// @Failure        429 {object} ResponseEnvelope
// @Failure        500 {object} ResponseEnvelope
// @Router         /session/{provider}/image/edit [post]
func (a *SessionAPI) handleImageEdit(w http.ResponseWriter, r *http.Request, provider string) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "missing auth context", http.StatusUnauthorized)
		return
	}
	payload, err := a.decodeSessionImageEditPayload(r)
	if err != nil {
		a.writeJSON(w, http.StatusBadRequest, responseEnvelope{Error: err.Error()})
		return
	}
	res, err := a.service.EditImage(r.Context(), session.ImageEditInput{
		SessionInput: session.SessionInput{
			UserID:          claims.UserID,
			Provider:        provider,
			SessionID:       payload.SessionID,
			SessionTitle:    payload.SessionTitle,
			ProviderKey:     payload.ProviderKey,
			SessionMetadata: payload.SessionMetadata,
			ExpiresAt:       payload.ExpiresAt,
		},
		Prompt:      payload.Prompt,
		Size:        payload.Size,
		Media:       payload.Media,
		ImageURL:    payload.ImageURL,
		ImageBase64: payload.ImageBase64,
		MaskURL:     payload.MaskURL,
		MaskBase64:  payload.MaskBase64,
	})
	if err != nil {
		a.log.Error("session image edit failed", slog.Any("error", err), slog.String("provider", provider), slog.String("user", claims.UserID))
		a.handleSessionError(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, responseEnvelope{Provider: res.Provider, Data: res.Payload, Session: &res.Session})
}

// handleVideo proxies POST /session/{provider}/video requests.
// @Summary        Session video generation
// @Tags           session
// @Security       BearerAuth
// @Accept         json
// @Produce        json
// @Param          provider path string true "Provider name"
// @Param          request body SessionVideoRequest true "Video payload"
// @Success        200 {object} ResponseEnvelope
// @Failure        400 {object} ResponseEnvelope
// @Failure        401 {object} ResponseEnvelope
// @Failure        404 {object} ResponseEnvelope
// @Failure        429 {object} ResponseEnvelope
// @Failure        500 {object} ResponseEnvelope
// @Router         /session/{provider}/video [post]
func (a *SessionAPI) handleVideo(w http.ResponseWriter, r *http.Request, provider string) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "missing auth context", http.StatusUnauthorized)
		return
	}
	var payload SessionVideoRequest
	if err := decodeBody(r, &payload); err != nil {
		a.writeJSON(w, http.StatusBadRequest, responseEnvelope{Error: err.Error()})
		return
	}
	res, err := a.service.GenerateVideo(r.Context(), session.VideoInput{
		SessionInput: session.SessionInput{
			UserID:          claims.UserID,
			Provider:        provider,
			SessionID:       payload.SessionID,
			SessionTitle:    payload.SessionTitle,
			ProviderKey:     payload.ProviderKey,
			SessionMetadata: payload.SessionMetadata,
			ExpiresAt:       payload.ExpiresAt,
		},
		Prompt:          payload.Prompt,
		DurationSeconds: payload.DurationSeconds,
		Media:           payload.Media,
	})
	if err != nil {
		a.handleSessionError(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, responseEnvelope{Provider: res.Provider, Data: res.Payload, Session: &res.Session})
}

// handleAudio proxies POST /session/{provider}/audio requests.
// @Summary        Session audio transcription
// @Tags           session
// @Security       BearerAuth
// @Accept         json
// @Produce        json
// @Param          provider path string true "Provider name"
// @Param          request body SessionAudioRequest true "Audio payload"
// @Success        200 {object} ResponseEnvelope
// @Failure        400 {object} ResponseEnvelope
// @Failure        401 {object} ResponseEnvelope
// @Failure        404 {object} ResponseEnvelope
// @Failure        429 {object} ResponseEnvelope
// @Failure        500 {object} ResponseEnvelope
// @Router         /session/{provider}/audio [post]
func (a *SessionAPI) handleAudio(w http.ResponseWriter, r *http.Request, provider string) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "missing auth context", http.StatusUnauthorized)
		return
	}
	var payload SessionAudioRequest
	if err := decodeBody(r, &payload); err != nil {
		a.writeJSON(w, http.StatusBadRequest, responseEnvelope{Error: err.Error()})
		return
	}
	res, err := a.service.TranscribeAudio(r.Context(), session.AudioInput{
		SessionInput: session.SessionInput{
			UserID:          claims.UserID,
			Provider:        provider,
			SessionID:       payload.SessionID,
			SessionTitle:    payload.SessionTitle,
			ProviderKey:     payload.ProviderKey,
			SessionMetadata: payload.SessionMetadata,
			ExpiresAt:       payload.ExpiresAt,
		},
		AudioURL: payload.AudioURL,
		Language: payload.Language,
	})
	if err != nil {
		a.handleSessionError(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, responseEnvelope{Provider: res.Provider, Data: res.Payload, Session: &res.Session})
}

func (a *SessionAPI) handleSessionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrUserIDRequired),
		errors.Is(err, session.ErrProviderRequired),
		errors.Is(err, session.ErrPromptRequired),
		errors.Is(err, session.ErrAudioURLRequired),
		errors.Is(err, session.ErrImageSourceRequired):
		a.writeJSON(w, http.StatusBadRequest, responseEnvelope{Error: err.Error()})
		return
	case errors.Is(err, providermanager.ErrNoProviders):
		a.writeJSON(w, http.StatusServiceUnavailable, responseEnvelope{Error: err.Error()})
		return
	case errors.Is(err, providermanager.ErrCapabilityUnavailable):
		a.writeJSON(w, http.StatusNotImplemented, responseEnvelope{Error: err.Error()})
		return
	case errors.Is(err, providermanager.ErrRateLimited):
		a.writeJSON(w, http.StatusTooManyRequests, responseEnvelope{Error: err.Error()})
		return
	case errors.Is(err, providermanager.ErrCircuitOpen):
		a.writeJSON(w, http.StatusServiceUnavailable, responseEnvelope{Error: err.Error()})
		return
	case errors.Is(err, providermanager.ErrUnknownProvider):
		a.writeJSON(w, http.StatusBadRequest, responseEnvelope{Error: err.Error()})
		return
	case errors.Is(err, providersessions.ErrSessionNotFound):
		a.writeJSON(w, http.StatusNotFound, responseEnvelope{Error: err.Error()})
		return
	case errors.Is(err, session.ErrSessionProviderMatch):
		a.writeJSON(w, http.StatusBadRequest, responseEnvelope{Error: err.Error()})
		return
	case errors.Is(err, assets.ErrPersistence):
		a.writeJSON(w, http.StatusInternalServerError, responseEnvelope{Error: assets.ErrPersistence.Error()})
		return
	}
	a.log.Error("session request failed", slog.Any("error", err))
	a.writeJSON(w, http.StatusInternalServerError, responseEnvelope{Error: "internal error"})
}

func splitSessionPath(path string) (provider string, tail string) {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return "", ""
	}
	parts := strings.SplitN(trimmed, "/", 2)
	provider = parts[0]
	if len(parts) > 1 {
		tail = parts[1]
	}
	return provider, tail
}

func (a *SessionAPI) writeJSON(w http.ResponseWriter, status int, payload responseEnvelope) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		a.log.Error("session response encode failed", slog.Any("error", err))
	}
}

func (a *SessionAPI) methodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	a.writeJSON(w, http.StatusMethodNotAllowed, responseEnvelope{Error: "method not allowed"})
}

func (a *SessionAPI) decodeSessionImageEditPayload(r *http.Request) (SessionImageEditRequest, error) {
	if isMultipartRequest(r) {
		mp, err := parseImageEditMultipart(r)
		if err != nil {
			return SessionImageEditRequest{}, err
		}
		return SessionImageEditRequest{
			Prompt:          mp.Request.Prompt,
			ImageURL:        mp.Request.ImageURL,
			ImageBase64:     mp.Request.ImageBase64,
			MaskURL:         mp.Request.MaskURL,
			MaskBase64:      mp.Request.MaskBase64,
			Size:            mp.Request.Size,
			Media:           mp.Request.Media,
			ProviderKey:     mp.ProviderKey,
			SessionID:       mp.SessionID,
			SessionTitle:    mp.SessionTitle,
			SessionMetadata: mp.SessionMetadata,
			ExpiresAt:       mp.ExpiresAt,
		}, nil
	}
	var payload SessionImageEditRequest
	if err := decodeBody(r, &payload); err != nil {
		return SessionImageEditRequest{}, err
	}
	return payload, nil
}
