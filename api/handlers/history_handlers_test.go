package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/midia/aione/internal/services/auth"
	"github.com/midia/aione/internal/services/history"
)

func TestHistoryListRequiresAuth(t *testing.T) {
	repo := newHistoryMemoryRepo()
	svc, err := history.NewService(repo, nil)
	if err != nil {
		t.Fatalf("history service: %v", err)
	}
	handler := NewHistoryAPI(testLogger(), svc)
	req := httptest.NewRequest(http.MethodGet, "/history/openai", nil)
	rec := httptest.NewRecorder()
	handler.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHistoryListReturnsEntries(t *testing.T) {
	repo := newHistoryMemoryRepo()
	repo.entries = append(repo.entries, history.Entry{ID: 1, UserID: "user-1", ProviderName: "openai", Role: "user", Message: "hi", CreatedAt: time.Now()})
	svc, err := history.NewService(repo, nil)
	if err != nil {
		t.Fatalf("history service: %v", err)
	}
	handler := NewHistoryAPI(testLogger(), svc)
	rec := executeHistoryRequest(t, handler.Handler(), http.MethodGet, "/history/openai", "user-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp responseEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data == nil {
		t.Fatalf("expected entries in response")
	}
}

func TestHistoryClear(t *testing.T) {
	repo := newHistoryMemoryRepo()
	repo.entries = append(repo.entries, history.Entry{ID: 1, UserID: "user-1", ProviderName: "openai", Role: "user", Message: "hello", CreatedAt: time.Now()})
	svc, _ := history.NewService(repo, nil)
	handler := NewHistoryAPI(testLogger(), svc)
	rec := executeHistoryRequest(t, handler.Handler(), http.MethodDelete, "/history/openai/clear", "user-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if len(repo.entries) != 0 {
		t.Fatalf("expected entries to be cleared")
	}
}

func executeHistoryRequest(t *testing.T, handler http.Handler, method, path, userID string) *httptest.ResponseRecorder {
	t.Helper()
	tokens, err := auth.NewTokenManager("access", "refresh")
	if err != nil {
		t.Fatalf("token manager: %v", err)
	}
	token, _, err := tokens.GenerateAccess(userID, "user@example.com", time.Minute)
	if err != nil {
		t.Fatalf("generate access: %v", err)
	}
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	auth.AuthMiddleware(tokens)(handler).ServeHTTP(rec, req)
	return rec
}

type historyMemoryRepo struct {
	entries []history.Entry
}

func newHistoryMemoryRepo() *historyMemoryRepo {
	return &historyMemoryRepo{}
}

func (m *historyMemoryRepo) Insert(ctx context.Context, params history.InsertParams) (history.Entry, error) {
	entry := history.Entry{
		ID:           int64(len(m.entries) + 1),
		UserID:       params.UserID,
		ProviderName: params.ProviderName,
		Role:         params.Role,
		Message:      params.Message,
		MediaType:    params.MediaType,
		MediaPath:    params.MediaPath,
		CreatedAt:    time.Now(),
	}
	m.entries = append(m.entries, entry)
	return entry, nil
}

func (m *historyMemoryRepo) List(ctx context.Context, userID, provider string) ([]history.Entry, error) {
	var out []history.Entry
	for _, entry := range m.entries {
		if entry.UserID == userID && entry.ProviderName == provider {
			out = append(out, entry)
		}
	}
	return out, nil
}

func (m *historyMemoryRepo) DeleteAll(ctx context.Context, userID, provider string) error {
	filtered := m.entries[:0]
	for _, entry := range m.entries {
		if !(entry.UserID == userID && entry.ProviderName == provider) {
			filtered = append(filtered, entry)
		}
	}
	m.entries = filtered
	return nil
}

func (m *historyMemoryRepo) DeleteIDs(ctx context.Context, ids []int64) error {
	toDelete := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		toDelete[id] = struct{}{}
	}
	filtered := m.entries[:0]
	for _, entry := range m.entries {
		if _, ok := toDelete[entry.ID]; !ok {
			filtered = append(filtered, entry)
		}
	}
	m.entries = filtered
	return nil
}
