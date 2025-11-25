package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadUsesDefaults(t *testing.T) {
	t.Setenv("HTTP_PORT", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("SHUTDOWN_TIMEOUT", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("PROVIDER_STRATEGY", "")
	t.Setenv("PROVIDER_FAILOVER_ATTEMPTS", "")
	t.Setenv("PROVIDER_CB_THRESHOLD", "")
	t.Setenv("PROVIDER_CB_COOLDOWN", "")
	t.Setenv("REDIS_ADDR", "")
	t.Setenv("REDIS_PASSWORD", "")
	t.Setenv("REDIS_DB", "")
	t.Setenv("CACHE_TTL", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("UPLOAD_DIR", "")
	t.Setenv("STORAGE_PUBLIC_BASE_URL", "")
	t.Setenv("STORAGE_SERVE_FROM_API", "")
	t.Setenv("JOBS_WORKER_INTERVAL", "")
	t.Setenv("JOBS_CALLBACK_MAX_ATTEMPTS", "")
	t.Setenv("JOBS_CALLBACK_BACKOFF", "")
	t.Setenv("JOBS_UPLOAD_MAX_MB", "")
	t.Setenv("LOG_HTTP_REQUESTS", "")
	t.Setenv("LOG_PROVIDER_CALLS", "")

	cfg := Load()
	if cfg.HTTPPort != "8080" {
		t.Fatalf("expected default http port, got %s", cfg.HTTPPort)
	}
	if cfg.ProviderManager.FailoverAttempts != 3 {
		t.Fatalf("expected default failover attempts 3, got %d", cfg.ProviderManager.FailoverAttempts)
	}
	if cfg.Storage.UploadDir != "storage" {
		t.Fatalf("expected default upload dir, got %s", cfg.Storage.UploadDir)
	}
	if cfg.Storage.PublicBaseURL != "" {
		t.Fatalf("expected empty public base url by default")
	}
	if !cfg.Storage.ServeFromAPI {
		t.Fatalf("expected local media handler enabled by default")
	}
	if cfg.Logging.HTTPRequests || cfg.Logging.ProviderCalls {
		t.Fatalf("expected logging disabled by default")
	}
}

func TestLoadReadsEnvironment(t *testing.T) {
	t.Setenv("HTTP_PORT", "9090")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("SHUTDOWN_TIMEOUT", "1")
	t.Setenv("OPENAI_API_KEY", "secret")
	t.Setenv("PROVIDER_STRATEGY", "fast")
	t.Setenv("PROVIDER_FAILOVER_ATTEMPTS", "5")
	t.Setenv("PROVIDER_CB_THRESHOLD", "7")
	t.Setenv("PROVIDER_CB_COOLDOWN", "2")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("REDIS_PASSWORD", "pw")
	t.Setenv("REDIS_DB", "2")
	t.Setenv("CACHE_TTL", "10")
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("UPLOAD_DIR", "uploads")
	t.Setenv("STORAGE_PUBLIC_BASE_URL", "https://cdn.example.com/media")
	t.Setenv("STORAGE_SERVE_FROM_API", "false")
	t.Setenv("JOBS_WORKER_INTERVAL", "4")
	t.Setenv("JOBS_CALLBACK_MAX_ATTEMPTS", "9")
	t.Setenv("JOBS_CALLBACK_BACKOFF", "6")
	t.Setenv("JOBS_UPLOAD_MAX_MB", "500")
	t.Setenv("LOG_HTTP_REQUESTS", "true")
	t.Setenv("LOG_PROVIDER_CALLS", "true")

	cfg := Load()
	if cfg.HTTPPort != "9090" || cfg.LogLevel != "debug" {
		t.Fatalf("expected overrides, got %+v", cfg)
	}
	if cfg.OpenAI.APIKey != "secret" {
		t.Fatalf("expected openai api key from env")
	}
	if cfg.ProviderManager.DefaultStrategy != "fast" {
		t.Fatalf("expected strategy from env")
	}
	if cfg.ProviderManager.FailoverAttempts != 5 {
		t.Fatalf("expected failover attempts from env")
	}
	if cfg.ProviderManager.CircuitBreakerThreshold != 7 {
		t.Fatalf("expected cb threshold from env")
	}
	if cfg.ProviderManager.CircuitBreakerCooldown != 2*time.Second {
		t.Fatalf("expected cb cooldown 2s, got %s", cfg.ProviderManager.CircuitBreakerCooldown)
	}
	if cfg.ProviderManager.Cache.RedisAddr != "localhost:6379" {
		t.Fatalf("expected redis addr override")
	}
	if cfg.ProviderManager.Cache.RedisDB != 2 {
		t.Fatalf("expected redis db override")
	}
	if cfg.ProviderManager.Cache.TTL != 10*time.Second {
		t.Fatalf("expected cache ttl override")
	}
	if cfg.Database.URL != "postgres://example" {
		t.Fatalf("expected database url override")
	}
	if cfg.Storage.UploadDir != "uploads" {
		t.Fatalf("expected upload dir override")
	}
	if cfg.Storage.PublicBaseURL != "https://cdn.example.com/media" {
		t.Fatalf("expected public base url override")
	}
	if cfg.Storage.ServeFromAPI {
		t.Fatalf("expected storage serve flag override")
	}
	if cfg.Jobs.WorkerInterval != 4*time.Second {
		t.Fatalf("expected worker interval override")
	}
	if cfg.Jobs.CallbackMaxAttempts != 9 {
		t.Fatalf("expected callback attempts override")
	}
	if cfg.Jobs.CallbackBackoff != 6*time.Second {
		t.Fatalf("expected callback backoff override")
	}
	if cfg.Jobs.UploadMaxMB != 500 {
		t.Fatalf("expected upload size override")
	}
	if !cfg.Logging.HTTPRequests || !cfg.Logging.ProviderCalls {
		t.Fatalf("expected logging flags from env")
	}
}

func TestGetEnvHelpers(t *testing.T) {
	key := "UNIT_TEST_KEY"
	t.Setenv(key, "value")
	if got := getEnv(key, "fallback"); got != "value" {
		t.Fatalf("expected env value, got %s", got)
	}
	os.Unsetenv(key)
	if got := getEnv(key, "fallback"); got != "fallback" {
		t.Fatalf("expected fallback when env missing, got %s", got)
	}

	t.Setenv("DURATION_KEY", "15")
	if got := getEnvAsDuration("DURATION_KEY", time.Second); got != 15*time.Second {
		t.Fatalf("expected parsed duration, got %s", got)
	}
	t.Setenv("DURATION_KEY", "not-number")
	if got := getEnvAsDuration("DURATION_KEY", 3*time.Second); got != 3*time.Second {
		t.Fatalf("expected fallback on parse error, got %s", got)
	}
	os.Unsetenv("DURATION_KEY")
	if got := getEnvAsDuration("DURATION_KEY", 2*time.Second); got != 2*time.Second {
		t.Fatalf("expected fallback when missing")
	}

	t.Setenv("INT_KEY", "42")
	if got := getEnvAsInt("INT_KEY", 1); got != 42 {
		t.Fatalf("expected parsed int, got %d", got)
	}
	t.Setenv("INT_KEY", "oops")
	if got := getEnvAsInt("INT_KEY", 7); got != 7 {
		t.Fatalf("expected fallback on parse error")
	}
	os.Unsetenv("INT_KEY")
	if got := getEnvAsInt("INT_KEY", 9); got != 9 {
		t.Fatalf("expected fallback when missing")
	}
}
