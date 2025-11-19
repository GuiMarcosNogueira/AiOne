package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/midia/aione/internal/providers"
	"github.com/midia/aione/internal/providers/dto"
)

var (
	// ErrNoProviders signals that no provider implementations were registered.
	ErrNoProviders = errors.New("no providers registered")
	// ErrCapabilityUnavailable indicates that no registered provider exposes the
	// requested modality.
	ErrCapabilityUnavailable = errors.New("capability unavailable")
	// ErrCircuitOpen indicates the provider circuit-breaker is open.
	ErrCircuitOpen = errors.New("provider circuit open")
	// ErrRateLimited indicates the provider hit its rate limit.
	ErrRateLimited = errors.New("provider rate limited")
	// ErrUnknownProvider indicates the requested provider name is not registered.
	ErrUnknownProvider = errors.New("unknown provider")
)

// Strategy defines how the manager should pick a provider when multiple can
// satisfy the requested capability.
type Strategy string

const (
	StrategyFirst Strategy = "first"
	StrategyFast  Strategy = "fast"
	StrategyCheap Strategy = "cheap"
	StrategyBest  Strategy = "bestOf"
)

// ParseStrategy normalizes untrusted values.
func ParseStrategy(raw string) Strategy {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(StrategyFast):
		return StrategyFast
	case string(StrategyCheap):
		return StrategyCheap
	case strings.ToLower(string(StrategyBest)):
		return StrategyBest
	default:
		return StrategyFirst
	}
}

type ctxKey string

const (
	strategyKey ctxKey = "provider-strategy"
	providerKey ctxKey = "provider-name"
)

// ContextWithStrategy stores the routing strategy in the request context.
func ContextWithStrategy(ctx context.Context, strategy Strategy) context.Context {
	if strategy == "" || strategy == StrategyFirst {
		return ctx
	}
	return context.WithValue(ctx, strategyKey, strategy)
}

// StrategyFromContext extracts the strategy stored in the context.
func StrategyFromContext(ctx context.Context) Strategy {
	if ctx == nil {
		return ""
	}
	if val, ok := ctx.Value(strategyKey).(Strategy); ok {
		return val
	}
	if val, ok := ctx.Value(strategyKey).(string); ok {
		return ParseStrategy(val)
	}
	return ""
}

// ContextWithProvider stores the provider override in the context.
func ContextWithProvider(ctx context.Context, provider string) context.Context {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return ctx
	}
	return context.WithValue(ctx, providerKey, provider)
}

// ProviderFromContext extracts the preferred provider from the context.
func ProviderFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if val, ok := ctx.Value(providerKey).(string); ok {
		return strings.TrimSpace(val)
	}
	return ""
}

// CircuitBreakerConfig configures the basic breaker used for failover.
type CircuitBreakerConfig struct {
	FailureThreshold int
	Cooldown         time.Duration
}

// Result wraps the provider identifier used to satisfy a request.
type Result[T any] struct {
	Provider string
	Data     T
}

type capabilityPredicate func(providers.Capabilities) bool

type providerState struct {
	impl    providers.Provider
	caps    providers.Capabilities
	limiter *rate.Limiter
	breaker *breakerState
}

type breakerState struct {
	mu        sync.Mutex
	failures  int
	openUntil time.Time
}

func newBreakerState() *breakerState {
	return &breakerState{}
}

func (b *breakerState) Allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if now.Before(b.openUntil) {
		return false
	}
	return true
}

func (b *breakerState) Report(success bool, now time.Time, cfg CircuitBreakerConfig) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if success {
		b.failures = 0
		return
	}
	b.failures++
	threshold := cfg.FailureThreshold
	if threshold <= 0 {
		threshold = 3
	}
	if b.failures >= threshold {
		cooldown := cfg.Cooldown
		if cooldown <= 0 {
			cooldown = 30 * time.Second
		}
		b.openUntil = now.Add(cooldown)
		b.failures = 0
	}
}

// Option customizes the manager at construction time.
type Option func(*Manager)

// WithDefaultStrategy overrides the manager default strategy.
func WithDefaultStrategy(strategy Strategy) Option {
	return func(m *Manager) {
		if strategy != "" {
			m.defaultStrategy = strategy
		}
	}
}

// WithFailoverAttempts limits the number of providers attempted before giving up.
func WithFailoverAttempts(attempts int) Option {
	return func(m *Manager) {
		if attempts > 0 {
			m.failoverAttempts = attempts
		}
	}
}

// WithCircuitBreaker overrides the breaker configuration.
func WithCircuitBreaker(cfg CircuitBreakerConfig) Option {
	return func(m *Manager) {
		if cfg.FailureThreshold > 0 {
			m.breakerCfg.FailureThreshold = cfg.FailureThreshold
		}
		if cfg.Cooldown > 0 {
			m.breakerCfg.Cooldown = cfg.Cooldown
		}
	}
}

// WithCache enables Redis-backed caching.
func WithCache(cache Cache, ttl time.Duration) Option {
	return func(m *Manager) {
		if cache == nil {
			return
		}
		if ttl <= 0 {
			ttl = 30 * time.Second
		}
		m.cache = &cacheLayer{store: cache, ttl: ttl}
	}
}

// Manager coordinates calls across the registered providers.
type Manager struct {
	mu               sync.RWMutex
	registry         map[string]*providerState
	order            []string
	defaultStrategy  Strategy
	failoverAttempts int
	breakerCfg       CircuitBreakerConfig
	cache            *cacheLayer
}

type cacheLayer struct {
	store Cache
	ttl   time.Duration
}

// NewManager instantiates a provider manager with the supplied providers.
func NewManager(list []providers.Provider, opts ...Option) *Manager {
	m := &Manager{
		registry:        make(map[string]*providerState),
		defaultStrategy: StrategyFirst,
		breakerCfg: CircuitBreakerConfig{
			FailureThreshold: 3,
			Cooldown:         30 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(m)
	}
	for _, provider := range list {
		m.Register(provider)
	}
	return m
}

// Register adds or updates a provider in the registry.
func (m *Manager) Register(provider providers.Provider) {
	if provider == nil {
		return
	}
	caps := provider.Capabilities()
	state := &providerState{
		impl:    provider,
		caps:    caps,
		breaker: newBreakerState(),
	}
	if rps := caps.Attributes.RateLimitRPS; rps > 0 {
		burst := int(math.Ceil(rps))
		if burst < 1 {
			burst = 1
		}
		state.limiter = rate.NewLimiter(rate.Limit(rps), burst)
	}

	name := provider.Name()
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.registry[name]; !exists {
		m.order = append(m.order, name)
	}
	m.registry[name] = state
}

// ListProviders exposes the names of all registered providers.
func (m *Manager) ListProviders() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := make([]string, 0, len(m.order))
	for _, name := range m.order {
		if _, ok := m.registry[name]; ok {
			list = append(list, name)
		}
	}
	return list
}

// CapabilityMatrixEntry exposes provider capabilities through the API.
type CapabilityMatrixEntry struct {
	Name         string                 `json:"name"`
	Capabilities providers.Capabilities `json:"capabilities"`
}

// CapabilityMatrix returns the provider capability matrix for observability.
func (m *Manager) CapabilityMatrix() []CapabilityMatrixEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	matrix := make([]CapabilityMatrixEntry, 0, len(m.order))
	for _, name := range m.order {
		if state, ok := m.registry[name]; ok {
			matrix = append(matrix, CapabilityMatrixEntry{Name: name, Capabilities: state.caps})
		}
	}
	return matrix
}

func (m *Manager) capabilityCandidates(predicate capabilityPredicate) []*providerState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var list []*providerState
	for _, name := range m.order {
		if state, ok := m.registry[name]; ok && predicate(state.caps) {
			list = append(list, state)
		}
	}
	return list
}

func (m *Manager) providerByName(name string) *providerState {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if state, ok := m.registry[name]; ok {
		return state
	}
	lower := strings.ToLower(name)
	for existing, state := range m.registry {
		if strings.ToLower(existing) == lower {
			return state
		}
	}
	return nil
}

func (m *Manager) sortCandidates(strategy Strategy, list []*providerState) {
	switch strategy {
	case StrategyFast:
		sort.SliceStable(list, func(i, j int) bool {
			return scoreOrDefault(list[i].caps.Attributes.LatencyScore, 5) < scoreOrDefault(list[j].caps.Attributes.LatencyScore, 5)
		})
	case StrategyCheap:
		sort.SliceStable(list, func(i, j int) bool {
			left := scoreOrDefault(list[i].caps.Attributes.CostScore, 5)
			right := scoreOrDefault(list[j].caps.Attributes.CostScore, 5)
			if left == right {
				return scoreOrDefault(list[i].caps.Attributes.QualityScore, 5) > scoreOrDefault(list[j].caps.Attributes.QualityScore, 5)
			}
			return left < right
		})
	case StrategyBest:
		sort.SliceStable(list, func(i, j int) bool {
			left := scoreOrDefault(list[i].caps.Attributes.QualityScore, 5)
			right := scoreOrDefault(list[j].caps.Attributes.QualityScore, 5)
			if left == right {
				return scoreOrDefault(list[i].caps.Attributes.CostScore, 5) < scoreOrDefault(list[j].caps.Attributes.CostScore, 5)
			}
			return left > right
		})
	}
}

func scoreOrDefault(score, fallback int) int {
	if score == 0 {
		return fallback
	}
	return score
}

func (m *Manager) cacheEnabled() bool {
	return m != nil && m.cache != nil && m.cache.store != nil && m.cache.ttl > 0
}

func getCached[T any](m *Manager, ctx context.Context, namespace string, req any) (Result[T], bool, error) {
	var result Result[T]
	if !m.cacheEnabled() {
		return result, false, nil
	}
	key, err := m.cacheKey(namespace, req)
	if err != nil {
		return result, false, err
	}
	data, err := m.cache.store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, ErrCacheMiss) {
			return result, false, nil
		}
		return result, false, err
	}
	var envelope cacheEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return result, false, err
	}
	if err := json.Unmarshal(envelope.Payload, &result.Data); err != nil {
		return result, false, err
	}
	result.Provider = envelope.Provider
	return result, true, nil
}

func storeCached[T any](m *Manager, ctx context.Context, namespace string, req any, result Result[T]) error {
	if !m.cacheEnabled() {
		return nil
	}
	key, err := m.cacheKey(namespace, req)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(result.Data)
	if err != nil {
		return err
	}
	envelope := cacheEnvelope{Provider: result.Provider, Payload: payload}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	return m.cache.store.Set(ctx, key, raw, m.cache.ttl)
}

func (m *Manager) cacheKey(namespace string, req any) (string, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return namespace + ":" + hex.EncodeToString(sum[:]), nil
}

type cacheEnvelope struct {
	Provider string          `json:"provider"`
	Payload  json.RawMessage `json:"payload"`
}

func execute[T any](m *Manager, ctx context.Context, namespace string, req any, cacheable bool, predicate capabilityPredicate, call func(context.Context, providers.Provider) (T, error)) (Result[T], error) {
	var zero Result[T]
	if m == nil {
		return zero, ErrNoProviders
	}
	preferred := ProviderFromContext(ctx)
	cacheAllowed := cacheable && preferred == ""

	var candidates []*providerState
	if preferred != "" {
		state := m.providerByName(preferred)
		if state == nil {
			if !m.hasProviders() {
				return zero, ErrNoProviders
			}
			return zero, fmt.Errorf("%w: %s", ErrUnknownProvider, preferred)
		}
		if !predicate(state.caps) {
			return zero, ErrCapabilityUnavailable
		}
		candidates = []*providerState{state}
	} else {
		candidates = m.capabilityCandidates(predicate)
		if len(candidates) == 0 {
			if !m.hasProviders() {
				return zero, ErrNoProviders
			}
			return zero, ErrCapabilityUnavailable
		}
		strategy := StrategyFromContext(ctx)
		if strategy == "" {
			strategy = m.defaultStrategy
		}
		m.sortCandidates(strategy, candidates)
	}

	attemptLimit := len(candidates)
	if m.failoverAttempts > 0 && m.failoverAttempts < attemptLimit {
		attemptLimit = m.failoverAttempts
	}

	if cacheAllowed {
		if cached, ok, err := getCached[T](m, ctx, namespace, req); err == nil && ok {
			return cached, nil
		}
	}

	var lastErr error
	for idx := 0; idx < attemptLimit; idx++ {
		state := candidates[idx]
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		now := time.Now()
		if !state.breaker.Allow(now) {
			lastErr = ErrCircuitOpen
			continue
		}
		if state.limiter != nil && !state.limiter.Allow() {
			lastErr = ErrRateLimited
			continue
		}
		response, err := call(ctx, state.impl)
		success := err == nil
		state.breaker.Report(success, time.Now(), m.breakerCfg)
		if err != nil {
			lastErr = err
			continue
		}
		result := Result[T]{Provider: state.impl.Name(), Data: response}
		if cacheAllowed {
			_ = storeCached(m, ctx, namespace, req, result)
		}
		return result, nil
	}

	if lastErr != nil {
		return zero, lastErr
	}
	return zero, ErrCapabilityUnavailable
}

// TextGenerate proxies a text generation request to a capable provider.
func (m *Manager) TextGenerate(ctx context.Context, req dto.TextReq) (Result[dto.TextResp], error) {
	return execute(m, ctx, "text", req, true, func(c providers.Capabilities) bool { return c.TextGeneration }, func(ctx context.Context, provider providers.Provider) (dto.TextResp, error) {
		return provider.TextGenerate(ctx, req)
	})
}

// ImageGenerate proxies an image generation request to a capable provider.
func (m *Manager) ImageGenerate(ctx context.Context, req dto.ImageReq) (Result[dto.ImageResp], error) {
	return execute(m, ctx, "image", req, true, func(c providers.Capabilities) bool { return c.ImageGeneration }, func(ctx context.Context, provider providers.Provider) (dto.ImageResp, error) {
		return provider.ImageGenerate(ctx, req)
	})
}

// VideoGenerate proxies a video generation request to a capable provider.
func (m *Manager) VideoGenerate(ctx context.Context, req dto.VideoReq) (Result[dto.VideoResp], error) {
	return execute(m, ctx, "video", req, false, func(c providers.Capabilities) bool { return c.VideoGeneration }, func(ctx context.Context, provider providers.Provider) (dto.VideoResp, error) {
		return provider.VideoGenerate(ctx, req)
	})
}

// SpeechToText proxies an STT request to a capable provider.
func (m *Manager) SpeechToText(ctx context.Context, req dto.STTReq) (Result[dto.STTResp], error) {
	return execute(m, ctx, "stt", req, false, func(c providers.Capabilities) bool { return c.SpeechToText }, func(ctx context.Context, provider providers.Provider) (dto.STTResp, error) {
		return provider.SpeechToText(ctx, req)
	})
}

// TextToSpeech proxies a TTS request to a capable provider.
func (m *Manager) TextToSpeech(ctx context.Context, req dto.TTSReq) (Result[dto.TTSResp], error) {
	return execute(m, ctx, "tts", req, false, func(c providers.Capabilities) bool { return c.TextToSpeech }, func(ctx context.Context, provider providers.Provider) (dto.TTSResp, error) {
		return provider.TextToSpeech(ctx, req)
	})
}

// Embeddings proxies an embeddings request to a capable provider.
func (m *Manager) Embeddings(ctx context.Context, req dto.EmbeddingsReq) (Result[dto.EmbeddingsResp], error) {
	return execute(m, ctx, "embeddings", req, true, func(c providers.Capabilities) bool { return c.Embeddings }, func(ctx context.Context, provider providers.Provider) (dto.EmbeddingsResp, error) {
		return provider.Embeddings(ctx, req)
	})
}

// Moderation proxies a moderation request to a capable provider.
func (m *Manager) Moderation(ctx context.Context, req dto.ModerationReq) (Result[dto.ModerationResp], error) {
	return execute(m, ctx, "moderation", req, true, func(c providers.Capabilities) bool { return c.Moderation }, func(ctx context.Context, provider providers.Provider) (dto.ModerationResp, error) {
		return provider.Moderation(ctx, req)
	})
}

func (m *Manager) hasProviders() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.registry) > 0
}
