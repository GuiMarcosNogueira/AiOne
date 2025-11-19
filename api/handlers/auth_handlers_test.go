package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/midia/aione/internal/services/auth"
	"github.com/midia/aione/internal/services/users"
)

func TestAuthRegisterSuccess(t *testing.T) {
	h := newAuthTestHarness(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(`{"email":"user@example.com","display_name":"Tester","password":"very-strong"}`))
	req.RemoteAddr = "203.0.113.5:1234"
	rec := httptest.NewRecorder()

	h.api.RegisterHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		Data struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.AccessToken == "" || resp.Data.RefreshToken == "" {
		t.Fatalf("expected tokens in response: %+v", resp.Data)
	}
}

func TestAuthRegisterConflict(t *testing.T) {
	h := newAuthTestHarness(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(`{"email":"user@example.com","display_name":"Tester","password":"very-strong"}`))
	req.RemoteAddr = "203.0.113.5:1234"
	rec := httptest.NewRecorder()
	h.api.RegisterHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected first register 200, got %d", rec.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(`{"email":"user@example.com","display_name":"Tester","password":"very-strong"}`))
	rec2 := httptest.NewRecorder()
	h.api.RegisterHandler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusConflict {
		t.Fatalf("expected conflict status, got %d", rec2.Code)
	}
}

func TestAuthLoginRateLimited(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	limiter, err := auth.NewRateLimiter(client, time.Second, 1)
	if err != nil {
		t.Fatalf("rate limiter: %v", err)
	}

	h := newAuthTestHarness(t, limiter)
	_, err = h.service.Register(context.Background(), auth.RegisterInput{
		Email:       "user@example.com",
		DisplayName: "Tester",
		Password:    "very-strong",
		IP:          "203.0.113.5",
		UserAgent:   "tests",
	})
	if err != nil {
		t.Fatalf("seed register: %v", err)
	}

	makeReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"email":"user@example.com","password":"very-strong"}`))
		req.RemoteAddr = "203.0.113.5:1234"
		return req
	}

	rec := httptest.NewRecorder()
	h.api.LoginHandler().ServeHTTP(rec, makeReq())
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on first login, got %d", rec.Code)
	}

	rec2 := httptest.NewRecorder()
	h.api.LoginHandler().ServeHTTP(rec2, makeReq())
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on rate limited call, got %d", rec2.Code)
	}
	if h.repo.lookupCount != 1 {
		t.Fatalf("expected underlying repo invoked once, got %d", h.repo.lookupCount)
	}
}

func TestAuthLogoutRequiresToken(t *testing.T) {
	h := newAuthTestHarness(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.api.LogoutHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

type authTestHarness struct {
	api      *AuthAPI
	service  *auth.Service
	repo     *testUserRepo
	sessions *testSessionStore
}

func newAuthTestHarness(t *testing.T, limiter *auth.RateLimiter) authTestHarness {
	repo := newTestUserRepo()
	sessions := newTestSessionStore()
	hasher := auth.NewHasher(64*1024, 3, 16, 32, 2)
	tokens, err := auth.NewTokenManager("access-secret", "refresh-secret")
	if err != nil {
		t.Fatalf("token manager: %v", err)
	}
	service, err := auth.NewService(repo, hasher, tokens, sessions, time.Minute, time.Hour)
	if err != nil {
		t.Fatalf("auth service: %v", err)
	}
	return authTestHarness{
		api:      NewAuthAPI(testLogger(), service, limiter),
		service:  service,
		repo:     repo,
		sessions: sessions,
	}
}

type testUserRepo struct {
	store       map[string]users.Aggregate
	lookupCount int
}

func newTestUserRepo() *testUserRepo {
	return &testUserRepo{store: make(map[string]users.Aggregate)}
}

func (r *testUserRepo) Create(ctx context.Context, params users.CreateParams) (users.Aggregate, error) {
	key := strings.ToLower(params.Email)
	if _, exists := r.store[key]; exists {
		return users.Aggregate{}, users.ErrUserExists
	}
	now := time.Now()
	agg := users.Aggregate{
		User: users.User{
			ID:          params.ID,
			Email:       key,
			DisplayName: params.DisplayName,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		Credentials: users.Credentials{
			UserID:       params.ID,
			PasswordHash: params.PasswordHash,
			PasswordAlgo: params.PasswordAlgo,
			CreatedAt:    now,
			UpdatedAt:    now,
			LastRotated:  now,
		},
		Settings: users.Settings{
			UserID:      params.ID,
			Preferences: params.Preferences,
			Timezone:    params.Timezone,
			Locale:      params.Locale,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
	}
	r.store[key] = agg
	return agg, nil
}

func (r *testUserRepo) GetByEmail(ctx context.Context, email string) (users.Aggregate, error) {
	r.lookupCount++
	if agg, ok := r.store[strings.ToLower(email)]; ok {
		return agg, nil
	}
	return users.Aggregate{}, users.ErrUserNotFound
}

func (r *testUserRepo) GetByID(ctx context.Context, id string) (users.Aggregate, error) {
	for _, agg := range r.store {
		if agg.User.ID == id {
			return agg, nil
		}
	}
	return users.Aggregate{}, users.ErrUserNotFound
}

func (r *testUserRepo) UpdateLastLogin(context.Context, string) error { return nil }

type testSessionStore struct {
	sessions map[string]auth.Session
}

func newTestSessionStore() *testSessionStore {
	return &testSessionStore{sessions: make(map[string]auth.Session)}
}

func (s *testSessionStore) Save(ctx context.Context, data auth.Session, ttl time.Duration) error {
	s.sessions[data.SessionID] = data
	return nil
}

func (s *testSessionStore) Get(ctx context.Context, sessionID string) (auth.Session, error) {
	if sess, ok := s.sessions[sessionID]; ok {
		return sess, nil
	}
	return auth.Session{}, auth.ErrSessionNotFound
}

func (s *testSessionStore) Delete(ctx context.Context, sessionID string) error {
	delete(s.sessions, sessionID)
	return nil
}

func (s *testSessionStore) DeleteByToken(ctx context.Context, refreshTokenID string) error {
	for id, sess := range s.sessions {
		if sess.RefreshTokenID == refreshTokenID {
			delete(s.sessions, id)
		}
	}
	return nil
}
