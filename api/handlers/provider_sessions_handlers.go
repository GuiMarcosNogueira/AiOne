package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
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

// ProviderSessionCreateRequest captures the payload for creating a provider session.
type ProviderSessionCreateRequest struct {
	Title     string         `json:"title"`
	Metadata  map[string]any `json:"metadata"`
	ExpiresAt *time.Time     `json:"expires_at"`
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
	case r.Method == http.MethodPost && tail == "sessions":
		a.createSession(w, r, provider)
	case r.Method == http.MethodGet && tail == "sessions":
		a.listSessions(w, r, provider)
	case strings.HasPrefix(tail, "sessions/"):
		sessionID := strings.TrimPrefix(tail, "sessions/")
		sessionID = strings.Trim(sessionID, "/")
		if sessionID == "" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			a.getSession(w, r, provider, sessionID)
		case http.MethodDelete:
			a.archiveSession(w, r, sessionID)
		default:
			a.methodNotAllowed(w, http.MethodGet+", "+http.MethodDelete)
		}
	default:
		http.NotFound(w, r)
	}
}

// createSession handles POST /providers/{provider}/sessions requests.
// @Summary        Create provider session
// @Tags           provider_sessions
// @Security       BearerAuth
// @Accept         json
// @Produce        json
// @Param          provider path string true "Provider name"
// @Param          request body ProviderSessionCreateRequest true "Session payload"
// @Success        201 {object} ResponseEnvelope
// @Failure        400 {object} ResponseEnvelope
// @Failure        401 {object} ResponseEnvelope
// @Failure        500 {object} ResponseEnvelope
// @Router         /providers/{provider}/sessions [post]
func (a *ProviderSessionAPI) createSession(w http.ResponseWriter, r *http.Request, provider string) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "missing auth context", http.StatusUnauthorized)
		return
	}
	var payload ProviderSessionCreateRequest
	if err := decodeBody(r, &payload); err != nil {
		a.writeJSON(w, http.StatusBadRequest, responseEnvelope{Error: err.Error()})
		return
	}
	sess, err := a.service.CreateSession(r.Context(), providersessions.CreateSessionInput{
		UserID:       claims.UserID,
		ProviderName: provider,
		Title:        payload.Title,
		Metadata:     payload.Metadata,
		ExpiresAt:    payload.ExpiresAt,
	})
	if err != nil {
		a.handleSessionError(w, err)
		return
	}
	a.writeJSON(w, http.StatusCreated, responseEnvelope{Data: sess})
}

// listSessions handles GET /providers/{provider}/sessions requests.
// @Summary        List provider sessions
// @Tags           provider_sessions
// @Security       BearerAuth
// @Produce        json
// @Param          provider path string true "Provider name"
// @Param          limit query int false "Limit the number of sessions"
// @Param          include_archived query bool false "Include archived sessions"
// @Success        200 {object} ResponseEnvelope
// @Failure        401 {object} ResponseEnvelope
// @Failure        500 {object} ResponseEnvelope
// @Router         /providers/{provider}/sessions [get]
func (a *ProviderSessionAPI) listSessions(w http.ResponseWriter, r *http.Request, provider string) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "missing auth context", http.StatusUnauthorized)
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"))
	includeArchived := strings.EqualFold(r.URL.Query().Get("include_archived"), "true")
	sessions, err := a.service.ListSessions(r.Context(), providersessions.ListSessionsInput{
		UserID:          claims.UserID,
		ProviderName:    provider,
		Limit:           limit,
		IncludeArchived: includeArchived,
	})
	if err != nil {
		a.handleSessionError(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, responseEnvelope{Data: sessions})
}

// getSession handles GET /providers/{provider}/sessions/{session_id} requests.
// @Summary        Get provider session
// @Tags           provider_sessions
// @Security       BearerAuth
// @Produce        json
// @Param          provider path string true "Provider name"
// @Param          session_id path string true "Session identifier"
// @Success        200 {object} ResponseEnvelope
// @Failure        400 {object} ResponseEnvelope
// @Failure        401 {object} ResponseEnvelope
// @Failure        404 {object} ResponseEnvelope
// @Failure        500 {object} ResponseEnvelope
// @Router         /providers/{provider}/sessions/{session_id} [get]
func (a *ProviderSessionAPI) getSession(w http.ResponseWriter, r *http.Request, provider, sessionID string) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "missing auth context", http.StatusUnauthorized)
		return
	}
	sess, err := a.service.GetSession(r.Context(), claims.UserID, sessionID)
	if err != nil {
		a.handleSessionError(w, err)
		return
	}
	if sess.ProviderName != provider {
		a.writeJSON(w, http.StatusBadRequest, responseEnvelope{Error: "session provider mismatch"})
		return
	}
	a.writeJSON(w, http.StatusOK, responseEnvelope{Data: sess})
}

// archiveSession handles DELETE /providers/{provider}/sessions/{session_id} requests.
// @Summary        Archive provider session
// @Tags           provider_sessions
// @Security       BearerAuth
// @Produce        json
// @Param          provider path string true "Provider name"
// @Param          session_id path string true "Session identifier"
// @Success        200 {object} ResponseEnvelope
// @Failure        401 {object} ResponseEnvelope
// @Failure        404 {object} ResponseEnvelope
// @Failure        500 {object} ResponseEnvelope
// @Router         /providers/{provider}/sessions/{session_id} [delete]
func (a *ProviderSessionAPI) archiveSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "missing auth context", http.StatusUnauthorized)
		return
	}
	if err := a.service.ArchiveSession(r.Context(), claims.UserID, sessionID); err != nil {
		a.handleSessionError(w, err)
		return
	}
	a.writeJSON(w, http.StatusOK, responseEnvelope{Data: map[string]string{"status": "archived"}})
}

func (a *ProviderSessionAPI) handleSessionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, providersessions.ErrSessionNotFound):
		a.writeJSON(w, http.StatusNotFound, responseEnvelope{Error: err.Error()})
	case errors.Is(err, providersessions.ErrUserIDRequired),
		errors.Is(err, providersessions.ErrProviderRequired),
		errors.Is(err, providersessions.ErrSessionIDRequired),
		errors.Is(err, providersessions.ErrTitleRequired):
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

func (a *ProviderSessionAPI) methodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	w.WriteHeader(http.StatusMethodNotAllowed)
	if err := json.NewEncoder(w).Encode(responseEnvelope{Error: "method not allowed"}); err != nil {
		a.log.Error("failed to encode provider session response", slog.Any("error", err))
	}
}

func parseLimit(raw string) int {
	if raw == "" {
		return 0
	}
	limit, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return limit
}
