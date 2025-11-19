package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/midia/aione/internal/services/auth"
	"github.com/midia/aione/internal/services/providersessions"
	"github.com/midia/aione/pkg/encryption"
)

func TestProviderSessionSetKeyHandler(t *testing.T) {
	api, _, tokens := newProviderSessionTestAPI(t)
	body := strings.NewReader(`{"provider_key":"sk-123","metadata":{"label":"team"}}`)
	rec := executeAuthedRequest(t, tokens, api.Handler(), http.MethodPost, "/providers/openai/set-key", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		Data providersessions.SessionDetails `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.ProviderName != "openai" || resp.Data.ProviderKey != "sk-123" {
		t.Fatalf("unexpected payload: %+v", resp.Data)
	}
}

func TestProviderSessionGetSessionNotFound(t *testing.T) {
	api, _, tokens := newProviderSessionTestAPI(t)
	rec := executeAuthedRequest(t, tokens, api.Handler(), http.MethodGet, "/providers/openai/session", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestProviderSessionResetSession(t *testing.T) {
	api, repo, tokens := newProviderSessionTestAPI(t)
	_, err := api.service.SetProviderKey(
		context.Background(),
		providersessions.SetKeyInput{UserID: "user-1", ProviderName: "openai", ProviderKey: "sk-erase"},
	)
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	rec := executeAuthedRequest(t, tokens, api.Handler(), http.MethodDelete, "/providers/openai/session/reset", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if repo.hasSession("user-1", "openai") {
		t.Fatalf("expected session to be removed")
	}
}

func TestProviderSessionValidationError(t *testing.T) {
	api, _, tokens := newProviderSessionTestAPI(t)
	body := strings.NewReader(`{"metadata":{}}`)
	rec := executeAuthedRequest(t, tokens, api.Handler(), http.MethodPost, "/providers/openai/set-key", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestProviderSessionRequiresAuth(t *testing.T) {
	api, _, _ := newProviderSessionTestAPI(t)
	req := httptest.NewRequest(http.MethodPost, "/providers/openai/set-key", strings.NewReader(`{"provider_key":"sk"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestExtractProviderPath(t *testing.T) {
	provider, tail := extractProviderPath("openai/session/reset")
	if provider != "openai" {
		t.Fatalf("expected provider openai, got %s", provider)
	}
	if tail != "session/reset" {
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
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	enc, err := encryption.NewManager("primary", map[string]string{"primary": key})
	if err != nil {
		t.Fatalf("encryption manager: %v", err)
	}
	service, err := providersessions.NewService(repo, enc)
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

func (r *handlerMemoryRepo) Upsert(ctx context.Context, params providersessions.UpsertParams) (providersessions.Session, error) {
	key := params.UserID + "|" + params.ProviderName
	now := time.Now().UTC()
	sess := providersessions.Session{
		UserID:          params.UserID,
		ProviderName:    params.ProviderName,
		EncryptedKey:    append([]byte(nil), params.EncryptedKey...),
		EncryptionKeyID: params.EncryptionKeyID,
		LastInteraction: params.LastInteraction,
		Metadata:        params.Metadata,
		ExpiresAt:       params.ExpiresAt,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	r.sessions[key] = sess
	return sess, nil
}

func (r *handlerMemoryRepo) Get(ctx context.Context, userID, provider string) (providersessions.Session, error) {
	key := userID + "|" + provider
	sess, ok := r.sessions[key]
	if !ok {
		return providersessions.Session{}, providersessions.ErrSessionNotFound
	}
	return sess, nil
}

func (r *handlerMemoryRepo) UpdateUsage(ctx context.Context, params providersessions.UsageUpdateParams) (providersessions.Session, error) {
	key := params.UserID + "|" + params.ProviderName
	sess, ok := r.sessions[key]
	if !ok {
		return providersessions.Session{}, providersessions.ErrSessionNotFound
	}
	sess.TotalTokensUsed += params.TokensDelta
	if params.Metadata != nil {
		sess.Metadata = params.Metadata
	}
	sess.LastInteraction = params.LastInteraction
	sess.UpdatedAt = time.Now().UTC()
	r.sessions[key] = sess
	return sess, nil
}

func (r *handlerMemoryRepo) Delete(ctx context.Context, userID, provider string) error {
	key := userID + "|" + provider
	if _, ok := r.sessions[key]; !ok {
		return providersessions.ErrSessionNotFound
	}
	delete(r.sessions, key)
	return nil
}

func (r *handlerMemoryRepo) hasSession(userID, provider string) bool {
	_, ok := r.sessions[userID+"|"+provider]
	return ok
}
