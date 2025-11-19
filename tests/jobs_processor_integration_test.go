package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/midia/aione/internal/providers"
	"github.com/midia/aione/internal/providers/dto"
	"github.com/midia/aione/internal/services/jobs"
	providermanager "github.com/midia/aione/internal/services/provider"
)

func TestProcessorCompletesJobAndSendsCallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	repo := jobs.NewMemoryRepository()

	callbackCh := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode callback payload: %v", err)
		}
		callbackCh <- payload
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	provider := &stubProvider{name: "video-stub", videoResp: dto.VideoResp{URL: "https://stub/video.mp4"}}
	manager := providermanager.NewManager([]providers.Provider{provider})

	processor := jobs.NewProcessor(repo, manager, testLogger(), time.Millisecond, jobs.Options{CallbackBackoff: 5 * time.Millisecond, CallbackMaxAttempts: 3})

	job := jobs.Job{
		ID:                  uuid.NewString(),
		Type:                jobs.TypeVideo,
		Status:              jobs.StatusPending,
		Prompt:              "trailer",
		CallbackURL:         server.URL,
		MaxCallbackAttempts: 3,
		CreatedAt:           time.Now().Add(-time.Minute),
		UpdatedAt:           time.Now().Add(-time.Minute),
	}
	if _, err := repo.Create(ctx, job); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	processor.Start(ctx)

	if !waitForCondition(2*time.Second, func() bool {
		current, err := repo.Get(ctx, job.ID)
		return err == nil && current.Status == jobs.StatusCompleted
	}) {
		t.Fatalf("job did not reach completed state")
	}

	select {
	case payload := <-callbackCh:
		if payload["id"] != job.ID {
			t.Fatalf("callback payload missing job id: %v", payload)
		}
		if payload["status"] != string(jobs.StatusCompleted) {
			t.Fatalf("callback payload missing status: %v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("did not receive callback")
	}
}
