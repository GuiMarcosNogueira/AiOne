package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"log/slog"

	"github.com/midia/aione/internal/services/auth"
	"github.com/midia/aione/internal/services/providersessions"
)

// ProviderSessionAPI exposes handlers to manage per-user provider sessions.
type ProviderSessionAPI struct {
	log     *slog.Logger
	service *providersessions.Service
}

// NewProviderSessionAPI wires a new handler bundle.
func NewProviderSessionAPI(log *slog.Logger, service *providersessions.Service) *ProviderSessionAPI {
	return &ProviderSessionAPI{log: log, service: service}
}

// Handler returns an http.Handler that routes provider session requests.
func (a *ProviderSessionAPI) Handler() http.Handler {
	return http.HandlerFunc(a.handle)
}

func (a *ProviderSessionAPI) handle(w http.ResponseWriter, r *http.Request) {
	provider, tail := extractProviderPath(strings.TrimPrefix(r.URL.Path, "/providers/"))
	if provider == "" {
		http.NotFound(w, r)
		return
	}
	provider = strings.ToLower(provider)
	switch {
	case r.Method == http.MethodPost && tail == "set-key":
		a.setKey(w, r, provider)
	case r.Method == http.MethodGet && tail == "session":
		a.getSession(w, r, provider)
	case r.Method == http.MethodDelete && tail == "session/reset":
		a.resetSession(w, r, provider)
	default:
		http.NotFound(w, r)
	}
}

func (a *ProviderSessionAPI) setKey(w http.ResponseWriter, r *http.Request, provider string) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "missing auth context", http.StatusUnauthorized)
		return
	}
	var payload struct {
		ProviderKey string         `json:"provider_key"`
		Metadata    map[string]any `json:"metadata"`
		ExpiresAt   *time.Time     `json:"expires_at"`
	}
	if err := decodeBody(r, &payload); err != nil {
		a.writeJSON(w, http.StatusBadRequest, responseEnvelope{Error: err.Error()})
		return
	}
	input := providersessions.SetKeyInput{
		UserID:       claims.UserID,
		ProviderName: provider,
		ProviderKey:  payload.ProviderKey,
		Metadata:     payload.Metadata,
		ExpiresAt:    payload.ExpiresAt,
	}
	sess, err := a.service.SetProviderKey(r.Context(), input)
	if err != nil {
		a.handleSessionError(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, responseEnvelope{Data: sess})
}

func (a *ProviderSessionAPI) getSession(w http.ResponseWriter, r *http.Request, provider string) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "missing auth context", http.StatusUnauthorized)
		return
	}
	sess, err := a.service.GetSession(r.Context(), claims.UserID, provider)
	if err != nil {
		a.handleSessionError(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, responseEnvelope{Data: sess})
}

func (a *ProviderSessionAPI) resetSession(w http.ResponseWriter, r *http.Request, provider string) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "missing auth context", http.StatusUnauthorized)
		return
	}
	if err := a.service.ResetSession(r.Context(), claims.UserID, provider); err != nil {
		a.handleSessionError(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, responseEnvelope{Data: map[string]string{"status": "reset"}})
}

func (a *ProviderSessionAPI) handleSessionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, providersessions.ErrSessionNotFound):
		a.writeJSON(w, http.StatusNotFound, responseEnvelope{Error: err.Error()})
	case errors.Is(err, providersessions.ErrUserIDRequired),
		errors.Is(err, providersessions.ErrProviderRequired),
		errors.Is(err, providersessions.ErrProviderKeyRequired):
		a.writeJSON(w, http.StatusBadRequest, responseEnvelope{Error: err.Error()})
	default:
		a.log.Error("provider session request failed", slog.Any("error", err))
		a.writeJSON(w, http.StatusInternalServerError, responseEnvelope{Error: "internal error"})
	}
}

func extractProviderPath(path string) (provider string, tail string) {
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

func (a *ProviderSessionAPI) writeJSON(w http.ResponseWriter, status int, payload responseEnvelope) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		a.log.Error("failed to encode provider session response", slog.Any("error", err))
	}
}
