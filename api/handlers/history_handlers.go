package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"log/slog"

	"github.com/midia/aione/internal/services/auth"
	"github.com/midia/aione/internal/services/history"
)

// HistoryAPI exposes endpoints to list and clear chat history per provider.
type HistoryAPI struct {
	log     *slog.Logger
	service *history.Service
}

// NewHistoryAPI builds a new handler bundle.
func NewHistoryAPI(log *slog.Logger, service *history.Service) *HistoryAPI {
	return &HistoryAPI{log: log, service: service}
}

// Handler returns an http.Handler serving /history/* routes.
func (h *HistoryAPI) Handler() http.Handler {
	return http.HandlerFunc(h.handle)
}

func (h *HistoryAPI) handle(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/history/"), "/")
	if sessionID == "" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.listSession(w, r, sessionID)
	case http.MethodDelete:
		h.deleteSession(w, r, sessionID)
	default:
		h.methodNotAllowed(w, http.MethodGet+", "+http.MethodDelete)
	}
}

// listSession handles GET /history/{session_id} requests.
// @Summary        Fetch session history
// @Tags           history
// @Security       BearerAuth
// @Produce        json
// @Param          session_id path string true "Session identifier"
// @Success        200 {object} ResponseEnvelope
// @Failure        401 {object} ResponseEnvelope
// @Failure        500 {object} ResponseEnvelope
// @Router         /history/{session_id} [get]
func (h *HistoryAPI) listSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "missing auth context", http.StatusUnauthorized)
		return
	}
	entries, err := h.service.ListSessionHistory(r.Context(), claims.UserID, sessionID)
	if err != nil {
		h.log.Error("list history failed", slog.Any("error", err))
		h.writeJSON(w, http.StatusInternalServerError, responseEnvelope{Error: "failed to load history"})
		return
	}
	h.writeJSON(w, http.StatusOK, responseEnvelope{Data: entries})
}

// deleteSession handles DELETE /history/{session_id} requests.
// @Summary        Delete session history
// @Tags           history
// @Security       BearerAuth
// @Produce        json
// @Param          session_id path string true "Session identifier"
// @Success        200 {object} ResponseEnvelope
// @Failure        401 {object} ResponseEnvelope
// @Failure        500 {object} ResponseEnvelope
// @Router         /history/{session_id} [delete]
func (h *HistoryAPI) deleteSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "missing auth context", http.StatusUnauthorized)
		return
	}
	if err := h.service.DeleteSessionHistory(r.Context(), claims.UserID, sessionID); err != nil {
		h.log.Error("delete history failed", slog.Any("error", err))
		h.writeJSON(w, http.StatusInternalServerError, responseEnvelope{Error: "failed to clear history"})
		return
	}
	h.writeJSON(w, http.StatusOK, responseEnvelope{Data: map[string]string{"status": "cleared"}})
}

func (h *HistoryAPI) writeJSON(w http.ResponseWriter, status int, payload responseEnvelope) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		h.log.Error("history response encode failed", slog.Any("error", err))
	}
}

func (h *HistoryAPI) methodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	h.writeJSON(w, http.StatusMethodNotAllowed, responseEnvelope{Error: "method not allowed"})
}
