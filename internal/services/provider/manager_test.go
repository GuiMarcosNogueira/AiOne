package provider

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/midia/aione/internal/providers"
	"github.com/midia/aione/internal/providers/dto"
)

func TestStrategyFastChoosesLowestLatency(t *testing.T) {
	fast := newStubProvider("fast", capability(5, 1, 6, 0), successText("fast"))
	slow := newStubProvider("slow", capability(3, 8, 6, 0), successText("slow"))
	mgr := NewManager([]providers.Provider{slow, fast})
	ctx := ContextWithStrategy(context.Background(), StrategyFast)
	res, err := mgr.TextGenerate(ctx, dto.TextReq{Prompt: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Provider != "fast" {
		t.Fatalf("expected fast provider, got %s", res.Provider)
	}
}

func TestParseStrategyAndContextHelpers(t *testing.T) {
	if got := ParseStrategy("FAST"); got != StrategyFast {
		t.Fatalf("expected fast strategy, got %s", got)
	}
	if got := ParseStrategy("unknown"); got != StrategyFirst {
		t.Fatalf("expected default strategy, got %s", got)
	}
	ctx := ContextWithStrategy(context.Background(), StrategyCheap)
	if StrategyFromContext(ctx) != StrategyCheap {
		t.Fatalf("expected strategy from context")
	}
	ctx = ContextWithStrategy(context.Background(), "")
	if StrategyFromContext(ctx) != "" {
		t.Fatalf("expected empty strategy when not set")
	}
}

func TestStrategyCheapChoosesLowerCost(t *testing.T) {
	expensive := newStubProvider("expensive", capability(9, 5, 8, 0), successText("expensive"))
	cheap := newStubProvider("cheap", capability(2, 6, 7, 0), successText("cheap"))
	mgr := NewManager([]providers.Provider{expensive, cheap})
	ctx := ContextWithStrategy(context.Background(), StrategyCheap)
	res, err := mgr.TextGenerate(ctx, dto.TextReq{Prompt: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Provider != "cheap" {
		t.Fatalf("expected cheap provider, got %s", res.Provider)
	}
}

func TestStrategyBestPrefersQuality(t *testing.T) {
	good := newStubProvider("good", capability(5, 5, 9, 0), successText("good"))
	ok := newStubProvider("ok", capability(3, 4, 6, 0), successText("ok"))
	mgr := NewManager([]providers.Provider{ok, good})
	ctx := ContextWithStrategy(context.Background(), StrategyBest)
	res, err := mgr.TextGenerate(ctx, dto.TextReq{Prompt: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Provider != "good" {
		t.Fatalf("expected best quality provider, got %s", res.Provider)
	}
}

func TestFailoverAndCircuitBreaker(t *testing.T) {
	var failingCalls int
	failing := newStubProvider("failing", capability(5, 5, 5, 0), func(ctx context.Context, req dto.TextReq) (dto.TextResp, error) {
		failingCalls++
		return dto.TextResp{}, errors.New("boom")
	})
	backup := newStubProvider("backup", capability(5, 5, 5, 0), successText("backup"))
	mgr := NewManager([]providers.Provider{failing, backup}, WithCircuitBreaker(CircuitBreakerConfig{FailureThreshold: 1, Cooldown: time.Hour}))
	for i := 0; i < 2; i++ {
		res, err := mgr.TextGenerate(context.Background(), dto.TextReq{Prompt: "hi"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Provider != "backup" {
			t.Fatalf("expected failover provider, got %s", res.Provider)
		}
	}
	if failingCalls != 1 {
		t.Fatalf("expected breaker to open after first failure, got %d calls", failingCalls)
	}
}

func TestContextWithProviderForcesSelection(t *testing.T) {
	primary := newStubProvider("primary", capability(5, 5, 5, 0), successText("primary"))
	secondary := newStubProvider("secondary", capability(5, 5, 5, 0), successText("secondary"))
	mgr := NewManager([]providers.Provider{primary, secondary})
	ctx := ContextWithProvider(context.Background(), "secondary")
	res, err := mgr.TextGenerate(ctx, dto.TextReq{Prompt: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Provider != "secondary" {
		t.Fatalf("expected secondary provider, got %s", res.Provider)
	}
}

func TestContextWithProviderUnknown(t *testing.T) {
	primary := newStubProvider("primary", capability(5, 5, 5, 0), successText("primary"))
	mgr := NewManager([]providers.Provider{primary})
	ctx := ContextWithProvider(context.Background(), "missing")
	if _, err := mgr.TextGenerate(ctx, dto.TextReq{Prompt: "hi"}); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("expected unknown provider error, got %v", err)
	}
}

func TestWithDefaultStrategyInfluencesOrdering(t *testing.T) {
	slow := newStubProvider("slow", capability(3, 9, 5, 0), successText("slow"))
	fast := newStubProvider("fast", capability(3, 1, 5, 0), successText("fast"))
	mgr := NewManager([]providers.Provider{slow, fast}, WithDefaultStrategy(StrategyFast))
	res, err := mgr.TextGenerate(context.Background(), dto.TextReq{Prompt: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Provider != "fast" {
		t.Fatalf("expected fast provider due to default strategy, got %s", res.Provider)
	}
}

func TestWithFailoverAttemptsStopsEarly(t *testing.T) {
	failing := newStubProvider("fail", capability(5, 5, 5, 0), func(ctx context.Context, req dto.TextReq) (dto.TextResp, error) {
		return dto.TextResp{}, errors.New("boom")
	})
	success := newStubProvider("success", capability(5, 5, 5, 0), successText("success"))
	mgr := NewManager([]providers.Provider{failing, success}, WithFailoverAttempts(1))
	if _, err := mgr.TextGenerate(context.Background(), dto.TextReq{Prompt: "hi"}); err == nil || err.Error() != "boom" {
		t.Fatalf("expected failover limit to propagate first error, got %v", err)
	}
}

func TestRateLimitTriggersFailover(t *testing.T) {
	var primaryCalls, backupCalls int
	limited := newStubProvider("limited", capability(5, 5, 5, 1), func(ctx context.Context, req dto.TextReq) (dto.TextResp, error) {
		primaryCalls++
		return dto.TextResp{Content: "limited"}, nil
	})
	backup := newStubProvider("backup", capability(5, 5, 5, 10), func(ctx context.Context, req dto.TextReq) (dto.TextResp, error) {
		backupCalls++
		return dto.TextResp{Content: "backup"}, nil
	})
	mgr := NewManager([]providers.Provider{limited, backup})
	if _, err := mgr.TextGenerate(context.Background(), dto.TextReq{Prompt: "hi"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	res, err := mgr.TextGenerate(context.Background(), dto.TextReq{Prompt: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Provider != "backup" {
		t.Fatalf("expected backup provider after rate limit, got %s", res.Provider)
	}
	if backupCalls == 0 {
		t.Fatal("expected backup provider to run")
	}
	if primaryCalls != 1 {
		t.Fatalf("expected primary provider to run once, got %d", primaryCalls)
	}
}

func TestListProvidersReturnsOrder(t *testing.T) {
	one := newStubProvider("one", capability(5, 5, 5, 0), successText("one"))
	two := newStubProvider("two", capability(5, 5, 5, 0), successText("two"))
	mgr := NewManager([]providers.Provider{one, two})
	list := mgr.ListProviders()
	if len(list) != 2 || list[0] != "one" || list[1] != "two" {
		t.Fatalf("unexpected provider order: %+v", list)
	}
}

func TestCapabilityUnavailableWhenNoMatch(t *testing.T) {
	mgr := NewManager([]providers.Provider{newStubProvider("text-only", capability(5, 5, 5, 0), successText("ok"))})
	if _, err := mgr.ImageGenerate(context.Background(), dto.ImageReq{Prompt: "paint"}); !errors.Is(err, ErrCapabilityUnavailable) {
		t.Fatalf("expected capability unavailable, got %v", err)
	}
}

func TestOtherModalitiesRoute(t *testing.T) {
	provider := newFullProvider("full")
	mgr := NewManager([]providers.Provider{provider})
	if _, err := mgr.ImageGenerate(context.Background(), dto.ImageReq{Prompt: "img"}); err != nil {
		t.Fatalf("ImageGenerate failed: %v", err)
	}
	if _, err := mgr.VideoGenerate(context.Background(), dto.VideoReq{Prompt: "vid"}); err != nil {
		t.Fatalf("VideoGenerate failed: %v", err)
	}
	if _, err := mgr.SpeechToText(context.Background(), dto.STTReq{AudioURL: "http://"}); err != nil {
		t.Fatalf("STT failed: %v", err)
	}
	if _, err := mgr.TextToSpeech(context.Background(), dto.TTSReq{Text: "hi"}); err != nil {
		t.Fatalf("TTS failed: %v", err)
	}
	if _, err := mgr.Embeddings(context.Background(), dto.EmbeddingsReq{Inputs: []string{"a"}}); err != nil {
		t.Fatalf("Embeddings failed: %v", err)
	}
	if _, err := mgr.Moderation(context.Background(), dto.ModerationReq{Input: "text"}); err != nil {
		t.Fatalf("Moderation failed: %v", err)
	}
}

func TestCacheShortCircuitsProvider(t *testing.T) {
	cache := newMemoryCache()
	calls := 0
	provider := newStubProvider("cached", capability(5, 5, 5, 0), func(ctx context.Context, req dto.TextReq) (dto.TextResp, error) {
		calls++
		return dto.TextResp{Content: "cached"}, nil
	})
	mgr := NewManager([]providers.Provider{provider}, WithCache(cache, time.Minute))
	for i := 0; i < 2; i++ {
		res, err := mgr.TextGenerate(context.Background(), dto.TextReq{Prompt: "hi"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Provider != "cached" {
			t.Fatalf("unexpected provider %s", res.Provider)
		}
	}
	if calls != 1 {
		t.Fatalf("expected cache hit on second call, got %d invocations", calls)
	}
}

func TestCapabilityMatrixIncludesProviders(t *testing.T) {
	one := newStubProvider("one", capability(5, 5, 5, 0), successText("one"))
	two := newStubProvider("two", capability(5, 5, 5, 0), successText("two"))
	mgr := NewManager([]providers.Provider{one, two})
	matrix := mgr.CapabilityMatrix()
	if len(matrix) != 2 {
		t.Fatalf("expected 2 providers in matrix, got %d", len(matrix))
	}
	if matrix[0].Name == matrix[1].Name {
		t.Fatal("expected unique provider names in matrix")
	}
}

type stubProvider struct {
	name       string
	caps       providers.Capabilities
	textFn     func(context.Context, dto.TextReq) (dto.TextResp, error)
	imageFn    func(context.Context, dto.ImageReq) (dto.ImageResp, error)
	videoFn    func(context.Context, dto.VideoReq) (dto.VideoResp, error)
	sttFn      func(context.Context, dto.STTReq) (dto.STTResp, error)
	ttsFn      func(context.Context, dto.TTSReq) (dto.TTSResp, error)
	embedFn    func(context.Context, dto.EmbeddingsReq) (dto.EmbeddingsResp, error)
	moderateFn func(context.Context, dto.ModerationReq) (dto.ModerationResp, error)
}

func newStubProvider(name string, caps providers.Capabilities, fn func(context.Context, dto.TextReq) (dto.TextResp, error)) *stubProvider {
	return &stubProvider{name: name, caps: caps, textFn: fn}
}

func newFullProvider(name string) *stubProvider {
	caps := providers.Capabilities{
		TextGeneration:  true,
		ImageGeneration: true,
		VideoGeneration: true,
		SpeechToText:    true,
		TextToSpeech:    true,
		Embeddings:      true,
		Moderation:      true,
	}
	return &stubProvider{
		name: name,
		caps: caps,
		textFn: func(ctx context.Context, req dto.TextReq) (dto.TextResp, error) {
			return dto.TextResp{Content: "text"}, nil
		},
		imageFn: func(ctx context.Context, req dto.ImageReq) (dto.ImageResp, error) {
			return dto.ImageResp{URL: "img"}, nil
		},
		videoFn: func(ctx context.Context, req dto.VideoReq) (dto.VideoResp, error) {
			return dto.VideoResp{URL: "video"}, nil
		},
		sttFn: func(ctx context.Context, req dto.STTReq) (dto.STTResp, error) {
			return dto.STTResp{Transcript: "stt"}, nil
		},
		ttsFn: func(ctx context.Context, req dto.TTSReq) (dto.TTSResp, error) {
			return dto.TTSResp{AudioURL: "tts"}, nil
		},
		embedFn: func(ctx context.Context, req dto.EmbeddingsReq) (dto.EmbeddingsResp, error) {
			return dto.EmbeddingsResp{Vectors: [][]float32{{1}}}, nil
		},
		moderateFn: func(ctx context.Context, req dto.ModerationReq) (dto.ModerationResp, error) {
			return dto.ModerationResp{Flagged: false}, nil
		},
	}
}

func successText(content string) func(context.Context, dto.TextReq) (dto.TextResp, error) {
	return func(ctx context.Context, req dto.TextReq) (dto.TextResp, error) {
		return dto.TextResp{Content: content}, nil
	}
}

func capability(cost, latency, quality int, rps float64) providers.Capabilities {
	return providers.Capabilities{
		TextGeneration: true,
		Attributes: providers.CapabilityAttributes{
			CostScore:    cost,
			LatencyScore: latency,
			QualityScore: quality,
			RateLimitRPS: rps,
		},
	}
}

func (s *stubProvider) Name() string { return s.name }

func (s *stubProvider) Capabilities() providers.Capabilities { return s.caps }

func (s *stubProvider) Health(ctx context.Context) error { return nil }

func (s *stubProvider) TextGenerate(ctx context.Context, req dto.TextReq) (dto.TextResp, error) {
	if s.textFn != nil {
		return s.textFn(ctx, req)
	}
	return dto.TextResp{Content: s.name}, nil
}

func (s *stubProvider) ImageGenerate(ctx context.Context, req dto.ImageReq) (dto.ImageResp, error) {
	if s.imageFn != nil {
		return s.imageFn(ctx, req)
	}
	return dto.ImageResp{}, errors.New("not implemented")
}

func (s *stubProvider) VideoGenerate(ctx context.Context, req dto.VideoReq) (dto.VideoResp, error) {
	if s.videoFn != nil {
		return s.videoFn(ctx, req)
	}
	return dto.VideoResp{}, errors.New("not implemented")
}

func (s *stubProvider) SpeechToText(ctx context.Context, req dto.STTReq) (dto.STTResp, error) {
	if s.sttFn != nil {
		return s.sttFn(ctx, req)
	}
	return dto.STTResp{}, errors.New("not implemented")
}

func (s *stubProvider) TextToSpeech(ctx context.Context, req dto.TTSReq) (dto.TTSResp, error) {
	if s.ttsFn != nil {
		return s.ttsFn(ctx, req)
	}
	return dto.TTSResp{}, errors.New("not implemented")
}

func (s *stubProvider) Embeddings(ctx context.Context, req dto.EmbeddingsReq) (dto.EmbeddingsResp, error) {
	if s.embedFn != nil {
		return s.embedFn(ctx, req)
	}
	return dto.EmbeddingsResp{}, errors.New("not implemented")
}

func (s *stubProvider) Moderation(ctx context.Context, req dto.ModerationReq) (dto.ModerationResp, error) {
	if s.moderateFn != nil {
		return s.moderateFn(ctx, req)
	}
	return dto.ModerationResp{}, errors.New("not implemented")
}

type memoryCache struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemoryCache() *memoryCache {
	return &memoryCache{data: make(map[string][]byte)}
}

func (m *memoryCache) Get(ctx context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	val, ok := m.data[key]
	if !ok {
		return nil, ErrCacheMiss
	}
	return append([]byte(nil), val...), nil
}

func (m *memoryCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	buf := make([]byte, len(value))
	copy(buf, value)
	m.data[key] = buf
	return nil
}
