package config

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config groups runtime configuration loaded from environment variables.
type Config struct {
	HTTPPort        string
	LogLevel        string
	ShutdownTimeout time.Duration
	OpenAI          OpenAIConfig
	Gemini          GeminiConfig
	GenericHTTP     GenericHTTPConfig
	ProviderManager ProviderManagerConfig
	Database        DatabaseConfig
	Storage         StorageConfig
	Jobs            JobsConfig
	Auth            AuthConfig
	Security        SecurityConfig
	Logging         LoggingConfig
}

// OpenAIConfig captures the knobs for the OpenAI provider adapter.
type OpenAIConfig struct {
	APIKey             string
	ChatModel          string
	ImageModel         string
	VideoModel         string
	VideoSize          string
	TranscriptionModel string
	EmbeddingsModel    string
	ModerationModel    string
	RequestTimeout     time.Duration
}

// GeminiConfig captures the knobs for Google Gemini adapter.
type GeminiConfig struct {
	APIKey             string
	TextModel          string
	VisionModel        string
	ImageModel         string
	VideoModel         string
	TranscriptionModel string
	EmbeddingsModel    string
	Timeout            time.Duration
	MaxUploadMB        int
	AllowedMIMETypes   []string
}

// ProviderManagerConfig exposes routing knobs.
type ProviderManagerConfig struct {
	DefaultStrategy         string
	FailoverAttempts        int
	CircuitBreakerThreshold int
	CircuitBreakerCooldown  time.Duration
	Cache                   CacheConfig
}

// CacheConfig configures the optional Redis cache.
type CacheConfig struct {
	RedisAddr     string
	RedisPassword string
	RedisDB       int
	TTL           time.Duration
}

// DatabaseConfig captures the datasource settings.
type DatabaseConfig struct {
	URL string
}

// StorageConfig customizes the upload storage backend.
type StorageConfig struct {
	UploadDir     string
	PublicBaseURL string
	ServeFromAPI  bool
}

// JobsConfig exposes knobs for long-running job orchestration.
type JobsConfig struct {
	WorkerInterval      time.Duration
	CallbackMaxAttempts int
	CallbackBackoff     time.Duration
	UploadMaxMB         int
}

// GenericHTTPConfig configures on-disk generic provider adapters.
type GenericHTTPConfig struct {
	ConfigDir string
}

// AuthConfig captures authentication and session settings.
type AuthConfig struct {
	AccessSecret  string
	RefreshSecret string
	AccessTTL     time.Duration
	RefreshTTL    time.Duration
	SessionPrefix string
	SessionRedis  RedisConnConfig
	RateLimit     RateLimitConfig
	Argon         Argon2Config
}

// RedisConnConfig defines connection knobs for Redis-backed features.
type RedisConnConfig struct {
	Addr     string
	Password string
	DB       int
}

// RateLimitConfig configures login rate limiting.
type RateLimitConfig struct {
	Window      time.Duration
	MaxAttempts int
	Redis       RedisConnConfig
}

// Argon2Config tunes password hashing costs.
type Argon2Config struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// SecurityConfig captures encryption knobs for user-specific provider sessions.
type SecurityConfig struct {
	ProviderSession ProviderSessionSecurityConfig
}

// ProviderSessionSecurityConfig holds AES key-ring information.
type ProviderSessionSecurityConfig struct {
	PrimaryKeyID string
	Keys         map[string]string
}

// LoggingConfig toggles structured tracing for requests and provider calls.
type LoggingConfig struct {
	HTTPRequests  bool
	ProviderCalls bool
}

// Load builds a Config from environment variables with sane defaults so the
// service can boot even when optional variables are missing.
func Load() Config {
	return Config{
		HTTPPort:        getEnv("HTTP_PORT", "8080"),
		LogLevel:        getEnv("LOG_LEVEL", "info"),
		ShutdownTimeout: getEnvAsDuration("SHUTDOWN_TIMEOUT", 5*time.Second),
		OpenAI: OpenAIConfig{
			APIKey:             getEnv("OPENAI_API_KEY", ""),
			ChatModel:          getEnv("OPENAI_CHAT_MODEL", "gpt-4o-mini"),
			ImageModel:         getEnv("OPENAI_IMAGE_MODEL", "gpt-image-1"),
			VideoModel:         getEnv("OPENAI_VIDEO_MODEL", "sora-2"),
			VideoSize:          getEnv("OPENAI_VIDEO_SIZE", "720x1280"),
			TranscriptionModel: getEnv("OPENAI_TRANSCRIPTION_MODEL", "gpt-4o-mini-transcribe"),
			EmbeddingsModel:    getEnv("OPENAI_EMBEDDINGS_MODEL", "text-embedding-3-large"),
			ModerationModel:    getEnv("OPENAI_MODERATION_MODEL", "omni-moderation-latest"),
			RequestTimeout:     getEnvAsDuration("OPENAI_TIMEOUT", 30*time.Second),
		},
		Gemini: GeminiConfig{
			APIKey:             getEnv("GEMINI_API_KEY", ""),
			TextModel:          getEnv("GEMINI_TEXT_MODEL", "gemini-2.5-flash"),
			VisionModel:        getEnv("GEMINI_VISION_MODEL", "gemini-2.5-pro"),
			ImageModel:         getEnv("GEMINI_IMAGE_MODEL", "gemini-2.5-flash-image"),
			VideoModel:         getEnv("GEMINI_VIDEO_MODEL", "gemini-2.5-flash"),
			TranscriptionModel: getEnv("GEMINI_TRANSCRIPTION_MODEL", "gemini-2.5-flash"),
			EmbeddingsModel:    getEnv("GEMINI_EMBEDDINGS_MODEL", "text-embedding-004"),
			Timeout:            getEnvAsDuration("GEMINI_TIMEOUT", 30*time.Second),
			MaxUploadMB:        getEnvAsInt("GEMINI_MAX_UPLOAD_MB", 50),
			AllowedMIMETypes:   getEnvAsCSV("GEMINI_ALLOWED_MIME", []string{"image/png", "image/jpeg", "video/mp4", "audio/wav", "audio/mpeg"}),
		},
		GenericHTTP: GenericHTTPConfig{
			ConfigDir: getEnv("GENERIC_PROVIDER_CONFIG_DIR", "internal/providers/config"),
		},
		ProviderManager: ProviderManagerConfig{
			DefaultStrategy:         getEnv("PROVIDER_STRATEGY", "first"),
			FailoverAttempts:        getEnvAsInt("PROVIDER_FAILOVER_ATTEMPTS", 3),
			CircuitBreakerThreshold: getEnvAsInt("PROVIDER_CB_THRESHOLD", 3),
			CircuitBreakerCooldown:  getEnvAsDuration("PROVIDER_CB_COOLDOWN", 30*time.Second),
			Cache: CacheConfig{
				RedisAddr:     getEnv("REDIS_ADDR", ""),
				RedisPassword: getEnv("REDIS_PASSWORD", ""),
				RedisDB:       getEnvAsInt("REDIS_DB", 0),
				TTL:           getEnvAsDuration("CACHE_TTL", 30*time.Second),
			},
		},
		Database: DatabaseConfig{
			URL: getEnv("DATABASE_URL", ""),
		},
		Storage: StorageConfig{
			UploadDir:     getEnv("UPLOAD_DIR", "storage"),
			PublicBaseURL: getEnv("STORAGE_PUBLIC_BASE_URL", ""),
			ServeFromAPI:  getEnvAsBool("STORAGE_SERVE_FROM_API", true),
		},
		Jobs: JobsConfig{
			WorkerInterval:      getEnvAsDuration("JOBS_WORKER_INTERVAL", 5*time.Second),
			CallbackMaxAttempts: getEnvAsInt("JOBS_CALLBACK_MAX_ATTEMPTS", 5),
			CallbackBackoff:     getEnvAsDuration("JOBS_CALLBACK_BACKOFF", 30*time.Second),
			UploadMaxMB:         getEnvAsInt("JOBS_UPLOAD_MAX_MB", 200),
		},
		Auth: AuthConfig{
			AccessSecret:  getEnv("AUTH_ACCESS_SECRET", ""),
			RefreshSecret: getEnv("AUTH_REFRESH_SECRET", ""),
			AccessTTL:     getEnvAsDuration("AUTH_ACCESS_TTL", 900*time.Second),
			RefreshTTL:    getEnvAsDuration("AUTH_REFRESH_TTL", 604800*time.Second),
			SessionPrefix: getEnv("AUTH_SESSION_PREFIX", "auth:session"),
			SessionRedis: RedisConnConfig{
				Addr:     getEnv("AUTH_SESSION_REDIS_ADDR", ""),
				Password: getEnv("AUTH_SESSION_REDIS_PASSWORD", ""),
				DB:       getEnvAsInt("AUTH_SESSION_REDIS_DB", 1),
			},
			RateLimit: RateLimitConfig{
				Window:      getEnvAsDuration("AUTH_RATELIMIT_WINDOW", 60*time.Second),
				MaxAttempts: getEnvAsInt("AUTH_RATELIMIT_MAX_ATTEMPTS", 5),
				Redis: RedisConnConfig{
					Addr:     getEnv("AUTH_RATELIMIT_REDIS_ADDR", ""),
					Password: getEnv("AUTH_RATELIMIT_REDIS_PASSWORD", ""),
					DB:       getEnvAsInt("AUTH_RATELIMIT_REDIS_DB", 2),
				},
			},
			Argon: Argon2Config{
				Memory:      getEnvAsUint32("AUTH_ARGON_MEMORY", 64*1024),
				Iterations:  getEnvAsUint32("AUTH_ARGON_ITERATIONS", 3),
				Parallelism: uint8(getEnvAsUint32("AUTH_ARGON_PARALLELISM", 2)),
				SaltLength:  getEnvAsUint32("AUTH_ARGON_SALT_LENGTH", 16),
				KeyLength:   getEnvAsUint32("AUTH_ARGON_KEY_LENGTH", 32),
			},
		},
		Security: SecurityConfig{
			ProviderSession: ProviderSessionSecurityConfig{
				PrimaryKeyID: getEnv("PROVIDER_SESSION_PRIMARY_KEY_ID", ""),
				Keys:         getEnvAsKeyRing("PROVIDER_SESSION_KEYS"),
			},
		},
		Logging: LoggingConfig{
			HTTPRequests:  getEnvAsBool("LOG_HTTP_REQUESTS", false),
			ProviderCalls: getEnvAsBool("LOG_PROVIDER_CALLS", false),
		},
	}
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}

func getEnvAsDuration(key string, fallback time.Duration) time.Duration {
	if raw, ok := os.LookupEnv(key); ok && raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil {
			log.Printf("invalid %s value %q, using default %s", key, raw, fallback)
			return fallback
		}
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

func getEnvAsInt(key string, fallback int) int {
	if raw, ok := os.LookupEnv(key); ok && raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			log.Printf("invalid %s value %q, using default %d", key, raw, fallback)
			return fallback
		}
		return value
	}
	return fallback
}

func getEnvAsBool(key string, fallback bool) bool {
	if raw, ok := os.LookupEnv(key); ok && raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			log.Printf("invalid %s value %q, using default %t", key, raw, fallback)
			return fallback
		}
		return value
	}
	return fallback
}

func getEnvAsCSV(key string, fallback []string) []string {
	raw, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return append([]string(nil), fallback...)
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			values = append(values, trimmed)
		}
	}
	if len(values) == 0 {
		return append([]string(nil), fallback...)
	}
	return values
}

func getEnvAsUint32(key string, fallback uint32) uint32 {
	if raw, ok := os.LookupEnv(key); ok && raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			log.Printf("invalid %s value %q, using default %d", key, raw, fallback)
			return fallback
		}
		if value < 0 {
			value = int(fallback)
		}
		return uint32(value)
	}
	return fallback
}

func getEnvAsKeyRing(key string) map[string]string {
	raw, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return map[string]string{}
	}
	entries := strings.Split(raw, ",")
	result := make(map[string]string)
	for _, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			log.Printf("invalid key ring entry for %s: %s", key, trimmed)
			continue
		}
		id := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if id == "" || val == "" {
			continue
		}
		result[id] = val
	}
	return result
}
