package history

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/midia/aione/internal/services/storage"
)

var (
	// ErrUserIDRequired indicates a missing user identifier.
	ErrUserIDRequired = errors.New("user id required")
	// ErrProviderRequired indicates a missing provider name.
	ErrProviderRequired = errors.New("provider name required")
	// ErrRoleRequired indicates a missing message role.
	ErrRoleRequired = errors.New("role required")
	// ErrStorageUnavailable indicates SaveMedia was called without a storage backend.
	ErrStorageUnavailable = errors.New("storage not configured for media saving")
)

// Service coordinates access to persisted chat history.
type Service struct {
	repo    Repository
	storage storage.Storage
}

// NewService builds a history service.
func NewService(repo Repository, storage storage.Storage) (*Service, error) {
	if repo == nil {
		return nil, errors.New("history repository required")
	}
	return &Service{repo: repo, storage: storage}, nil
}

// SaveMessage persists a textual chat turn.
func (s *Service) SaveMessage(ctx context.Context, input SaveMessageInput) (Entry, error) {
	if err := validateUserProvider(input.UserID, input.ProviderName); err != nil {
		return Entry{}, err
	}
	if strings.TrimSpace(input.Role) == "" {
		return Entry{}, ErrRoleRequired
	}
	tokens := input.Tokens
	if tokens <= 0 {
		tokens = EstimateTokens(input.Message)
	}
	params := InsertParams{
		UserID:          input.UserID,
		ProviderName:    strings.ToLower(strings.TrimSpace(input.ProviderName)),
		Role:            strings.ToLower(strings.TrimSpace(input.Role)),
		Message:         input.Message,
		TokensEstimated: tokens,
	}
	return s.repo.Insert(ctx, params)
}

// SaveMedia persists a media reference, streaming the file through the configured storage backend.
func (s *Service) SaveMedia(ctx context.Context, input SaveMediaInput) (Entry, error) {
	if err := validateUserProvider(input.UserID, input.ProviderName); err != nil {
		return Entry{}, err
	}
	if strings.TrimSpace(input.Role) == "" {
		return Entry{}, ErrRoleRequired
	}
	if input.Content == nil {
		return Entry{}, errors.New("media content required")
	}
	if s.storage == nil {
		return Entry{}, ErrStorageUnavailable
	}
	mediaPath, err := s.storage.Save(ctx, input.FileName, input.Content)
	if err != nil {
		return Entry{}, fmt.Errorf("save media: %w", err)
	}
	tokens := input.Tokens
	if tokens <= 0 {
		tokens = EstimateTokens(input.MediaType)
	}
	params := InsertParams{
		UserID:          input.UserID,
		ProviderName:    strings.ToLower(strings.TrimSpace(input.ProviderName)),
		Role:            strings.ToLower(strings.TrimSpace(input.Role)),
		MediaType:       input.MediaType,
		MediaPath:       mediaPath,
		TokensEstimated: tokens,
	}
	return s.repo.Insert(ctx, params)
}

// ListHistory returns ordered entries for the user/provider pair.
func (s *Service) ListHistory(ctx context.Context, userID, provider string) ([]Entry, error) {
	if err := validateUserProvider(userID, provider); err != nil {
		return nil, err
	}
	return s.repo.List(ctx, userID, strings.ToLower(strings.TrimSpace(provider)))
}

// DeleteHistory removes all entries for the user/provider pair.
func (s *Service) DeleteHistory(ctx context.Context, userID, provider string) error {
	if err := validateUserProvider(userID, provider); err != nil {
		return err
	}
	return s.repo.DeleteAll(ctx, userID, strings.ToLower(strings.TrimSpace(provider)))
}

// TruncateHistoryToTokenLimit removes the oldest messages until the total estimated tokens fits within limit.
func (s *Service) TruncateHistoryToTokenLimit(ctx context.Context, userID, provider string, limit int) error {
	if err := validateUserProvider(userID, provider); err != nil {
		return err
	}
	if limit <= 0 {
		return nil
	}
	entries, err := s.repo.List(ctx, userID, strings.ToLower(strings.TrimSpace(provider)))
	if err != nil {
		return err
	}
	total := 0
	toDelete := make([]int64, 0)
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		total += max(entry.TokensEstimated, 1)
		if total > limit {
			toDelete = append(toDelete, entry.ID)
		}
	}
	if len(toDelete) == 0 {
		return nil
	}
	return s.repo.DeleteIDs(ctx, toDelete)
}

// EstimateTokens approximates the number of tokens in a string using a simple heuristic.
func EstimateTokens(text string) int {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0
	}
	runes := []rune(trimmed)
	// Approximate 4 characters per token as a baseline similar to GPT-3 style encodings.
	tokens := int(math.Ceil(float64(len(runes)) / 4.0))
	if tokens == 0 {
		tokens = 1
	}
	return tokens
}

// FormatContext serializes entries into a textual context block.
func FormatContext(entries []Entry) string {
	if len(entries) == 0 {
		return ""
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].CreatedAt.Before(entries[j].CreatedAt)
	})
	var b strings.Builder
	for _, entry := range entries {
		role := strings.ToUpper(entry.Role)
		if role == "" {
			role = "USER"
		}
		b.WriteString(role)
		b.WriteString(": ")
		if entry.Message != "" {
			b.WriteString(entry.Message)
		}
		if entry.MediaType != "" || entry.MediaPath != "" {
			if entry.Message != "" {
				b.WriteString(" ")
			}
			b.WriteString("[media")
			if entry.MediaType != "" {
				b.WriteString(":" + entry.MediaType)
			}
			if entry.MediaPath != "" {
				b.WriteString("=" + entry.MediaPath)
			}
			b.WriteString("]")
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func validateUserProvider(userID, provider string) error {
	if strings.TrimSpace(userID) == "" {
		return ErrUserIDRequired
	}
	if strings.TrimSpace(provider) == "" {
		return ErrProviderRequired
	}
	return nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
