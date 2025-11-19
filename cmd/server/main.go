package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	apihandlers "github.com/midia/aione/api/handlers"
	"github.com/midia/aione/internal/core/config"
	"github.com/midia/aione/internal/core/db"
	"github.com/midia/aione/internal/core/logger"
	"github.com/midia/aione/internal/core/redisclient"
	"github.com/midia/aione/internal/providers"
	geminiprovider "github.com/midia/aione/internal/providers/gemini"
	generichttpprovider "github.com/midia/aione/internal/providers/generichttp"
	mockproviders "github.com/midia/aione/internal/providers/mock"
	openaiprovider "github.com/midia/aione/internal/providers/openai"
	"github.com/midia/aione/internal/services/auth"
	healthsvc "github.com/midia/aione/internal/services/health"
	"github.com/midia/aione/internal/services/history"
	providermanager "github.com/midia/aione/internal/services/provider"
	"github.com/midia/aione/internal/services/providersessions"
	"github.com/midia/aione/internal/services/storage"
	"github.com/midia/aione/internal/services/users"
	"github.com/midia/aione/pkg/encryption"
	httprouter "github.com/midia/aione/pkg/http/router"
)

var notifyContext = signal.NotifyContext

type authComponents struct {
	AuthAPI    *apihandlers.AuthAPI
	SessionAPI *apihandlers.ProviderSessionAPI
	HistoryAPI *apihandlers.HistoryAPI
	HistorySvc *history.Service
	Middleware func(http.Handler) http.Handler
}

func main() {
	cfg := config.Load()
	log := logger.New(cfg.LogLevel)

	log.Info("starting AI aggregator backend")

	modules, cleanupAuth, err := initAuth(context.Background(), log, cfg)
	if err != nil {
		log.Error("auth initialization failed", "error", err)
		return
	}
	if cleanupAuth != nil {
		defer cleanupAuth()
	}

	var authHandlers *apihandlers.AuthAPI
	var sessionHandler http.Handler
	var historyHandler http.Handler
	var historySvc *history.Service
	var authMiddleware func(http.Handler) http.Handler
	if modules != nil {
		authHandlers = modules.AuthAPI
		if modules.SessionAPI != nil {
			sessionHandler = modules.SessionAPI.Handler()
		}
		if modules.HistoryAPI != nil {
			historyHandler = modules.HistoryAPI.Handler()
		}
		historySvc = modules.HistorySvc
		authMiddleware = modules.Middleware
	}

	providersSet := registerProviders(cfg, log)
	managerOpts := []providermanager.Option{
		providermanager.WithDefaultStrategy(providermanager.ParseStrategy(cfg.ProviderManager.DefaultStrategy)),
		providermanager.WithFailoverAttempts(cfg.ProviderManager.FailoverAttempts),
		providermanager.WithCircuitBreaker(providermanager.CircuitBreakerConfig{
			FailureThreshold: cfg.ProviderManager.CircuitBreakerThreshold,
			Cooldown:         cfg.ProviderManager.CircuitBreakerCooldown,
		}),
	}
	if cacheCfg := cfg.ProviderManager.Cache; cacheCfg.RedisAddr != "" {
		redisCache, err := providermanager.NewRedisCache(providermanager.RedisCacheConfig{
			Addr:     cacheCfg.RedisAddr,
			Password: cacheCfg.RedisPassword,
			DB:       cacheCfg.RedisDB,
		})
		if err != nil {
			log.Error("failed to initialize redis cache", "error", err)
		} else {
			managerOpts = append(managerOpts, providermanager.WithCache(redisCache, cacheCfg.TTL))
		}
	}
	providerManager := providermanager.NewManager(providersSet, managerOpts...)
	apiHandlers := apihandlers.New(log, providerManager, historySvc)

	healthService := healthsvc.NewService(providersSet)
	router := httprouter.New(log, healthService, apiHandlers, authHandlers, sessionHandler, historyHandler, authMiddleware)

	srv := &http.Server{
		Addr:    ":" + cfg.HTTPPort,
		Handler: router,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server crashed", "error", err)
		}
	}()

	ctx, stop := notifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	<-ctx.Done()
	log.Info("shutting down server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "error", err)
		return
	}

	log.Info("server stopped cleanly")
}

func registerProviders(cfg config.Config, log *slog.Logger) []providers.Provider {
	var list []providers.Provider
	if cfg.OpenAI.APIKey == "" {
		log.Warn("skipping openai provider", "reason", "missing api key")
	} else {
		prov, err := openaiprovider.NewProvider(openaiprovider.Config{
			APIKey:             cfg.OpenAI.APIKey,
			BaseURL:            cfg.OpenAI.BaseURL,
			ChatModel:          cfg.OpenAI.ChatModel,
			ImageModel:         cfg.OpenAI.ImageModel,
			TranscriptionModel: cfg.OpenAI.TranscriptionModel,
			EmbeddingsModel:    cfg.OpenAI.EmbeddingsModel,
			ModerationModel:    cfg.OpenAI.ModerationModel,
			Timeout:            cfg.OpenAI.RequestTimeout,
		})
		if err != nil {
			log.Error("failed to initialize openai provider", "error", err)
		} else {
			list = append(list, prov)
		}
	}
	if cfg.Gemini.APIKey == "" {
		log.Warn("skipping gemini provider", "reason", "missing api key")
	} else {
		prov, err := geminiprovider.NewProvider(geminiprovider.Config{
			APIKey:             cfg.Gemini.APIKey,
			BaseURL:            cfg.Gemini.BaseURL,
			TextModel:          cfg.Gemini.TextModel,
			VisionModel:        cfg.Gemini.VisionModel,
			ImageModel:         cfg.Gemini.ImageModel,
			VideoModel:         cfg.Gemini.VideoModel,
			TranscriptionModel: cfg.Gemini.TranscriptionModel,
			EmbeddingsModel:    cfg.Gemini.EmbeddingsModel,
			Timeout:            cfg.Gemini.Timeout,
			MaxRetries:         cfg.Gemini.MaxRetries,
			MaxUploadMB:        cfg.Gemini.MaxUploadMB,
			AllowedMIMETypes:   cfg.Gemini.AllowedMIMETypes,
		})
		if err != nil {
			log.Error("failed to initialize gemini provider", "error", err)
		} else {
			list = append(list, prov)
		}
	}
	list = append(list,
		mockproviders.New("mock-claude"),
		mockproviders.New("mock-grok"),
	)

	if dir := strings.TrimSpace(cfg.GenericHTTP.ConfigDir); dir != "" {
		genericProviders, err := generichttpprovider.LoadFromDir(dir)
		if err != nil {
			log.Error("failed to load generic providers", "dir", dir, "error", err)
		} else if len(genericProviders) > 0 {
			list = append(list, genericProviders...)
		}
	}
	return list
}

func initAuth(ctx context.Context, log *slog.Logger, cfg config.Config) (*authComponents, func(), error) {
	missing := []string{}
	if strings.TrimSpace(cfg.Database.URL) == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if cfg.Auth.AccessSecret == "" {
		missing = append(missing, "AUTH_ACCESS_SECRET")
	}
	if cfg.Auth.RefreshSecret == "" {
		missing = append(missing, "AUTH_REFRESH_SECRET")
	}
	if cfg.Auth.SessionRedis.Addr == "" {
		missing = append(missing, "AUTH_SESSION_REDIS_ADDR")
	}
	if len(missing) > 0 {
		log.Warn("auth module disabled due to missing configuration", "fields", strings.Join(missing, ","))
		return nil, nil, nil
	}

	dbConn, err := db.Connect(ctx, cfg.Database.URL)
	if err != nil {
		return nil, nil, fmt.Errorf("connect database: %w", err)
	}
	cleanupFns := []func(){func() { dbConn.Close() }}

	userRepo := users.NewPostgresRepository(dbConn)
	hasher := auth.NewHasher(cfg.Auth.Argon.Memory, cfg.Auth.Argon.Iterations, cfg.Auth.Argon.SaltLength, cfg.Auth.Argon.KeyLength, cfg.Auth.Argon.Parallelism)
	tokenManager, err := auth.NewTokenManager(cfg.Auth.AccessSecret, cfg.Auth.RefreshSecret)
	if err != nil {
		runCleanup(cleanupFns)
		return nil, nil, fmt.Errorf("token manager: %w", err)
	}
	sessionClient, err := redisclient.Connect(redisclient.Config{
		Addr:     cfg.Auth.SessionRedis.Addr,
		Password: cfg.Auth.SessionRedis.Password,
		DB:       cfg.Auth.SessionRedis.DB,
	})
	if err != nil {
		runCleanup(cleanupFns)
		return nil, nil, fmt.Errorf("session redis: %w", err)
	}
	cleanupFns = append(cleanupFns, func() { sessionClient.Close() })
	sessionStore, err := auth.NewSessionStore(sessionClient, cfg.Auth.SessionPrefix)
	if err != nil {
		runCleanup(cleanupFns)
		return nil, nil, fmt.Errorf("session store: %w", err)
	}

	authService, err := auth.NewService(userRepo, hasher, tokenManager, sessionStore, cfg.Auth.AccessTTL, cfg.Auth.RefreshTTL)
	if err != nil {
		runCleanup(cleanupFns)
		return nil, nil, fmt.Errorf("auth service: %w", err)
	}

	var rateLimiter *auth.RateLimiter
	if cfg.Auth.RateLimit.Redis.Addr != "" && cfg.Auth.RateLimit.MaxAttempts > 0 {
		rateLimiterClient, err := redisclient.Connect(redisclient.Config{
			Addr:     cfg.Auth.RateLimit.Redis.Addr,
			Password: cfg.Auth.RateLimit.Redis.Password,
			DB:       cfg.Auth.RateLimit.Redis.DB,
		})
		if err != nil {
			log.Error("failed to connect rate limit redis", "error", err)
		} else {
			cleanupFns = append(cleanupFns, func() { rateLimiterClient.Close() })
			rateLimiter, err = auth.NewRateLimiter(rateLimiterClient, cfg.Auth.RateLimit.Window, cfg.Auth.RateLimit.MaxAttempts)
			if err != nil {
				log.Error("failed to initialize rate limiter", "error", err)
			}
		}
	}

	cleanup := func() { runCleanup(cleanupFns) }
	var sessionAPI *apihandlers.ProviderSessionAPI
	var historyAPI *apihandlers.HistoryAPI
	var historySvc *history.Service
	if cfg.Security.ProviderSession.PrimaryKeyID == "" || len(cfg.Security.ProviderSession.Keys) == 0 {
		log.Warn("provider session endpoints disabled", "reason", "missing encryption key config")
	} else {
		sessionRepo := providersessions.NewPostgresRepository(dbConn)
		encMgr, err := encryption.NewManager(cfg.Security.ProviderSession.PrimaryKeyID, cfg.Security.ProviderSession.Keys)
		if err != nil {
			log.Error("failed to initialize provider session encryption", slog.Any("error", err))
		} else {
			sessionService, err := providersessions.NewService(sessionRepo, encMgr)
			if err != nil {
				log.Error("failed to initialize provider session service", slog.Any("error", err))
			} else {
				sessionAPI = apihandlers.NewProviderSessionAPI(log, sessionService)
			}
		}
	}
	historyRepo := history.NewPostgresRepository(dbConn)
	storageBackend := storage.NewLocal(cfg.Storage.UploadDir)
	historySvc, err = history.NewService(historyRepo, storageBackend)
	if err != nil {
		log.Error("failed to initialize history service", slog.Any("error", err))
	} else {
		historyAPI = apihandlers.NewHistoryAPI(log, historySvc)
	}

	components := &authComponents{
		AuthAPI:    apihandlers.NewAuthAPI(log, authService, rateLimiter),
		SessionAPI: sessionAPI,
		HistoryAPI: historyAPI,
		HistorySvc: historySvc,
		Middleware: auth.AuthMiddleware(tokenManager),
	}
	return components, cleanup, nil
}

func runCleanup(funcs []func()) {
	for _, fn := range funcs {
		fn()
	}
}
