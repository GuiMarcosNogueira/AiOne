package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"log/slog"

	"github.com/midia/aione/api/handlers"
	"github.com/midia/aione/internal/services/health"
)

// HealthChecker abstracts the health service dependency.
type HealthChecker interface {
	Check(ctx context.Context) []health.Status
}

var openAPIPath = "openapi.yaml"

// New builds the API mux wiring mandatory routes.
func New(log *slog.Logger, healthSvc HealthChecker, apiHandlers *handlers.API, authHandlers *handlers.AuthAPI, sessionHandler http.Handler, historyHandler http.Handler, authMiddleware func(http.Handler) http.Handler) http.Handler {
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
	mux.HandleFunc("/v1/video", apiHandlers.Video)
	mux.HandleFunc("/v1/stt", apiHandlers.STT)
	mux.HandleFunc("/v1/tts", apiHandlers.TTS)
	mux.HandleFunc("/v1/embeddings", apiHandlers.Embeddings)
	mux.HandleFunc("/v1/moderation", apiHandlers.Moderation)
	mux.HandleFunc("/v1/providers", apiHandlers.Providers)

	if authHandlers != nil {
		mux.Handle("/auth/register", authHandlers.RegisterHandler())
		mux.Handle("/auth/login", authHandlers.LoginHandler())
		mux.Handle("/auth/refresh", authHandlers.RefreshHandler())
		mux.Handle("/auth/logout", authHandlers.LogoutHandler())
	}

	if sessionHandler != nil && authMiddleware != nil {
		mux.Handle("/providers/", authMiddleware(sessionHandler))
	}

	if historyHandler != nil && authMiddleware != nil {
		mux.Handle("/history/", authMiddleware(historyHandler))
	}

	mux.HandleFunc("/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, openAPIPath)
	})

	mux.HandleFunc("/docs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, swaggerHTML("/openapi.yaml"))
	})

	return mux
}

func writeJSON(w http.ResponseWriter, log *slog.Logger, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Error("failed to encode response", slog.Any("error", err))
	}
}

func swaggerHTML(specURL string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="UTF-8">
	<title>AI Aggregator API Docs</title>
	<link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css" />
</head>
<body>
	<div id="swagger-ui"></div>
	<script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js" crossorigin></script>
	<script>
		window.onload = () => {
			window.ui = SwaggerUIBundle({
				url: '%s',
				dom_id: '#swagger-ui',
			});
		};
	</script>
</body>
</html>`, specURL)
}
