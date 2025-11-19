package generichttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/midia/aione/internal/providers"
)

// FileConfig represents the on-disk configuration for a generic HTTP provider.
type FileConfig struct {
	Name           string            `json:"name" yaml:"name"`
	BaseURL        string            `json:"base_url" yaml:"base_url"`
	Headers        map[string]string `json:"headers" yaml:"headers"`
	Auth           AuthConfig        `json:"auth" yaml:"auth"`
	TimeoutSeconds int               `json:"timeout_seconds" yaml:"timeout_seconds"`
	Capabilities   CapabilityConfig  `json:"capabilities" yaml:"capabilities"`
	Endpoints      EndpointSet       `json:"endpoints" yaml:"endpoints"`
}

// CapabilityConfig lets configs override the derived capability metadata.
type CapabilityConfig struct {
	TextGeneration  *bool                          `json:"text_generation" yaml:"text_generation"`
	ImageGeneration *bool                          `json:"image_generation" yaml:"image_generation"`
	VideoGeneration *bool                          `json:"video_generation" yaml:"video_generation"`
	SpeechToText    *bool                          `json:"speech_to_text" yaml:"speech_to_text"`
	TextToSpeech    *bool                          `json:"text_to_speech" yaml:"text_to_speech"`
	Embeddings      *bool                          `json:"embeddings" yaml:"embeddings"`
	Moderation      *bool                          `json:"moderation" yaml:"moderation"`
	Limits          providers.Limits               `json:"limits" yaml:"limits"`
	Attributes      providers.CapabilityAttributes `json:"attributes" yaml:"attributes"`
}

// EndpointSet enumerates supported modalities for configuration convenience.
type EndpointSet struct {
	Text       *EndpointConfig `json:"text" yaml:"text"`
	Image      *EndpointConfig `json:"image" yaml:"image"`
	Video      *EndpointConfig `json:"video" yaml:"video"`
	STT        *EndpointConfig `json:"stt" yaml:"stt"`
	TTS        *EndpointConfig `json:"tts" yaml:"tts"`
	Embeddings *EndpointConfig `json:"embeddings" yaml:"embeddings"`
	Moderation *EndpointConfig `json:"moderation" yaml:"moderation"`
}

// EndpointConfig declares how the provider should call a specific upstream route.
type EndpointConfig struct {
	Enabled         *bool             `json:"enabled" yaml:"enabled"`
	Method          string            `json:"method" yaml:"method"`
	Path            string            `json:"path" yaml:"path"`
	Query           map[string]string `json:"query" yaml:"query"`
	Headers         map[string]string `json:"headers" yaml:"headers"`
	SuccessStatuses []int             `json:"success_statuses" yaml:"success_statuses"`
	TimeoutSeconds  int               `json:"timeout_seconds" yaml:"timeout_seconds"`
	Request         RequestMapping    `json:"request" yaml:"request"`
	Response        ResponseMapping   `json:"response" yaml:"response"`
}

// RequestMapping configures how request bodies are built.
type RequestMapping struct {
	ContentType  string `json:"content_type" yaml:"content_type"`
	BodyTemplate string `json:"body" yaml:"body"`
}

// ResponseMapping specifies how to project upstream JSON into DTOs.
type ResponseMapping struct {
	ProviderPath   string `json:"provider_path" yaml:"provider_path"`
	TextPath       string `json:"text_path" yaml:"text_path"`
	URLPath        string `json:"url_path" yaml:"url_path"`
	TranscriptPath string `json:"transcript_path" yaml:"transcript_path"`
	AudioURLPath   string `json:"audio_url_path" yaml:"audio_url_path"`
	EmbeddingsPath string `json:"embeddings_path" yaml:"embeddings_path"`
	FlaggedPath    string `json:"flagged_path" yaml:"flagged_path"`
	ReasonPath     string `json:"reason_path" yaml:"reason_path"`
}

// AuthConfig supports common API authentication schemes.
type AuthConfig struct {
	Type            string `json:"type" yaml:"type"`
	In              string `json:"in" yaml:"in"`
	Name            string `json:"name" yaml:"name"`
	Value           string `json:"value" yaml:"value"`
	ValueFromEnv    string `json:"value_from_env" yaml:"value_from_env"`
	Prefix          string `json:"prefix" yaml:"prefix"`
	Username        string `json:"username" yaml:"username"`
	UsernameFromEnv string `json:"username_from_env" yaml:"username_from_env"`
	Password        string `json:"password" yaml:"password"`
	PasswordFromEnv string `json:"password_from_env" yaml:"password_from_env"`
}

// LoadFromDir parses every JSON/YAML file in dir and returns the configured providers.
func LoadFromDir(dir string, opts ...Option) ([]providers.Provider, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read generic provider dir: %w", err)
	}
	var list []providers.Provider
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" && ext != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read config %s: %w", entry.Name(), err)
		}
		cfg, err := parseConfig(raw, ext)
		if err != nil {
			return nil, fmt.Errorf("parse config %s: %w", entry.Name(), err)
		}
		prov, err := NewFromConfig(cfg, opts...)
		if err != nil {
			return nil, fmt.Errorf("init provider %s: %w", cfg.Name, err)
		}
		list = append(list, prov)
	}
	return list, nil
}

func parseConfig(raw []byte, ext string) (FileConfig, error) {
	var cfg FileConfig
	switch ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return FileConfig{}, err
		}
	case ".json":
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return FileConfig{}, err
		}
	default:
		return FileConfig{}, fmt.Errorf("unsupported extension %s", ext)
	}
	return cfg, nil
}

func (cfg FileConfig) validate() error {
	if strings.TrimSpace(cfg.Name) == "" {
		return errors.New("name is required")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return errors.New("base_url is required")
	}
	if cfg.Endpoints == (EndpointSet{}) {
		return errors.New("at least one endpoint must be configured")
	}
	return nil
}

func (cfg FileConfig) deriveCapabilities() providers.Capabilities {
	caps := providers.Capabilities{}
	if cfg.Endpoints.Text != nil && cfg.Endpoints.Text.enabled() {
		caps.TextGeneration = true
	}
	if cfg.Endpoints.Image != nil && cfg.Endpoints.Image.enabled() {
		caps.ImageGeneration = true
	}
	if cfg.Endpoints.Video != nil && cfg.Endpoints.Video.enabled() {
		caps.VideoGeneration = true
	}
	if cfg.Endpoints.STT != nil && cfg.Endpoints.STT.enabled() {
		caps.SpeechToText = true
	}
	if cfg.Endpoints.TTS != nil && cfg.Endpoints.TTS.enabled() {
		caps.TextToSpeech = true
	}
	if cfg.Endpoints.Embeddings != nil && cfg.Endpoints.Embeddings.enabled() {
		caps.Embeddings = true
	}
	if cfg.Endpoints.Moderation != nil && cfg.Endpoints.Moderation.enabled() {
		caps.Moderation = true
	}
	overrideBool := func(ptr *bool, current bool) bool {
		if ptr == nil {
			return current
		}
		return *ptr
	}
	caps.TextGeneration = overrideBool(cfg.Capabilities.TextGeneration, caps.TextGeneration)
	caps.ImageGeneration = overrideBool(cfg.Capabilities.ImageGeneration, caps.ImageGeneration)
	caps.VideoGeneration = overrideBool(cfg.Capabilities.VideoGeneration, caps.VideoGeneration)
	caps.SpeechToText = overrideBool(cfg.Capabilities.SpeechToText, caps.SpeechToText)
	caps.TextToSpeech = overrideBool(cfg.Capabilities.TextToSpeech, caps.TextToSpeech)
	caps.Embeddings = overrideBool(cfg.Capabilities.Embeddings, caps.Embeddings)
	caps.Moderation = overrideBool(cfg.Capabilities.Moderation, caps.Moderation)
	caps.Limits = cfg.Capabilities.Limits
	caps.Attributes = cfg.Capabilities.Attributes
	return caps
}

func (cfg *EndpointConfig) enabled() bool {
	if cfg == nil {
		return false
	}
	if cfg.Enabled == nil {
		return true
	}
	return *cfg.Enabled
}
