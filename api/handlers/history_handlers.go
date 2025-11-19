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
	provider, tail := splitHistoryPath(strings.TrimPrefix(r.URL.Path, "/history/"))
	if provider == "" {
		http.NotFound(w, r)
		return
	}
	switch {
	case r.Method == http.MethodGet && tail == "":
		h.list(w, r, provider)
	case r.Method == http.MethodDelete && tail == "clear":
		h.clear(w, r, provider)
	default:
		http.NotFound(w, r)
	}
}

func (h *HistoryAPI) list(w http.ResponseWriter, r *http.Request, provider string) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "missing auth context", http.StatusUnauthorized)
		return
	}
	entries, err := h.service.ListHistory(r.Context(), claims.UserID, provider)
	if err != nil {
		h.log.Error("list history failed", slog.Any("error", err))
		h.writeJSON(w, http.StatusInternalServerError, responseEnvelope{Error: "failed to load history"})
		return
	}
	h.writeJSON(w, http.StatusOK, responseEnvelope{Data: entries})
}

func (h *HistoryAPI) clear(w http.ResponseWriter, r *http.Request, provider string) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, "missing auth context", http.StatusUnauthorized)
		return
	}
	if err := h.service.DeleteHistory(r.Context(), claims.UserID, provider); err != nil {
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

func splitHistoryPath(path string) (provider string, tail string) {
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
