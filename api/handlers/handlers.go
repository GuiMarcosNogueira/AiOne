package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"log/slog"

	"github.com/midia/aione/internal/providers/dto"
	providermanager "github.com/midia/aione/internal/services/provider"
)

// API wires HTTP handlers to the provider manager.
type API struct {
	log     *slog.Logger
	manager *providermanager.Manager
}

// New creates an API handler bundle.
func New(log *slog.Logger, manager *providermanager.Manager) *API {
	return &API{log: log, manager: manager}
}

// Chat handles POST /v1/chat requests.
func (a *API) Chat(w http.ResponseWriter, r *http.Request) {
	handlePost(a, w, r, func(ctx context.Context, req dto.TextReq) (any, string, error) {
		res, err := a.manager.TextGenerate(ctx, req)
		return res.Data, res.Provider, err
	})
}

// Image handles POST /v1/image requests.
func (a *API) Image(w http.ResponseWriter, r *http.Request) {
	handlePost(a, w, r, func(ctx context.Context, req dto.ImageReq) (any, string, error) {
		res, err := a.manager.ImageGenerate(ctx, req)
		return res.Data, res.Provider, err
	})
}

// Video handles POST /v1/video requests.
func (a *API) Video(w http.ResponseWriter, r *http.Request) {
	handlePost(a, w, r, func(ctx context.Context, req dto.VideoReq) (any, string, error) {
		res, err := a.manager.VideoGenerate(ctx, req)
		return res.Data, res.Provider, err
	})
}

// STT handles POST /v1/stt requests.
func (a *API) STT(w http.ResponseWriter, r *http.Request) {
	handlePost(a, w, r, func(ctx context.Context, req dto.STTReq) (any, string, error) {
		res, err := a.manager.SpeechToText(ctx, req)
		return res.Data, res.Provider, err
	})
}

// TTS handles POST /v1/tts requests.
func (a *API) TTS(w http.ResponseWriter, r *http.Request) {
	handlePost(a, w, r, func(ctx context.Context, req dto.TTSReq) (any, string, error) {
		res, err := a.manager.TextToSpeech(ctx, req)
		return res.Data, res.Provider, err
	})
}

// Embeddings handles POST /v1/embeddings requests.
func (a *API) Embeddings(w http.ResponseWriter, r *http.Request) {
	handlePost(a, w, r, func(ctx context.Context, req dto.EmbeddingsReq) (any, string, error) {
		res, err := a.manager.Embeddings(ctx, req)
		return res.Data, res.Provider, err
	})
}

// Moderation handles POST /v1/moderation requests.
func (a *API) Moderation(w http.ResponseWriter, r *http.Request) {
	handlePost(a, w, r, func(ctx context.Context, req dto.ModerationReq) (any, string, error) {
		res, err := a.manager.Moderation(ctx, req)
		return res.Data, res.Provider, err
	})
}

// Providers handles GET /v1/providers requests.
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
	if errors.Is(err, providermanager.ErrNoProviders) {
		a.writeJSON(w, http.StatusServiceUnavailable, responseEnvelope{Error: err.Error()})
		return
	}
	if errors.Is(err, providermanager.ErrCapabilityUnavailable) {
		a.writeJSON(w, http.StatusNotImplemented, responseEnvelope{Error: err.Error()})
		return
	}
	if errors.Is(err, providermanager.ErrRateLimited) {
		a.writeJSON(w, http.StatusTooManyRequests, responseEnvelope{Error: err.Error()})
		return
	}
	if errors.Is(err, providermanager.ErrCircuitOpen) {
		a.writeJSON(w, http.StatusServiceUnavailable, responseEnvelope{Error: err.Error()})
		return
	}
	if errors.Is(err, providermanager.ErrUnknownProvider) {
		a.writeJSON(w, http.StatusBadRequest, responseEnvelope{Error: err.Error()})
		return
	}

	a.log.Error("provider request failed", slog.Any("error", err))
	a.writeJSON(w, http.StatusInternalServerError, responseEnvelope{Error: "internal error"})
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

type responseEnvelope struct {
	Provider string `json:"provider,omitempty"`
	Data     any    `json:"data,omitempty"`
	Error    string `json:"error,omitempty"`
}

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
