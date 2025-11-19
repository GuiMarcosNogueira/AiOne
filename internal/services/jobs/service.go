package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"log/slog"

	"github.com/google/uuid"

	"github.com/midia/aione/internal/providers/dto"
	providermanager "github.com/midia/aione/internal/services/provider"
	"github.com/midia/aione/internal/services/storage"
)

// CreateInput holds information provided by the API layer when creating a job.
type CreateInput struct {
	Type        Type
	Prompt      string
	Payload     map[string]any
	CallbackURL string
	File        multipart.File
	FileName    string
}

// Options customize the service behaviour.
type Options struct {
	CallbackMaxAttempts int
	CallbackBackoff     time.Duration
}

// Service coordinates job creation, retrieval, and upload management.
type Service struct {
	repo      Repository
	storage   storage.Storage
	providers *providermanager.Manager
	log       *slog.Logger
	opts      Options
}

// NewService bootstraps a job service.
func NewService(repo Repository, storage storage.Storage, providers *providermanager.Manager, log *slog.Logger, opts Options) *Service {
	return &Service{repo: repo, storage: storage, providers: providers, log: log, opts: opts}
}

// Create registers a new job and persists it.
func (s *Service) Create(ctx context.Context, input CreateInput) (Job, error) {
	if input.Type == "" {
		input.Type = TypeGeneric
	}
	if input.Type != TypeVideo && input.Type != TypeGeneric {
		return Job{}, fmt.Errorf("unsupported job type %s", input.Type)
	}
	var filePath string
	if input.File != nil {
		defer input.File.Close()
		path, err := s.storage.Save(ctx, input.FileName, input.File)
		if err != nil {
			return Job{}, err
		}
		filePath = path
	}
	job := Job{
		ID:                  uuid.NewString(),
		Type:                input.Type,
		Status:              StatusPending,
		Prompt:              input.Prompt,
		Payload:             input.Payload,
		FilePath:            filePath,
		CallbackURL:         input.CallbackURL,
		MaxCallbackAttempts: max(1, s.opts.CallbackMaxAttempts),
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}
	created, err := s.repo.Create(ctx, job)
	if err != nil {
		return Job{}, err
	}
	return created, nil
}

// Get fetches job status by identifier.
func (s *Service) Get(ctx context.Context, id string) (Job, error) {
	return s.repo.Get(ctx, id)
}

// Processor orchestrates job execution and callbacks.
type Processor struct {
	repo        Repository
	providers   *providermanager.Manager
	log         *slog.Logger
	httpClient  *http.Client
	interval    time.Duration
	callbackCfg Options
}

// NewProcessor creates a job processor.
func NewProcessor(repo Repository, providers *providermanager.Manager, log *slog.Logger, interval time.Duration, opts Options) *Processor {
	return &Processor{
		repo:       repo,
		providers:  providers,
		log:        log,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		interval:   interval,
		callbackCfg: Options{
			CallbackBackoff:     opts.CallbackBackoff,
			CallbackMaxAttempts: opts.CallbackMaxAttempts,
		},
	}
}

// Start launches processing and callback workers until the context is cancelled.
func (p *Processor) Start(ctx context.Context) {
	go p.runProcessor(ctx)
	go p.runCallbacks(ctx)
}

func (p *Processor) runProcessor(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		job, err := p.repo.ClaimPending(ctx)
		if err != nil {
			p.log.Error("claim pending job", slog.Any("error", err))
			<-ticker.C
			continue
		}
		if job == nil {
			<-ticker.C
			continue
		}
		p.processJob(ctx, job)
	}
}

func (p *Processor) runCallbacks(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		jobs, err := p.repo.JobsNeedingCallback(ctx, time.Now())
		if err != nil {
			p.log.Error("fetch callback jobs", slog.Any("error", err))
			continue
		}
		for _, job := range jobs {
			p.dispatchCallback(ctx, job)
		}
	}
}

func (p *Processor) processJob(ctx context.Context, job *Job) {
	logger := p.log.With(slog.String("job_id", job.ID))
	var result map[string]any
	var err error
	switch job.Type {
	case TypeVideo:
		result, err = p.runVideoJob(ctx, job)
	default:
		err = fmt.Errorf("unsupported job type %s", job.Type)
	}
	if err != nil {
		job.Status = StatusFailed
		job.ErrorMessage = err.Error()
	} else {
		job.Status = StatusCompleted
		job.Result = result
	}
	job.NextCallbackAttempt = time.Now()
	job.UpdatedAt = time.Now()
	if _, updateErr := p.repo.Update(ctx, *job); updateErr != nil {
		logger.Error("update job", slog.Any("error", updateErr))
	}
}

func (p *Processor) runVideoJob(ctx context.Context, job *Job) (map[string]any, error) {
	if p.providers == nil {
		return map[string]any{"message": "providers unavailable"}, nil
	}
	prompt := job.Prompt
	if prompt == "" {
		prompt = fmt.Sprint(job.Payload["prompt"])
	}
	res, err := p.providers.VideoGenerate(ctx, dto.VideoReq{Prompt: prompt})
	if err != nil {
		return nil, err
	}
	return map[string]any{"provider": res.Provider, "video_url": res.Data.URL}, nil
}

func (p *Processor) dispatchCallback(ctx context.Context, job Job) {
	body := map[string]any{
		"id":     job.ID,
		"status": job.Status,
		"result": job.Result,
		"error":  job.ErrorMessage,
	}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, job.CallbackURL, marshalBody(body))
	if err != nil {
		p.log.Error("build callback request", slog.Any("error", err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		p.handleCallbackResult(ctx, job, err, 0)
		return
	}
	defer resp.Body.Close()
	p.handleCallbackResult(ctx, job, nil, resp.StatusCode)
}

func marshalBody(payload map[string]any) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		enc := json.NewEncoder(pw)
		_ = enc.Encode(payload)
		pw.Close()
	}()
	return pr
}

func (p *Processor) handleCallbackResult(ctx context.Context, job Job, err error, status int) {
	job.CallbackAttempts++
	if err == nil && status >= 200 && status < 300 {
		job.NextCallbackAttempt = time.Time{}
		job.LastCallbackResponse = fmt.Sprintf("status=%d", status)
	} else {
		job.LastCallbackResponse = fmt.Sprintf("error=%v status=%d", err, status)
		job.NextCallbackAttempt = time.Now().Add(p.callbackCfg.CallbackBackoff)
	}
	if job.CallbackAttempts >= job.MaxCallbackAttempts {
		job.NextCallbackAttempt = time.Time{}
	}
	if _, updateErr := p.repo.Update(ctx, job); updateErr != nil {
		p.log.Error("update callback attempts", slog.Any("error", updateErr))
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
