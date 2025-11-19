package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	apihandlers "github.com/midia/aione/api/handlers"
	"github.com/midia/aione/internal/core/config"
	"github.com/midia/aione/internal/core/logger"
	"github.com/midia/aione/internal/providers"
	geminiprovider "github.com/midia/aione/internal/providers/gemini"
	generichttpprovider "github.com/midia/aione/internal/providers/generichttp"
	mockproviders "github.com/midia/aione/internal/providers/mock"
	openaiprovider "github.com/midia/aione/internal/providers/openai"
	healthsvc "github.com/midia/aione/internal/services/health"
	providermanager "github.com/midia/aione/internal/services/provider"
	httprouter "github.com/midia/aione/pkg/http/router"
)

var notifyContext = signal.NotifyContext

func main() {
	cfg := config.Load()
	log := logger.New(cfg.LogLevel)

	log.Info("starting AI aggregator backend")

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
	apiHandlers := apihandlers.New(log, providerManager)

	healthService := healthsvc.NewService(providersSet)
	router := httprouter.New(log, healthService, apiHandlers)

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
