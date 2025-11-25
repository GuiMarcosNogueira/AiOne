package router

import (
	"context"
	"encoding/json"
	"net/http"

	"log/slog"

	"github.com/midia/aione/api/handlers"
	"github.com/midia/aione/internal/services/health"
	httpSwagger "github.com/swaggo/http-swagger/v2"
)

// HealthChecker abstracts the health service dependency.
type HealthChecker interface {
	Check(ctx context.Context) []health.Status
}

// New builds the API mux wiring mandatory routes.
func New(log *slog.Logger, healthSvc HealthChecker, apiHandlers *handlers.API, authHandlers *handlers.AuthAPI, providerSessionHandler http.Handler, historyHandler http.Handler, conversationHandler http.Handler, mediaHandler http.Handler, authMiddleware func(http.Handler) http.Handler) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		statuses := healthSvc.Check(r.Context())
		writeJSON(w, log, http.StatusOK, map[string]any{
			"status":    "ok",
			"providers": statuses,
		})
	})

	mux.HandleFunc("/v1/chat", apiHandlers.Chat)
	mux.HandleFunc("/v1/image", apiHandlers.Image)
	mux.HandleFunc("/v1/image/edit", apiHandlers.ImageEdit)
	mux.HandleFunc("/v1/video", apiHandlers.Video)
	mux.HandleFunc("/v1/stt", apiHandlers.STT)
	mux.HandleFunc("/v1/tts", apiHandlers.TTS)
	mux.HandleFunc("/v1/embeddings", apiHandlers.Embeddings)
	mux.HandleFunc("/v1/moderation", apiHandlers.Moderation)
	mux.HandleFunc("/v1/providers", apiHandlers.Providers)
	mux.HandleFunc("/v1/models", apiHandlers.Models)

	if authHandlers != nil {
		mux.Handle("/auth/register", authHandlers.RegisterHandler())
		mux.Handle("/auth/login", authHandlers.LoginHandler())
		mux.Handle("/auth/refresh", authHandlers.RefreshHandler())
		mux.Handle("/auth/logout", authHandlers.LogoutHandler())
	}

	if providerSessionHandler != nil && authMiddleware != nil {
		mux.Handle("/providers/", authMiddleware(providerSessionHandler))
	}

	if historyHandler != nil && authMiddleware != nil {
		mux.Handle("/history/", authMiddleware(historyHandler))
	}

	if conversationHandler != nil && authMiddleware != nil {
		mux.Handle("/session/", authMiddleware(conversationHandler))
	}

	mux.Handle("/docs", http.RedirectHandler("/docs/", http.StatusPermanentRedirect))
	mux.Handle("/docs/", httpSwagger.Handler())

	if mediaHandler != nil {
		mux.Handle("/media/", mediaHandler)
	}

	return withCORS(mux)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, log *slog.Logger, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Error("failed to encode response", slog.Any("error", err))
	}
}
