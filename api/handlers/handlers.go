package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"log/slog"

	"github.com/midia/aione/internal/providers/dto"
	"github.com/midia/aione/internal/services/assets"
	"github.com/midia/aione/internal/services/auth"
	"github.com/midia/aione/internal/services/history"
	providermanager "github.com/midia/aione/internal/services/provider"
	"github.com/midia/aione/internal/services/providersessions"
)

// API wires HTTP handlers to the provider manager.
type API struct {
	log     *slog.Logger
	manager *providermanager.Manager
	history *history.Service
	assets  AssetManager
}

// AssetManager normalizes provider media payloads into publicly accessible URLs.
type AssetManager interface {
	NormalizeImage(ctx context.Context, image dto.ImageResp) (dto.ImageResp, error)
	NormalizeVideo(ctx context.Context, video dto.VideoResp) (dto.VideoResp, error)
}

// New creates an API handler bundle.
func New(log *slog.Logger, manager *providermanager.Manager, historySvc *history.Service, assets AssetManager) *API {
	return &API{log: log, manager: manager, history: historySvc, assets: assets}
}

// Chat handles POST /v1/chat requests.
// @Summary        Text completion
// @Description    Generates chat completions via the orchestrated providers with optional persisted history context.
// @Tags           chat
// @Accept         json
// @Produce        json
// @Param          strategy query string false "Routing strategy override (round_robin, random, failover, etc)"
// @Param          request body dto.TextReq true "Chat payload"
// @Success        200 {object} ResponseEnvelope
// @Failure        400 {object} ResponseEnvelope
// @Failure        429 {object} ResponseEnvelope
// @Failure        500 {object} ResponseEnvelope
// @Router         /v1/chat [post]
func (a *API) Chat(w http.ResponseWriter, r *http.Request) {
	userID := claimsUserID(r.Context())
	handlePost(a, w, r, func(ctx context.Context, req dto.TextReq) (any, string, error) {
		originalPrompt := req.Prompt
		providerOverride := strings.ToLower(strings.TrimSpace(req.PreferredProvider()))
		if userID != "" && a.history != nil && providerOverride != "" {
			a.applyHistoryContext(ctx, &req, userID, providerOverride, req.MaxTokens)
		}
		res, err := a.manager.TextGenerate(ctx, req)
		if err == nil && userID != "" && a.history != nil {
			a.recordChatTurns(ctx, userID, providerOverride, res.Provider, originalPrompt, res.Data.Content, req.MaxTokens)
		}
		return res.Data, res.Provider, err
	})
}

// Image handles POST /v1/image requests.
// @Summary        Image generation
// @Description    Generates images with optional asset normalization before returning URLs.
// @Tags           image
// @Accept         json
// @Produce        json
// @Param          strategy query string false "Routing strategy override"
// @Param          request body dto.ImageReq true "Image generation payload"
// @Success        200 {object} ResponseEnvelope
// @Failure        400 {object} ResponseEnvelope
// @Failure        429 {object} ResponseEnvelope
// @Failure        500 {object} ResponseEnvelope
// @Router         /v1/image [post]
func (a *API) Image(w http.ResponseWriter, r *http.Request) {
	handlePost(a, w, r, func(ctx context.Context, req dto.ImageReq) (any, string, error) {
		res, err := a.manager.ImageGenerate(ctx, req)
		if err != nil {
			return res.Data, res.Provider, err
		}
		image, err := a.normalizeImage(r.Context(), res.Data)
		if err != nil {
			a.log.Error("image normalization failed", slog.Any("error", err))
			return res.Data, res.Provider, err
		}
		return image, res.Provider, nil
	})
}

// ImageEdit handles POST /v1/image/edit requests.
// @Summary        Image editing
// @Description    Applies edits or inpainting to existing images via multipart or JSON payloads.
// @Tags           image
// @Accept         json
// @Accept         mpfd
// @Produce        json
// @Param          strategy query string false "Routing strategy override"
// @Param          request body dto.ImageEditReq true "Image edit payload"
// @Success        200 {object} ResponseEnvelope
// @Failure        400 {object} ResponseEnvelope
// @Failure        429 {object} ResponseEnvelope
// @Failure        500 {object} ResponseEnvelope
// @Router         /v1/image/edit [post]
func (a *API) ImageEdit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.methodNotAllowed(w, http.MethodPost)
		return
	}
	var req dto.ImageEditReq
	if isMultipartRequest(r) {
		payload, err := parseImageEditMultipart(r)
		if err != nil {
			a.writeJSON(w, http.StatusBadRequest, responseEnvelope{Error: err.Error()})
			return
		}
		req = payload.Request
	} else {
		if err := decodeBody(r, &req); err != nil {
			a.writeJSON(w, http.StatusBadRequest, responseEnvelope{Error: err.Error()})
			return
		}
	}

	ctx := providermanager.ContextWithStrategy(r.Context(), providermanager.ParseStrategy(r.URL.Query().Get("strategy")))
	ctx = applyProviderOverride(ctx, req)
	res, err := a.manager.ImageEdit(ctx, req)
	if err != nil {
		a.handleError(w, err)
		return
	}
	image, err := a.normalizeImage(r.Context(), res.Data)
	if err != nil {
		a.log.Error("image normalization failed", slog.Any("error", err))
		a.handleError(w, err)
		return
	}

	a.writeJSON(w, http.StatusOK, responseEnvelope{Provider: res.Provider, Data: image})
}

// Video handles POST /v1/video requests.
// @Summary        Video generation
// @Description    Generates short-form videos using provider-specific capabilities.
// @Tags           video
// @Accept         json
// @Produce        json
// @Param          strategy query string false "Routing strategy override"
// @Param          request body dto.VideoReq true "Video generation payload"
// @Success        200 {object} ResponseEnvelope
// @Failure        400 {object} ResponseEnvelope
// @Failure        429 {object} ResponseEnvelope
// @Failure        500 {object} ResponseEnvelope
// @Router         /v1/video [post]
func (a *API) Video(w http.ResponseWriter, r *http.Request) {
	handlePost(a, w, r, func(ctx context.Context, req dto.VideoReq) (any, string, error) {
		res, err := a.manager.VideoGenerate(ctx, req)
		if err != nil {
			return res.Data, res.Provider, err
		}
		video, err := a.normalizeVideo(r.Context(), res.Data)
		if err != nil {
			a.log.Error("video normalization failed", slog.Any("error", err))
			return res.Data, res.Provider, err
		}
		return video, res.Provider, nil
	})
}

// STT handles POST /v1/stt requests.
// @Summary        Speech to text
// @Description    Transcribes remote audio URLs via the selected provider.
// @Tags           speech
// @Accept         json
// @Produce        json
// @Param          strategy query string false "Routing strategy override"
// @Param          request body dto.STTReq true "Speech-to-text payload"
// @Success        200 {object} ResponseEnvelope
// @Failure        400 {object} ResponseEnvelope
// @Failure        429 {object} ResponseEnvelope
// @Failure        500 {object} ResponseEnvelope
// @Router         /v1/stt [post]
func (a *API) STT(w http.ResponseWriter, r *http.Request) {
	handlePost(a, w, r, func(ctx context.Context, req dto.STTReq) (any, string, error) {
		res, err := a.manager.SpeechToText(ctx, req)
		return res.Data, res.Provider, err
	})
}

// TTS handles POST /v1/tts requests.
// @Summary        Text to speech
// @Description    Synthesizes speech audio for the provided text.
// @Tags           speech
// @Accept         json
// @Produce        json
// @Param          strategy query string false "Routing strategy override"
// @Param          request body dto.TTSReq true "Text-to-speech payload"
// @Success        200 {object} ResponseEnvelope
// @Failure        400 {object} ResponseEnvelope
// @Failure        429 {object} ResponseEnvelope
// @Failure        500 {object} ResponseEnvelope
// @Router         /v1/tts [post]
func (a *API) TTS(w http.ResponseWriter, r *http.Request) {
	handlePost(a, w, r, func(ctx context.Context, req dto.TTSReq) (any, string, error) {
		res, err := a.manager.TextToSpeech(ctx, req)
		return res.Data, res.Provider, err
	})
}

// Embeddings handles POST /v1/embeddings requests.
// @Summary        Generate embeddings
// @Description    Produces vector embeddings for one or more inputs.
// @Tags           embeddings
// @Accept         json
// @Produce        json
// @Param          strategy query string false "Routing strategy override"
// @Param          request body dto.EmbeddingsReq true "Embeddings payload"
// @Success        200 {object} ResponseEnvelope
// @Failure        400 {object} ResponseEnvelope
// @Failure        429 {object} ResponseEnvelope
// @Failure        500 {object} ResponseEnvelope
// @Router         /v1/embeddings [post]
func (a *API) Embeddings(w http.ResponseWriter, r *http.Request) {
	handlePost(a, w, r, func(ctx context.Context, req dto.EmbeddingsReq) (any, string, error) {
		res, err := a.manager.Embeddings(ctx, req)
		return res.Data, res.Provider, err
	})
}

// Moderation handles POST /v1/moderation requests.
// @Summary        Moderate content
// @Description    Evaluates input text for policy violations using provider moderation APIs.
// @Tags           moderation
// @Accept         json
// @Produce        json
// @Param          strategy query string false "Routing strategy override"
// @Param          request body dto.ModerationReq true "Moderation payload"
// @Success        200 {object} ResponseEnvelope
// @Failure        400 {object} ResponseEnvelope
// @Failure        429 {object} ResponseEnvelope
// @Failure        500 {object} ResponseEnvelope
// @Router         /v1/moderation [post]
func (a *API) Moderation(w http.ResponseWriter, r *http.Request) {
	handlePost(a, w, r, func(ctx context.Context, req dto.ModerationReq) (any, string, error) {
		res, err := a.manager.Moderation(ctx, req)
		return res.Data, res.Provider, err
	})
}

// Providers handles GET /v1/providers requests.
// @Summary        List providers
// @Description    Returns the registered providers plus their capability matrix.
// @Tags           providers
// @Produce        json
// @Success        200 {object} ResponseEnvelope
// @Router         /v1/providers [get]
func (a *API) Providers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.methodNotAllowed(w, http.MethodGet)
		return
	}

	providers := a.manager.ListProviders()
	matrix := a.manager.CapabilityMatrix()
	a.writeJSON(w, http.StatusOK, responseEnvelope{Data: map[string]any{
		"providers": providers,
		"matrix":    matrix,
	}})
}

// Models handles GET /v1/models requests to expose provider catalogs.
// @Summary        List provider models
// @Description    Returns model catalogs for every provider or a specific provider when filtered.
// @Tags           providers
// @Produce        json
// @Param          provider query string false "Filter results to a single provider"
// @Success        200 {object} ResponseEnvelope
// @Failure        400 {object} ResponseEnvelope
// @Failure        404 {object} ResponseEnvelope
// @Failure        500 {object} ResponseEnvelope
// @Router         /v1/models [get]
func (a *API) Models(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.methodNotAllowed(w, http.MethodGet)
		return
	}
	if a.manager == nil {
		a.writeJSON(w, http.StatusServiceUnavailable, responseEnvelope{Error: "provider manager unavailable"})
		return
	}
	provider := strings.TrimSpace(r.URL.Query().Get("provider"))
	if provider != "" {
		catalog, err := a.manager.ModelCatalog(r.Context(), provider)
		if err != nil {
			switch {
			case errors.Is(err, providermanager.ErrUnknownProvider):
				a.writeJSON(w, http.StatusBadRequest, responseEnvelope{Error: err.Error()})
			case errors.Is(err, providermanager.ErrModelCatalogUnavailable):
				a.writeJSON(w, http.StatusNotFound, responseEnvelope{Error: err.Error()})
			default:
				a.log.Error("model catalog fetch failed", slog.Any("error", err), slog.String("provider", provider))
				a.writeJSON(w, http.StatusInternalServerError, responseEnvelope{Error: "internal error"})
			}
			return
		}
		a.writeJSON(w, http.StatusOK, responseEnvelope{Data: map[string]any{
			"provider": provider,
			"models":   catalog,
		}})
		return
	}
	catalogs := a.manager.AllModelCatalogs(r.Context())
	a.writeJSON(w, http.StatusOK, responseEnvelope{Data: catalogs})
}

func (a *API) normalizeImage(ctx context.Context, image dto.ImageResp) (dto.ImageResp, error) {
	if a.assets == nil {
		return image, nil
	}

	return a.assets.NormalizeImage(ctx, image)
}

func (a *API) normalizeVideo(ctx context.Context, video dto.VideoResp) (dto.VideoResp, error) {
	if a.assets == nil {
		return video, nil
	}
	return a.assets.NormalizeVideo(ctx, video)
}

func handlePost[T any](a *API, w http.ResponseWriter, r *http.Request, fn func(context.Context, T) (any, string, error)) {
	if r.Method != http.MethodPost {
		a.methodNotAllowed(w, http.MethodPost)
		return
	}

	var req T
	if err := decodeBody(r, &req); err != nil {
		a.writeJSON(w, http.StatusBadRequest, responseEnvelope{Error: err.Error()})
		return
	}

	ctx := providermanager.ContextWithStrategy(r.Context(), providermanager.ParseStrategy(r.URL.Query().Get("strategy")))
	ctx = applyProviderOverride(ctx, req)
	data, providerName, err := fn(ctx, req)
	if err != nil {
		a.handleError(w, err)
		return
	}

	a.writeJSON(w, http.StatusOK, responseEnvelope{Provider: providerName, Data: data})
}

func decodeBody[T any](r *http.Request, v *T) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return err
	}
	return nil
}

func (a *API) handleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, providermanager.ErrNoProviders):
		a.writeJSON(w, http.StatusServiceUnavailable, responseEnvelope{Error: err.Error()})
	case errors.Is(err, providermanager.ErrCapabilityUnavailable):
		a.writeJSON(w, http.StatusNotImplemented, responseEnvelope{Error: err.Error()})
	case errors.Is(err, providermanager.ErrRateLimited):
		a.writeJSON(w, http.StatusTooManyRequests, responseEnvelope{Error: err.Error()})
	case errors.Is(err, providermanager.ErrCircuitOpen):
		a.writeJSON(w, http.StatusServiceUnavailable, responseEnvelope{Error: err.Error()})
	case errors.Is(err, providermanager.ErrUnknownProvider):
		a.writeJSON(w, http.StatusBadRequest, responseEnvelope{Error: err.Error()})
	case errors.Is(err, assets.ErrPersistence):
		a.writeJSON(w, http.StatusInternalServerError, responseEnvelope{Error: assets.ErrPersistence.Error()})
	default:
		a.log.Error("provider request failed", slog.Any("error", err))
		a.writeJSON(w, http.StatusInternalServerError, responseEnvelope{Error: "internal error"})
	}
}

func (a *API) methodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	a.writeJSON(w, http.StatusMethodNotAllowed, responseEnvelope{Error: "method not allowed"})
}

func (a *API) writeJSON(w http.ResponseWriter, status int, payload responseEnvelope) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		a.log.Error("failed to encode response", slog.Any("error", err))
	}
}

// ResponseEnvelope wraps API responses with provider metadata and optional session info.
type ResponseEnvelope struct {
	Provider string                           `json:"provider,omitempty"`
	Data     any                              `json:"data,omitempty"`
	Session  *providersessions.SessionDetails `json:"session,omitempty"`
	Error    string                           `json:"error,omitempty"`
}

// backward compatibility for existing code paths still referencing the alias
type responseEnvelope = ResponseEnvelope

func applyProviderOverride(ctx context.Context, req any) context.Context {
	aware, ok := any(req).(dto.ProviderAware)
	if !ok {
		return ctx
	}
	if name := aware.PreferredProvider(); name != "" {
		return providermanager.ContextWithProvider(ctx, name)
	}
	return ctx
}

func (a *API) applyHistoryContext(ctx context.Context, req *dto.TextReq, userID, provider string, maxTokens int) {
	limit := a.historyBudget(provider, maxTokens)
	if limit > 0 {
		if _, err := a.history.TruncateHistoryToTokenLimit(ctx, userID, provider, limit); err != nil {
			a.log.Warn("failed to truncate history", slog.Any("error", err))
		}
	}
	entries, err := a.history.ListHistory(ctx, userID, provider)
	if err != nil {
		a.log.Warn("failed to load history", slog.Any("error", err))
		return
	}
	if prompt := history.FormatContext(entries); prompt != "" {
		req.Prompt = prompt + "\nUSER: " + req.Prompt
	}
}

func (a *API) recordChatTurns(ctx context.Context, userID, providerHint, providerUsed, prompt, reply string, maxTokens int) {
	provider := strings.TrimSpace(providerUsed)
	if provider == "" {
		provider = strings.TrimSpace(providerHint)
	}
	if provider == "" {
		return
	}
	if _, err := a.history.SaveMessage(ctx, history.SaveMessageInput{
		UserID:       userID,
		ProviderName: provider,
		Role:         "user",
		Message:      prompt,
	}); err != nil {
		a.log.Warn("failed to save user history", slog.Any("error", err))
	}
	if strings.TrimSpace(reply) != "" {
		if _, err := a.history.SaveMessage(ctx, history.SaveMessageInput{
			UserID:       userID,
			ProviderName: provider,
			Role:         "assistant",
			Message:      reply,
		}); err != nil {
			a.log.Warn("failed to save assistant history", slog.Any("error", err))
		}
	}
	if limit := a.historyBudget(provider, maxTokens); limit > 0 {
		if _, err := a.history.TruncateHistoryToTokenLimit(ctx, userID, provider, limit); err != nil {
			a.log.Warn("failed to enforce history limit", slog.Any("error", err))
		}
	}
}

func (a *API) historyBudget(provider string, maxTokens int) int {
	if a.manager == nil {
		return 0
	}
	caps, ok := a.manager.CapabilitiesFor(provider)
	if !ok || caps.Limits.MaxTextTokens <= 0 {
		return 0
	}
	budget := caps.Limits.MaxTextTokens
	if maxTokens > 0 && maxTokens < budget {
		budget -= maxTokens
	}
	return budget
}

func claimsUserID(ctx context.Context) string {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return ""
	}
	return claims.UserID
}
