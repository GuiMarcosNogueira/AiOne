package providersessions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/midia/aione/pkg/encryption"
)

var (
	// ErrUserIDRequired indicates the user id is missing.
	ErrUserIDRequired = errors.New("user id required")
	// ErrProviderRequired indicates the provider name is missing.
	ErrProviderRequired = errors.New("provider name required")
	// ErrProviderKeyRequired indicates the provider key is missing.
	ErrProviderKeyRequired = errors.New("provider key required")
)

// Service coordinates encrypted provider sessions per user.
type Service struct {
	repo Repository
	enc  *encryption.Manager
}

// SetKeyInput captures the payload to create or update a session secret.
type SetKeyInput struct {
	UserID       string
	ProviderName string
	ProviderKey  string
	Metadata     map[string]any
	ExpiresAt    *time.Time
}

// UsageInput represents token usage increments.
type UsageInput struct {
	UserID          string
	ProviderName    string
	TokensDelta     int64
	Metadata        map[string]any
	LastInteraction time.Time
	ExpiresAt       *time.Time
}

// NewService builds a provider session service.
func NewService(repo Repository, enc *encryption.Manager) (*Service, error) {
	if repo == nil {
		return nil, errors.New("providersessions: repository required")
	}
	if enc == nil {
		return nil, errors.New("providersessions: encryption manager required")
	}
	return &Service{repo: repo, enc: enc}, nil
}

// SetProviderKey encrypts and stores the provider secret for the given user.
func (s *Service) SetProviderKey(ctx context.Context, input SetKeyInput) (SessionDetails, error) {
	if err := validateSetKeyInput(input); err != nil {
		return SessionDetails{}, err
	}
	ciphertext, err := s.enc.Encrypt([]byte(input.ProviderKey))
	if err != nil {
		return SessionDetails{}, fmt.Errorf("encrypt provider key: %w", err)
	}
	rec, err := s.repo.Upsert(ctx, UpsertParams{
		UserID:          input.UserID,
		ProviderName:    input.ProviderName,
		EncryptedKey:    ciphertext,
		EncryptionKeyID: s.enc.ActiveKeyID(),
		Metadata:        input.Metadata,
		ExpiresAt:       input.ExpiresAt,
		LastInteraction: time.Now().UTC(),
	})
	if err != nil {
		return SessionDetails{}, err
	}
	return toDetails(rec, input.ProviderKey), nil
}

// GetSession loads and decrypts the provider session for the user.
func (s *Service) GetSession(ctx context.Context, userID, provider string) (SessionDetails, error) {
	rec, err := s.repo.Get(ctx, userID, provider)
	if err != nil {
		return SessionDetails{}, err
	}
	plaintext, err := s.enc.Decrypt(rec.EncryptedKey)
	if err != nil {
		return SessionDetails{}, fmt.Errorf("decrypt provider key: %w", err)
	}
	return toDetails(rec, string(plaintext)), nil
}

// RecordUsage increments the token counter and updates interaction metadata.
func (s *Service) RecordUsage(ctx context.Context, input UsageInput) (SessionDetails, error) {
	if strings.TrimSpace(input.UserID) == "" {
		return SessionDetails{}, ErrUserIDRequired
	}
	if strings.TrimSpace(input.ProviderName) == "" {
		return SessionDetails{}, ErrProviderRequired
	}
	if input.LastInteraction.IsZero() {
		input.LastInteraction = time.Now().UTC()
	}
	rec, err := s.repo.UpdateUsage(ctx, UsageUpdateParams{
		UserID:          input.UserID,
		ProviderName:    input.ProviderName,
		TokensDelta:     input.TokensDelta,
		LastInteraction: input.LastInteraction,
		Metadata:        input.Metadata,
		ExpiresAt:       input.ExpiresAt,
	})
	if err != nil {
		return SessionDetails{}, err
	}
	plaintext, err := s.enc.Decrypt(rec.EncryptedKey)
	if err != nil {
		return SessionDetails{}, fmt.Errorf("decrypt provider key: %w", err)
	}
	return toDetails(rec, string(plaintext)), nil
}

// ResetSession removes the stored provider secret for the user.
func (s *Service) ResetSession(ctx context.Context, userID, provider string) error {
	if strings.TrimSpace(userID) == "" {
		return ErrUserIDRequired
	}
	if strings.TrimSpace(provider) == "" {
		return ErrProviderRequired
	}
	return s.repo.Delete(ctx, userID, provider)
}

func validateSetKeyInput(input SetKeyInput) error {
	if strings.TrimSpace(input.UserID) == "" {
		return ErrUserIDRequired
	}
	if strings.TrimSpace(input.ProviderName) == "" {
		return ErrProviderRequired
	}
	if strings.TrimSpace(input.ProviderKey) == "" {
		return ErrProviderKeyRequired
	}
	return nil
}

func toDetails(session Session, providerKey string) SessionDetails {
	meta := session.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	return SessionDetails{
		ProviderName:    session.ProviderName,
		ProviderKey:     providerKey,
		LastInteraction: session.LastInteraction,
		TotalTokensUsed: session.TotalTokensUsed,
		ExpiresAt:       session.ExpiresAt,
		Metadata:        meta,
	}
}
