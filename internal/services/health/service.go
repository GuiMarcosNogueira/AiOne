package health

import (
	"context"
	"time"

	"github.com/midia/aione/internal/providers"
)

// Status captures the health state of one provider.
type Status struct {
	Name    string    `json:"name"`
	Healthy bool      `json:"healthy"`
	Error   string    `json:"error,omitempty"`
	Checked time.Time `json:"checked_at"`
}

// Service coordinates health checks across providers.
type Service struct {
	providers []providers.Provider
}

// NewService instantiates a health service with the given providers.
func NewService(providers []providers.Provider) *Service {
	return &Service{providers: providers}
}

// Check runs health probes against every provider and returns their status.
func (s *Service) Check(ctx context.Context) []Status {
	statuses := make([]Status, 0, len(s.providers))
	for _, provider := range s.providers {
		err := provider.Health(ctx)
		status := Status{
			Name:    provider.Name(),
			Healthy: err == nil,
			Checked: time.Now().UTC(),
		}
		if err != nil {
			status.Error = err.Error()
		}
		statuses = append(statuses, status)
	}
	return statuses
}
