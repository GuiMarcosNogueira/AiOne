package providersessions

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Service coordinates conversational sessions per user/provider.
type Service struct {
	repo Repository
}

// CreateSessionInput defines the payload to start a new session.
type CreateSessionInput struct {
	UserID       string
	ProviderName string
	Title        string
	Metadata     map[string]any
	ExpiresAt    *time.Time
}

// ListSessionsInput filters the listing of user sessions.
type ListSessionsInput struct {
	UserID          string
	ProviderName    string
	Limit           int
	IncludeArchived bool
}

// UsageInput represents token usage increments.
type UsageInput struct {
	SessionID       string
	UserID          string
	ProviderName    string
	TokensDelta     int64
	Metadata        map[string]any
	LastInteraction time.Time
	ExpiresAt       *time.Time
}

// NewService builds a chat session service.
func NewService(repo Repository) (*Service, error) {
	if repo == nil {
		return nil, errors.New("providersessions: repository required")
	}
	return &Service{repo: repo}, nil
}

// CreateSession opens a new conversation session for the user/provider.
func (s *Service) CreateSession(ctx context.Context, input CreateSessionInput) (SessionDetails, error) {
	if err := validateCreateInput(input); err != nil {
		return SessionDetails{}, err
	}
	params := CreateParams{
		ID:           uuid.NewString(),
		UserID:       strings.TrimSpace(input.UserID),
		ProviderName: strings.ToLower(strings.TrimSpace(input.ProviderName)),
		Title:        strings.TrimSpace(input.Title),
		Metadata:     input.Metadata,
		ExpiresAt:    input.ExpiresAt,
	}
	sess, err := s.repo.Create(ctx, params)
	if err != nil {
		return SessionDetails{}, err
	}
	return toDetails(sess), nil
}

// GetSession retrieves a session scoped to the user.
func (s *Service) GetSession(ctx context.Context, userID, sessionID string) (SessionDetails, error) {
	if strings.TrimSpace(userID) == "" {
		return SessionDetails{}, ErrUserIDRequired
	}
	if strings.TrimSpace(sessionID) == "" {
		return SessionDetails{}, ErrSessionIDRequired
	}
	sess, err := s.repo.Get(ctx, userID, sessionID)
	if err != nil {
		return SessionDetails{}, err
	}
	return toDetails(sess), nil
}

// ListSessions returns the most recent sessions for a user.
func (s *Service) ListSessions(ctx context.Context, input ListSessionsInput) ([]SessionDetails, error) {
	if strings.TrimSpace(input.UserID) == "" {
		return nil, ErrUserIDRequired
	}
	params := ListParams{
		UserID:          strings.TrimSpace(input.UserID),
		ProviderName:    strings.ToLower(strings.TrimSpace(input.ProviderName)),
		Limit:           input.Limit,
		IncludeArchived: input.IncludeArchived,
	}
	sessions, err := s.repo.List(ctx, params)
	if err != nil {
		return nil, err
	}
	results := make([]SessionDetails, 0, len(sessions))
	for _, s := range sessions {
		results = append(results, toDetails(s))
	}
	return results, nil
}

// RecordUsage updates interaction metrics for a session.
func (s *Service) RecordUsage(ctx context.Context, input UsageInput) (SessionDetails, error) {
	if strings.TrimSpace(input.UserID) == "" {
		return SessionDetails{}, ErrUserIDRequired
	}
	if strings.TrimSpace(input.ProviderName) == "" {
		return SessionDetails{}, ErrProviderRequired
	}
	if strings.TrimSpace(input.SessionID) == "" {
		return SessionDetails{}, ErrSessionIDRequired
	}
	if input.LastInteraction.IsZero() {
		input.LastInteraction = time.Now().UTC()
	}
	rec, err := s.repo.UpdateUsage(ctx, UsageUpdateParams{
		SessionID:       input.SessionID,
		UserID:          input.UserID,
		ProviderName:    strings.ToLower(strings.TrimSpace(input.ProviderName)),
		TokensDelta:     input.TokensDelta,
		LastInteraction: input.LastInteraction,
		Metadata:        input.Metadata,
		ExpiresAt:       input.ExpiresAt,
	})
	if err != nil {
		return SessionDetails{}, err
	}
	return toDetails(rec), nil
}

// ArchiveSession marks a session as archived.
func (s *Service) ArchiveSession(ctx context.Context, userID, sessionID string) error {
	if strings.TrimSpace(userID) == "" {
		return ErrUserIDRequired
	}
	if strings.TrimSpace(sessionID) == "" {
		return ErrSessionIDRequired
	}
	return s.repo.Archive(ctx, userID, sessionID)
}

func validateCreateInput(input CreateSessionInput) error {
	if strings.TrimSpace(input.UserID) == "" {
		return ErrUserIDRequired
	}
	if strings.TrimSpace(input.ProviderName) == "" {
		return ErrProviderRequired
	}
	if strings.TrimSpace(input.Title) == "" {
		return ErrTitleRequired
	}
	return nil
}

func toDetails(session Session) SessionDetails {
	meta := session.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	return SessionDetails{
		ID:              session.ID,
		ProviderName:    session.ProviderName,
		Title:           session.Title,
		LastInteraction: session.LastInteraction,
		TotalTokensUsed: session.TotalTokensUsed,
		ExpiresAt:       session.ExpiresAt,
		Metadata:        meta,
		ArchivedAt:      session.ArchivedAt,
	}
}
