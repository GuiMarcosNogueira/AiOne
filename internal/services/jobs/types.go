package jobs

import (
	"errors"
	"time"
)

var (
	// ErrJobNotFound indicates the requested job id does not exist.
	ErrJobNotFound = errors.New("job not found")
)

// Type enumerates the supported job types.
type Type string

const (
	TypeVideo   Type = "video"
	TypeGeneric Type = "generic"
)

// Status reflects the lifecycle of a job.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
)

// Job models a long running task persisted in the job store.
type Job struct {
	ID                   string         `json:"id"`
	Type                 Type           `json:"type"`
	Status               Status         `json:"status"`
	Prompt               string         `json:"prompt,omitempty"`
	Payload              map[string]any `json:"payload,omitempty"`
	Result               map[string]any `json:"result,omitempty"`
	ErrorMessage         string         `json:"error_message,omitempty"`
	FilePath             string         `json:"file_path,omitempty"`
	CallbackURL          string         `json:"callback_url,omitempty"`
	CallbackAttempts     int            `json:"callback_attempts"`
	MaxCallbackAttempts  int            `json:"max_callback_attempts"`
	NextCallbackAttempt  time.Time      `json:"next_callback_attempt,omitempty"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
	LastDispatchedAt     time.Time      `json:"last_dispatched_at,omitempty"`
	LastCallbackResponse string         `json:"last_callback_response,omitempty"`
}

// Clone returns a shallow copy of the job.
func (j Job) Clone() Job {
	copy := j
	return copy
}
