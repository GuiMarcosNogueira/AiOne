package jobs

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/midia/aione/internal/providers"
	"github.com/midia/aione/internal/providers/dto"
	providermanager "github.com/midia/aione/internal/services/provider"
)

func TestProcessorRunVideoJobUsesProvider(t *testing.T) {
	provider := &fakeProvider{name: "stub", videoURL: "https://video.example"}
	mgr := providermanager.NewManager([]providers.Provider{provider})
	proc := NewProcessor(NewMemoryRepository(), mgr, testLogger(), time.Millisecond, Options{})
	job := &Job{ID: "video", Type: TypeVideo, Prompt: "make a trailer"}
	result, err := proc.runVideoJob(context.Background(), job)
	if err != nil {
		t.Fatalf("runVideoJob failed: %v", err)
	}
	if got := result["video_url"]; got != provider.videoURL {
		t.Fatalf("unexpected video url: %v", got)
	}
	if got := result["provider"]; got != provider.name {
		t.Fatalf("unexpected provider: %v", got)
	}
	if provider.lastVideoReq.Prompt != job.Prompt {
		t.Fatalf("expected prompt to be forwarded")
	}
}

func TestProcessorRunVideoJobFallback(t *testing.T) {
	proc := &Processor{}
	job := &Job{ID: "video", Type: TypeVideo}
	result, err := proc.runVideoJob(context.Background(), job)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result["message"] != "providers unavailable" {
		t.Fatalf("unexpected fallback result: %v", result)
	}
}

func TestProcessorHandleCallbackResultSuccess(t *testing.T) {
	repo := &recordingRepo{}
	proc := &Processor{repo: repo, log: testLogger(), callbackCfg: Options{CallbackBackoff: time.Second}}
	job := Job{ID: "job", MaxCallbackAttempts: 3}
	proc.handleCallbackResult(context.Background(), job, nil, 204)
	if len(repo.updated) != 1 {
		t.Fatalf("expected one update, got %d", len(repo.updated))
	}
	updated := repo.updated[0]
	if !updated.NextCallbackAttempt.IsZero() {
		t.Fatalf("success callbacks should not schedule retries")
	}
	if updated.CallbackAttempts != 1 {
		t.Fatalf("expected attempts increment, got %d", updated.CallbackAttempts)
	}
}

func TestProcessorHandleCallbackResultSchedulesRetry(t *testing.T) {
	repo := &recordingRepo{}
	proc := &Processor{repo: repo, log: testLogger(), callbackCfg: Options{CallbackBackoff: time.Second}}
	job := Job{ID: "job", MaxCallbackAttempts: 3}
	proc.handleCallbackResult(context.Background(), job, errors.New("timeout"), 0)
	updated := repo.updated[0]
	if !updated.NextCallbackAttempt.After(time.Now().Add(-500 * time.Millisecond)) {
		t.Fatalf("expected next callback to be scheduled in the future")
	}
	if updated.CallbackAttempts != 1 {
		t.Fatalf("expected attempts increment, got %d", updated.CallbackAttempts)
	}
	if updated.LastCallbackResponse == "" {
		t.Fatalf("expected last callback response to capture error")
	}
}

func TestProcessorHandleCallbackResultStopsAfterMax(t *testing.T) {
	repo := &recordingRepo{}
	proc := &Processor{repo: repo, log: testLogger(), callbackCfg: Options{CallbackBackoff: time.Second}}
	job := Job{ID: "job", MaxCallbackAttempts: 1, CallbackAttempts: 1}
	proc.handleCallbackResult(context.Background(), job, errors.New("timeout"), 0)
	updated := repo.updated[0]
	if !updated.NextCallbackAttempt.IsZero() {
		t.Fatalf("expected callbacks to stop after reaching max attempts")
	}
}

func TestProcessorProcessJobUnsupportedType(t *testing.T) {
	repo := &recordingRepo{}
	proc := &Processor{repo: repo, log: testLogger(), callbackCfg: Options{CallbackBackoff: time.Millisecond}}
	job := &Job{ID: "job", Type: TypeGeneric}
	proc.processJob(context.Background(), job)
	if len(repo.updated) != 1 {
		t.Fatalf("expected update to be called")
	}
	updated := repo.updated[0]
	if updated.Status != StatusFailed {
		t.Fatalf("expected failure status, got %s", updated.Status)
	}
	if updated.ErrorMessage == "" {
		t.Fatalf("expected error message to be recorded")
	}
	if updated.NextCallbackAttempt.IsZero() {
		t.Fatalf("expected next callback attempt to be scheduled")
	}
}

func TestProcessorRunProcessorProcessesJobs(t *testing.T) {
	repo := &recordingRepo{}
	job := &Job{ID: "video", Type: TypeVideo, Prompt: "prompt"}
	repo.claimQueue = []*Job{job}
	provider := &fakeProvider{name: "stub", videoURL: "https://video"}
	mgr := providermanager.NewManager([]providers.Provider{provider})
	proc := NewProcessor(repo, mgr, testLogger(), time.Millisecond, Options{CallbackBackoff: time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go proc.runProcessor(ctx)
	if !waitForCondition(2*time.Second, func() bool { return len(repo.updated) > 0 }) {
		t.Fatalf("expected job to be processed")
	}
	updated := repo.updated[0]
	if updated.Status != StatusCompleted {
		t.Fatalf("expected completed status, got %s", updated.Status)
	}
}

func TestProcessorRunCallbacksDispatchesJobs(t *testing.T) {
	repo := &recordingRepo{}
	job := Job{ID: "job", Status: StatusCompleted, CallbackURL: "", MaxCallbackAttempts: 3}
	repo.callbackJobs = []Job{job}
	callbackCh := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		data, _ := io.ReadAll(r.Body)
		callbackCh <- map[string]any{"body": string(data)}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	repo.callbackJobs[0].CallbackURL = server.URL
	proc := NewProcessor(repo, nil, testLogger(), time.Millisecond, Options{CallbackBackoff: time.Millisecond, CallbackMaxAttempts: 3})
	proc.httpClient = server.Client()
	ctx, cancel := context.WithCancel(context.Background())
	go proc.runCallbacks(ctx)
	if !waitForCondition(2*time.Second, func() bool { return len(repo.updated) > 0 }) {
		cancel()
		t.Fatalf("expected callback to be dispatched")
	}
	select {
	case <-callbackCh:
	default:
		cancel()
		t.Fatalf("expected callback server to receive payload")
	}
	cancel()
	updated := repo.updated[0]
	if updated.LastCallbackResponse == "" {
		t.Fatalf("expected last callback response to be recorded")
	}
}

func TestProcessorDispatchCallbackHandlesError(t *testing.T) {
	repo := &recordingRepo{}
	proc := &Processor{repo: repo, log: testLogger(), httpClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("boom")
	})}, callbackCfg: Options{CallbackBackoff: time.Millisecond, CallbackMaxAttempts: 2}}
	job := Job{ID: "job", Status: StatusCompleted, CallbackURL: "http://callback", MaxCallbackAttempts: 2}
	proc.dispatchCallback(context.Background(), job)
	if len(repo.updated) != 1 {
		t.Fatalf("expected update on callback failure")
	}
	updated := repo.updated[0]
	if updated.NextCallbackAttempt.IsZero() {
		t.Fatalf("expected retry to be scheduled")
	}
}

func TestMarshalBodyEncodesPayload(t *testing.T) {
	reader := marshalBody(map[string]any{"id": "job", "status": "ok"})
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read marshal body: %v", err)
	}
	if !strings.Contains(string(data), "\"id\":\"job\"") {
		t.Fatalf("expected json payload, got %s", data)
	}
}

func TestJobClone(t *testing.T) {
	job := Job{ID: "job", Status: StatusCompleted, Result: map[string]any{"url": "video"}}
	copy := job.Clone()
	if copy.ID != job.ID || copy.Status != job.Status {
		t.Fatalf("expected clone to match original")
	}
	copy.ID = "other"
	if job.ID == copy.ID {
		t.Fatalf("expected clone to be independent")
	}
}

type recordingRepo struct {
	updated      []Job
	claimQueue   []*Job
	callbackJobs []Job
}

func (r *recordingRepo) Create(ctx context.Context, job Job) (Job, error) { return job, nil }
func (r *recordingRepo) Get(ctx context.Context, id string) (Job, error) {
	return Job{}, ErrJobNotFound
}
func (r *recordingRepo) Update(ctx context.Context, job Job) (Job, error) {
	r.updated = append(r.updated, job)
	return job, nil
}
func (r *recordingRepo) ClaimPending(ctx context.Context) (*Job, error) {
	if len(r.claimQueue) == 0 {
		return nil, nil
	}
	job := r.claimQueue[0]
	r.claimQueue = r.claimQueue[1:]
	return job, nil
}
func (r *recordingRepo) JobsNeedingCallback(ctx context.Context, now time.Time) ([]Job, error) {
	if len(r.callbackJobs) == 0 {
		return nil, nil
	}
	jobs := r.callbackJobs
	r.callbackJobs = nil
	return jobs, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func waitForCondition(timeout time.Duration, fn func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fn()
}

type fakeProvider struct {
	name         string
	videoURL     string
	err          error
	lastVideoReq dto.VideoReq
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Capabilities() providers.Capabilities {
	return providers.Capabilities{VideoGeneration: true}
}

func (f *fakeProvider) Health(ctx context.Context) error { return nil }
func (f *fakeProvider) TextGenerate(ctx context.Context, req dto.TextReq) (dto.TextResp, error) {
	return dto.TextResp{}, nil
}
func (f *fakeProvider) ImageGenerate(ctx context.Context, req dto.ImageReq) (dto.ImageResp, error) {
	return dto.ImageResp{}, nil
}
func (f *fakeProvider) VideoGenerate(ctx context.Context, req dto.VideoReq) (dto.VideoResp, error) {
	f.lastVideoReq = req
	if f.err != nil {
		return dto.VideoResp{}, f.err
	}
	return dto.VideoResp{URL: f.videoURL}, nil
}
func (f *fakeProvider) SpeechToText(ctx context.Context, req dto.STTReq) (dto.STTResp, error) {
	return dto.STTResp{}, nil
}
func (f *fakeProvider) TextToSpeech(ctx context.Context, req dto.TTSReq) (dto.TTSResp, error) {
	return dto.TTSResp{}, nil
}
func (f *fakeProvider) Embeddings(ctx context.Context, req dto.EmbeddingsReq) (dto.EmbeddingsResp, error) {
	return dto.EmbeddingsResp{}, nil
}
func (f *fakeProvider) Moderation(ctx context.Context, req dto.ModerationReq) (dto.ModerationResp, error) {
	return dto.ModerationResp{}, nil
}
