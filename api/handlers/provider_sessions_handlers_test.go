package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/midia/aione/internal/services/auth"
	"github.com/midia/aione/internal/services/providersessions"
)

func TestProviderSessionCreateHandler(t *testing.T) {
	api, repo, tokens := newProviderSessionTestAPI(t)
	body := strings.NewReader(`{"title":"Demo chat","metadata":{"label":"team"}}`)
	rec := executeAuthedRequest(t, tokens, api.Handler(), http.MethodPost, "/providers/openai/sessions", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}
	var resp struct {
		Data providersessions.SessionDetails `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.Title != "Demo chat" {
		t.Fatalf("unexpected payload: %+v", resp.Data)
	}
	if len(repo.sessions) != 1 {
		t.Fatalf("expected session stored")
	}
}

func TestProviderSessionListHandler(t *testing.T) {
	api, repo, tokens := newProviderSessionTestAPI(t)
	repo.seed("user-1", providersessions.Session{ID: "s1", UserID: "user-1", ProviderName: "openai", Title: "Chat", LastInteraction: time.Now(), CreatedAt: time.Now(), UpdatedAt: time.Now()})
	rec := executeAuthedRequest(t, tokens, api.Handler(), http.MethodGet, "/providers/openai/sessions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		Data []providersessions.SessionDetails `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected single session, got %d", len(resp.Data))
	}
}

func TestProviderSessionGetNotFound(t *testing.T) {
	api, _, tokens := newProviderSessionTestAPI(t)
	rec := executeAuthedRequest(t, tokens, api.Handler(), http.MethodGet, "/providers/openai/sessions/missing", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestProviderSessionArchive(t *testing.T) {
	api, repo, tokens := newProviderSessionTestAPI(t)
	repo.seed("user-1", providersessions.Session{ID: "s1", UserID: "user-1", ProviderName: "openai", Title: "Chat", LastInteraction: time.Now(), CreatedAt: time.Now(), UpdatedAt: time.Now()})
	rec := executeAuthedRequest(t, tokens, api.Handler(), http.MethodDelete, "/providers/openai/sessions/s1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if repo.sessions["s1"].ArchivedAt == nil {
		t.Fatalf("expected session archived")
	}
}

func TestProviderSessionRequiresAuth(t *testing.T) {
	api, _, _ := newProviderSessionTestAPI(t)
	req := httptest.NewRequest(http.MethodGet, "/providers/openai/sessions", nil)
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestExtractProviderPath(t *testing.T) {
	provider, tail := extractProviderPath("openai/sessions/s1")
	if provider != "openai" {
		t.Fatalf("expected provider openai, got %s", provider)
	}
	if tail != "sessions/s1" {
		t.Fatalf("unexpected tail %s", tail)
	}
	provider, tail = extractProviderPath("/")
	if provider != "" || tail != "" {
		t.Fatalf("expected empty path")
	}
}

func newProviderSessionTestAPI(t *testing.T) (*ProviderSessionAPI, *handlerMemoryRepo, *auth.TokenManager) {
	t.Helper()
	repo := newHandlerMemoryRepo()
	service, err := providersessions.NewService(repo)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	api := NewProviderSessionAPI(testLogger(), service)
	tokens, err := auth.NewTokenManager("access-secret", "refresh-secret")
	if err != nil {
		t.Fatalf("token manager: %v", err)
	}
	return api, repo, tokens
}

func executeAuthedRequest(t *testing.T, tokens *auth.TokenManager, handler http.Handler, method, path string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	access, _, err := tokens.GenerateAccess("user-1", "user@example.com", time.Minute)
	if err != nil {
		t.Fatalf("generate access: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+access)
	rec := httptest.NewRecorder()
	auth.AuthMiddleware(tokens)(handler).ServeHTTP(rec, req)
	return rec
}

type handlerMemoryRepo struct {
	sessions map[string]providersessions.Session
}

func newHandlerMemoryRepo() *handlerMemoryRepo {
	return &handlerMemoryRepo{sessions: map[string]providersessions.Session{}}
}

func (r *handlerMemoryRepo) seed(userID string, session providersessions.Session) {
	session.UserID = userID
	r.sessions[session.ID] = session
}

func (r *handlerMemoryRepo) Create(ctx context.Context, params providersessions.CreateParams) (providersessions.Session, error) {
	now := time.Now().UTC()
	sess := providersessions.Session{
		ID:              params.ID,
		UserID:          params.UserID,
		ProviderName:    params.ProviderName,
		Title:           params.Title,
		Metadata:        params.Metadata,
		ExpiresAt:       params.ExpiresAt,
		LastInteraction: now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	r.sessions[sess.ID] = sess
	return sess, nil
}

func (r *handlerMemoryRepo) Get(ctx context.Context, userID, sessionID string) (providersessions.Session, error) {
	sess, ok := r.sessions[sessionID]
	if !ok || sess.UserID != userID {
		return providersessions.Session{}, providersessions.ErrSessionNotFound
	}
	return sess, nil
}

func (r *handlerMemoryRepo) List(ctx context.Context, params providersessions.ListParams) ([]providersessions.Session, error) {
	var sessions []providersessions.Session
	for _, sess := range r.sessions {
		if sess.UserID != params.UserID {
			continue
		}
		if params.ProviderName != "" && sess.ProviderName != params.ProviderName {
			continue
		}
		if !params.IncludeArchived && sess.ArchivedAt != nil {
			continue
		}
		sessions = append(sessions, sess)
	}
	return sessions, nil
}

func (r *handlerMemoryRepo) UpdateUsage(ctx context.Context, params providersessions.UsageUpdateParams) (providersessions.Session, error) {
	sess, ok := r.sessions[params.SessionID]
	if !ok || sess.UserID != params.UserID {
		return providersessions.Session{}, providersessions.ErrSessionNotFound
	}
	sess.TotalTokensUsed += params.TokensDelta
	if !params.LastInteraction.IsZero() {
		sess.LastInteraction = params.LastInteraction
	}
	if params.Metadata != nil {
		sess.Metadata = params.Metadata
	}
	if params.ExpiresAt != nil {
		sess.ExpiresAt = params.ExpiresAt
	}
	sess.UpdatedAt = time.Now().UTC()
	r.sessions[sess.ID] = sess
	return sess, nil
}

func (r *handlerMemoryRepo) Archive(ctx context.Context, userID, sessionID string) error {
	sess, ok := r.sessions[sessionID]
	if !ok || sess.UserID != userID {
		return providersessions.ErrSessionNotFound
	}
	now := time.Now().UTC()
	sess.ArchivedAt = &now
	sess.UpdatedAt = now
	r.sessions[sessionID] = sess
	return nil
}
