package jobs

import (
	"context"
	"sync"
	"time"
)

// Repository defines the persistence contract for jobs.
type Repository interface {
	Create(ctx context.Context, job Job) (Job, error)
	Get(ctx context.Context, id string) (Job, error)
	Update(ctx context.Context, job Job) (Job, error)
	ClaimPending(ctx context.Context) (*Job, error)
	JobsNeedingCallback(ctx context.Context, now time.Time) ([]Job, error)
}

// memoryRepository stores jobs in-memory; used when Postgres is unavailable.
type memoryRepository struct {
	mu   sync.Mutex
	jobs map[string]Job
}

// NewMemoryRepository creates an in-memory repository.
func NewMemoryRepository() Repository {
	return &memoryRepository{jobs: make(map[string]Job)}
}

func (m *memoryRepository) Create(ctx context.Context, job Job) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[job.ID] = job
	return job, nil
}

func (m *memoryRepository) Get(ctx context.Context, id string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return Job{}, ErrJobNotFound
	}
	return job, nil
}

func (m *memoryRepository) Update(ctx context.Context, job Job) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.jobs[job.ID]; !ok {
		return Job{}, ErrJobNotFound
	}
	m.jobs[job.ID] = job
	return job, nil
}

func (m *memoryRepository) ClaimPending(ctx context.Context) (*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var oldestID string
	var oldest time.Time
	for id, job := range m.jobs {
		if job.Status != StatusPending {
			continue
		}
		if oldestID == "" || job.CreatedAt.Before(oldest) {
			oldestID = id
			oldest = job.CreatedAt
		}
	}
	if oldestID == "" {
		return nil, nil
	}
	job := m.jobs[oldestID]
	job.Status = StatusRunning
	job.LastDispatchedAt = time.Now()
	job.UpdatedAt = time.Now()
	m.jobs[oldestID] = job
	copy := job
	return &copy, nil
}

func (m *memoryRepository) JobsNeedingCallback(ctx context.Context, now time.Time) ([]Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var list []Job
	for _, job := range m.jobs {
		if job.CallbackURL == "" {
			continue
		}
		if job.Status != StatusCompleted && job.Status != StatusFailed {
			continue
		}
		if job.CallbackAttempts >= job.MaxCallbackAttempts {
			continue
		}
		if job.NextCallbackAttempt.After(now) {
			continue
		}
		list = append(list, job)
	}
	return list, nil
}
