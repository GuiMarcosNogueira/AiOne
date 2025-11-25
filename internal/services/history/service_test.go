package history

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSaveMessageStoresEntry(t *testing.T) {
	repo := newFakeRepo()
	svc, err := NewService(repo, nil)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	entry, err := svc.SaveMessage(context.Background(), SaveMessageInput{
		UserID:       "user-1",
		SessionID:    "session-1",
		ProviderName: "openai",
		Role:         "user",
		Message:      "hello world",
	})
	if err != nil {
		t.Fatalf("save message: %v", err)
	}
	if entry.ID == 0 || entry.ProviderName != "openai" {
		t.Fatalf("unexpected entry: %+v", entry)
	}
	if repo.inserted[0].TokensEstimated == 0 {
		t.Fatalf("expected tokens to be estimated")
	}
}

func TestSaveMediaRequiresStorage(t *testing.T) {
	repo := newFakeRepo()
	svc, err := NewService(repo, nil)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if _, err := svc.SaveMedia(context.Background(), SaveMediaInput{UserID: "user", ProviderName: "openai", Role: "user", MediaType: "image", FileName: "file.png", Content: strings.NewReader("data")}); !errors.Is(err, ErrStorageUnavailable) {
		t.Fatalf("expected storage error, got %v", err)
	}
}

func TestListAndDeleteHistory(t *testing.T) {
	repo := newFakeRepo()
	repo.entries = []Entry{
		{ID: 1, UserID: "user", ProviderName: "openai", Message: "a"},
	}
	svc, _ := NewService(repo, nil)
	entries, err := svc.ListHistory(context.Background(), "user", "openai")
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected single entry, got %v err=%v", entries, err)
	}
	if err := svc.DeleteHistory(context.Background(), "user", "openai"); err != nil {
		t.Fatalf("delete history: %v", err)
	}
	if len(repo.entries) != 0 {
		t.Fatalf("expected entries deleted")
	}
}

func TestListAndDeleteSessionHistory(t *testing.T) {
	repo := newFakeRepo()
	repo.entries = []Entry{
		{ID: 1, UserID: "user", SessionID: "session-a", ProviderName: "openai", Message: "a"},
		{ID: 2, UserID: "user", SessionID: "session-b", ProviderName: "openai", Message: "b"},
	}
	svc, _ := NewService(repo, nil)
	entries, err := svc.ListSessionHistory(context.Background(), "user", "session-a")
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected single session entry, got %v err=%v", entries, err)
	}
	if err := svc.DeleteSessionHistory(context.Background(), "user", "session-a"); err != nil {
		t.Fatalf("delete session history: %v", err)
	}
	if len(repo.entries) != 1 || repo.entries[0].SessionID != "session-b" {
		t.Fatalf("expected only other session to remain, got %+v", repo.entries)
	}
}

func TestTruncateHistoryToTokenLimit(t *testing.T) {
	repo := newFakeRepo()
	repo.entries = []Entry{
		{ID: 1, UserID: "user", ProviderName: "openai", TokensEstimated: 10, CreatedAt: time.Now().Add(-2 * time.Minute)},
		{ID: 2, UserID: "user", ProviderName: "openai", TokensEstimated: 5, CreatedAt: time.Now().Add(-time.Minute)},
		{ID: 3, UserID: "user", ProviderName: "openai", TokensEstimated: 2, CreatedAt: time.Now()},
	}
	svc, _ := NewService(repo, nil)
	removed, err := svc.TruncateHistoryToTokenLimit(context.Background(), "user", "openai", 7)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if removed == 0 || len(repo.deletedIDs) == 0 {
		t.Fatalf("expected some entries removed, removed=%d ids=%v", removed, repo.deletedIDs)
	}
}

func TestTruncateSessionHistoryToTokenLimit(t *testing.T) {
	repo := newFakeRepo()
	repo.entries = []Entry{
		{ID: 1, UserID: "user", SessionID: "session-a", TokensEstimated: 5, CreatedAt: time.Now().Add(-3 * time.Minute)},
		{ID: 2, UserID: "user", SessionID: "session-a", TokensEstimated: 4, CreatedAt: time.Now().Add(-2 * time.Minute)},
		{ID: 3, UserID: "user", SessionID: "session-a", TokensEstimated: 2, CreatedAt: time.Now().Add(-time.Minute)},
		{ID: 4, UserID: "user", SessionID: "session-b", TokensEstimated: 100, CreatedAt: time.Now()},
	}
	svc, _ := NewService(repo, nil)
	removed, err := svc.TruncateSessionHistoryToTokenLimit(context.Background(), "user", "session-a", 6)
	if err != nil {
		t.Fatalf("truncate session: %v", err)
	}
	if removed == 0 {
		t.Fatalf("expected some entries removed")
	}
	for _, id := range repo.deletedIDs {
		if id == 4 {
			t.Fatalf("should not delete other sessions")
		}
	}
}

func TestEstimateTokens(t *testing.T) {
	if EstimateTokens("hello world") == 0 {
		t.Fatalf("expected non-zero tokens")
	}
	if EstimateTokens("   ") != 0 {
		t.Fatalf("expected zero tokens for whitespace")
	}
}

func TestFormatContextAvoidsAssistantPrefix(t *testing.T) {
	entries := []Entry{
		{Role: "user", Message: "Oi"},
		{Role: "assistant", Message: "Olá"},
	}
	formatted := FormatContext(entries)
	if strings.Contains(formatted, "ASSISTANT:") {
		t.Fatalf("expected formatted context to avoid ASSISTANT prefix, got %q", formatted)
	}
	if !strings.Contains(formatted, "Previous assistant reply: Olá") {
		t.Fatalf("expected descriptive assistant label, got %q", formatted)
	}
}

// fake repo implementation for tests

type fakeRepo struct {
	entries    []Entry
	inserted   []InsertParams
	deletedIDs []int64
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{}
}

func (f *fakeRepo) Insert(ctx context.Context, params InsertParams) (Entry, error) {
	params.ProviderName = strings.ToLower(params.ProviderName)
	entry := Entry{
		ID:              int64(len(f.entries) + 1),
		UserID:          params.UserID,
		SessionID:       params.SessionID,
		ProviderName:    params.ProviderName,
		Role:            params.Role,
		Message:         params.Message,
		MediaType:       params.MediaType,
		MediaPath:       params.MediaPath,
		TokensEstimated: params.TokensEstimated,
		CreatedAt:       time.Now(),
	}
	f.entries = append(f.entries, entry)
	f.inserted = append(f.inserted, params)
	return entry, nil
}

func (f *fakeRepo) List(ctx context.Context, userID, provider string) ([]Entry, error) {
	var out []Entry
	for _, entry := range f.entries {
		if entry.UserID == userID && entry.ProviderName == provider {
			out = append(out, entry)
		}
	}
	return out, nil
}

func (f *fakeRepo) ListBySession(ctx context.Context, userID, sessionID string) ([]Entry, error) {
	var out []Entry
	for _, entry := range f.entries {
		if entry.UserID == userID && entry.SessionID == sessionID {
			out = append(out, entry)
		}
	}
	return out, nil
}

func (f *fakeRepo) DeleteAll(ctx context.Context, userID, provider string) error {
	filtered := f.entries[:0]
	for _, entry := range f.entries {
		if !(entry.UserID == userID && entry.ProviderName == provider) {
			filtered = append(filtered, entry)
		}
	}
	f.entries = filtered
	return nil
}

func (f *fakeRepo) DeleteSession(ctx context.Context, userID, sessionID string) error {
	filtered := f.entries[:0]
	for _, entry := range f.entries {
		if !(entry.UserID == userID && entry.SessionID == sessionID) {
			filtered = append(filtered, entry)
		}
	}
	f.entries = filtered
	return nil
}

func (f *fakeRepo) DeleteIDs(ctx context.Context, ids []int64) error {
	f.deletedIDs = append(f.deletedIDs, ids...)
	return nil
}
