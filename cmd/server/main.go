//go:generate swag init -g main.go -o ../api/docs --outputTypes go,json,yaml

// @title           AiOne API
// @version         1.0
// @description     Unified AI gateway that proxies multiple providers.
// @BasePath        /
// @schemes         http https
// @accept          json
// @produce         json
// @securityDefinitions.apikey BearerAuth
// @in              header
// @name            Authorization
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	_ "github.com/midia/aione/api/docs"
	grpcserver "github.com/midia/aione/api/grpc/server"
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
	"github.com/midia/aione/internal/services/assets"
	"github.com/midia/aione/internal/services/auth"
	healthsvc "github.com/midia/aione/internal/services/health"
	"github.com/midia/aione/internal/services/history"
	providermanager "github.com/midia/aione/internal/services/provider"
	"github.com/midia/aione/internal/services/providersessions"
	"github.com/midia/aione/internal/services/session"
	"github.com/midia/aione/internal/services/storage"
	"github.com/midia/aione/internal/services/users"
	httpmiddleware "github.com/midia/aione/pkg/http/middleware"
	httprouter "github.com/midia/aione/pkg/http/router"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

var notifyContext = signal.NotifyContext

type authComponents struct {
	AuthAPI    *apihandlers.AuthAPI
	SessionAPI *apihandlers.ProviderSessionAPI
	HistoryAPI *apihandlers.HistoryAPI
	AuthSvc    *auth.Service
	Tokens     *auth.TokenManager
	HistorySvc *history.Service
	SessionSvc *providersessions.Service
	Middleware func(http.Handler) http.Handler
}

func main() {
	cfg := config.Load()
	log := logger.New(cfg.LogLevel)

	log.Info("starting AiOne backend")

	var assetStorage storage.Storage
	var mediaHandler http.Handler
	if dir := strings.TrimSpace(cfg.Storage.UploadDir); dir != "" {
		assetStorage = storage.NewLocal(dir)
		if cfg.Storage.ServeFromAPI {
			mediaHandler = http.StripPrefix("/media/", http.FileServer(http.Dir(dir)))
		} else {
			log.Info("media files served by external storage", "public_base_url", cfg.Storage.PublicBaseURL)
		}
	} else {
		log.Warn("asset storage disabled", "reason", "missing upload dir")
	}
	assetService := assets.NewService(log, assetStorage, cfg.Storage.PublicBaseURL)

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
	var conversationSvc *session.Service
	var conversationHandler http.Handler
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
	if cfg.Logging.ProviderCalls {
		managerOpts = append(managerOpts, providermanager.WithProviderLogging(log, true))
	}
	providerManager := providermanager.NewManager(providersSet, managerOpts...)
	apiHandlers := apihandlers.New(log, providerManager, historySvc, assetService)

	if modules != nil && modules.SessionSvc != nil && historySvc != nil {
		conversationSvc, err := session.NewService(log, providerManager, modules.SessionSvc, historySvc, assetService)
		if err != nil {
			log.Error("failed to initialize session conversation service", "error", err)
		} else {
			conversationHandler = apihandlers.NewSessionAPI(log, conversationSvc).Handler()
		}
	}

	healthService := healthsvc.NewService(providersSet)
	router := httprouter.New(log, healthService, apiHandlers, authHandlers, sessionHandler, historyHandler, conversationHandler, mediaHandler, authMiddleware)
	handler := http.Handler(router)
	if cfg.Logging.HTTPRequests {
		handler = httpmiddleware.RequestLogger(log, httpmiddleware.RequestLogOptions{})(handler)
	}

	srv := &http.Server{
		Addr:    ":" + cfg.HTTPPort,
		Handler: handler,
	}

	var grpcSrv *grpc.Server
	var grpcShutdown func(context.Context)
	if cfg.GRPC.Enabled {
		grpcCfg := grpcserver.Config{
			Log:              log,
			Providers:        providerManager,
			Assets:           assetService,
			Auth:             nil,
			ProviderSessions: nil,
			Conversation:     conversationSvc,
			History:          historySvc,
		}
		var tokenManager *auth.TokenManager
		if modules != nil {
			grpcCfg.Auth = modules.AuthSvc
			grpcCfg.ProviderSessions = modules.SessionSvc
			grpcCfg.History = historySvc
			tokenManager = modules.Tokens
		}
		listener, err := net.Listen("tcp", ":"+cfg.GRPC.Port)
		if err != nil {
			log.Error("failed to bind grpc listener", "error", err, "port", cfg.GRPC.Port)
			return
		}
		serverOpts := grpcServerOptions(cfg.GRPC, log, tokenManager)
		grpcSrv = grpc.NewServer(serverOpts...)
		grpcserver.NewServer(grpcCfg).RegisterServices(grpcSrv)
		if cfg.GRPC.Reflection {
			reflection.Register(grpcSrv)
		}
		go func() {
			log.Info("gRPC server listening", "port", cfg.GRPC.Port)
			if err := grpcSrv.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
				log.Error("grpc server exited", "error", err)
			}
		}()
		grpcShutdown = func(ctx context.Context) {
			done := make(chan struct{})
			go func() {
				grpcSrv.GracefulStop()
				close(done)
			}()
			select {
			case <-done:
			case <-ctx.Done():
				grpcSrv.Stop()
			}
		}
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

	if grpcShutdown != nil {
		grpcShutdown(shutdownCtx)
	}

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "error", err)
		return
	}

	log.Info("server stopped cleanly")
}

func grpcServerOptions(cfg config.GRPCConfig, log *slog.Logger, tokens *auth.TokenManager) []grpc.ServerOption {
	recvLimit := bytesFromMB(cfg.MaxRecvMB, 16)
	sendLimit := bytesFromMB(cfg.MaxSendMB, 16)
	opts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(recvLimit),
		grpc.MaxSendMsgSize(sendLimit),
	}
	if interceptor := grpcAuthUnaryInterceptor(log, tokens); interceptor != nil {
		opts = append(opts, grpc.ChainUnaryInterceptor(interceptor))
	}
	return opts
}

func bytesFromMB(value int, fallback int) int {
	if value <= 0 {
		value = fallback
	}
	return value * 1024 * 1024
}

func grpcAuthUnaryInterceptor(log *slog.Logger, tokens *auth.TokenManager) grpc.UnaryServerInterceptor {
	if tokens == nil {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		token, err := bearerTokenFromContext(ctx)
		if err != nil {
			log.Debug("invalid gRPC authorization header", "method", info.FullMethod, "error", err)
			return nil, status.Error(codes.Unauthenticated, "invalid authorization header")
		}
		if token != "" {
			claims, err := tokens.ParseAccess(token)
			if err != nil {
				return nil, status.Error(codes.Unauthenticated, "invalid bearer token")
			}
			ctx = auth.ContextWithClaims(ctx, auth.Claims{UserID: claims.UserID, Email: claims.Email})
		}
		return handler(ctx, req)
	}
}

func bearerTokenFromContext(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", nil
	}
	for _, key := range []string{"authorization", "grpcgateway-authorization"} {
		if values := md.Get(key); len(values) > 0 {
			header := strings.TrimSpace(values[0])
			if header == "" {
				continue
			}
			parts := strings.SplitN(header, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
				continue
			}
			token := strings.TrimSpace(parts[1])
			if token == "" {
				return "", fmt.Errorf("missing bearer token")
			}
			return token, nil
		}
	}
	return "", nil
}

func registerProviders(cfg config.Config, log *slog.Logger) []providers.Provider {
	var list []providers.Provider
	if cfg.OpenAI.APIKey == "" {
		log.Warn("skipping openai provider", "reason", "missing api key")
	} else {
		prov, err := openaiprovider.NewProvider(openaiprovider.Config{
			APIKey:             cfg.OpenAI.APIKey,
			ChatModel:          cfg.OpenAI.ChatModel,
			ImageModel:         cfg.OpenAI.ImageModel,
			VideoModel:         cfg.OpenAI.VideoModel,
			VideoSize:          cfg.OpenAI.VideoSize,
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
			TextModel:          cfg.Gemini.TextModel,
			VisionModel:        cfg.Gemini.VisionModel,
			ImageModel:         cfg.Gemini.ImageModel,
			VideoModel:         cfg.Gemini.VideoModel,
			TranscriptionModel: cfg.Gemini.TranscriptionModel,
			EmbeddingsModel:    cfg.Gemini.EmbeddingsModel,
			Timeout:            cfg.Gemini.Timeout,
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
	var sessionSvc *providersessions.Service
	var historyAPI *apihandlers.HistoryAPI
	var historySvc *history.Service
	sessionRepo := providersessions.NewPostgresRepository(dbConn)
	sessionService, err := providersessions.NewService(sessionRepo)
	if err != nil {
		log.Error("failed to initialize provider session service", slog.Any("error", err))
	} else {
		sessionSvc = sessionService
		sessionAPI = apihandlers.NewProviderSessionAPI(log, sessionService)
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
		AuthSvc:    authService,
		Tokens:     tokenManager,
		HistorySvc: historySvc,
		SessionSvc: sessionSvc,
		Middleware: auth.AuthMiddleware(tokenManager),
	}
	return components, cleanup, nil
}

func runCleanup(funcs []func()) {
	for _, fn := range funcs {
		fn()
	}
}
