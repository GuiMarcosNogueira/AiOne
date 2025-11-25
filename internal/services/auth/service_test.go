package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/midia/aione/internal/services/users"
)

func TestServiceRegisterLoginRefreshLogout(t *testing.T) {
	repo := newFakeUserRepo()
	sessions := newFakeSessionStore()
	hasher := NewHasher(64*1024, 3, 16, 32, 2)
	tokens, err := NewTokenManager("access-secret", "refresh-secret")
	if err != nil {
		t.Fatalf("token manager: %v", err)
	}
	svc, err := NewService(repo, hasher, tokens, sessions, time.Minute, time.Hour)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := context.Background()
	registerResp, err := svc.Register(ctx, RegisterInput{
		Email:       "user@example.com",
		DisplayName: "Test User",
		Password:    "strongpass",
		Preferences: map[string]any{"theme": "dark"},
		Timezone:    "UTC",
		Locale:      "en",
		IP:          "10.0.0.1",
		UserAgent:   "tests",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if registerResp.AccessToken == "" || registerResp.RefreshToken == "" {
		t.Fatalf("expected tokens in response")
	}

	if err := svc.Logout(ctx, registerResp.RefreshToken); err != nil {
		t.Fatalf("logout after register: %v", err)
	}
	loginResp, err := svc.Login(ctx, LoginInput{Email: "user@example.com", Password: "strongpass", IP: "10.0.0.1", UserAgent: "tests"})
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	refreshResp, err := svc.Refresh(ctx, RefreshInput{RefreshToken: loginResp.RefreshToken, IP: "10.0.0.1", UserAgent: "tests"})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if refreshResp.RefreshToken == loginResp.RefreshToken {
		t.Fatalf("expected rotated refresh token")
	}

	if err := svc.Logout(ctx, refreshResp.RefreshToken); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if len(sessions.sessions) != 0 {
		t.Fatalf("expected sessions cleared, got %d", len(sessions.sessions))
	}

	if _, err := svc.Login(ctx, LoginInput{Email: "user@example.com", Password: "bad", IP: "10.0.0.1", UserAgent: "tests"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected invalid credentials error, got %v", err)
	}
	if _, err := svc.Login(ctx, LoginInput{Email: "missing@example.com", Password: "strongpass", IP: "10.0.0.1", UserAgent: "tests"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected invalid credentials for missing user")
	}
}

type fakeUserRepo struct {
	store map[string]users.Aggregate
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{store: make(map[string]users.Aggregate)}
}

func (f *fakeUserRepo) Create(ctx context.Context, params users.CreateParams) (users.Aggregate, error) {
	key := strings.ToLower(params.Email)
	if _, exists := f.store[key]; exists {
		return users.Aggregate{}, users.ErrUserExists
	}
	agg := users.Aggregate{
		User: users.User{
			ID:          params.ID,
			Email:       key,
			DisplayName: params.DisplayName,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		},
		Credentials: users.Credentials{
			UserID:       params.ID,
			PasswordHash: params.PasswordHash,
			PasswordAlgo: params.PasswordAlgo,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
			LastRotated:  time.Now(),
		},
		Settings: users.Settings{
			UserID:      params.ID,
			Preferences: params.Preferences,
			Timezone:    params.Timezone,
			Locale:      params.Locale,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		},
	}
	f.store[key] = agg
	return agg, nil
}

func (f *fakeUserRepo) GetByEmail(ctx context.Context, email string) (users.Aggregate, error) {
	agg, ok := f.store[strings.ToLower(email)]
	if !ok {
		return users.Aggregate{}, users.ErrUserNotFound
	}
	return agg, nil
}

func (f *fakeUserRepo) GetByID(ctx context.Context, id string) (users.Aggregate, error) {
	for _, agg := range f.store {
		if agg.User.ID == id {
			return agg, nil
		}
	}
	return users.Aggregate{}, users.ErrUserNotFound
}

func (f *fakeUserRepo) UpdateLastLogin(context.Context, string) error { return nil }

type fakeSessionStore struct {
	sessions map[string]Session
}

func newFakeSessionStore() *fakeSessionStore {
	return &fakeSessionStore{sessions: make(map[string]Session)}
}

func (f *fakeSessionStore) Save(ctx context.Context, data Session, ttl time.Duration) error {
	f.sessions[data.SessionID] = data
	return nil
}

func (f *fakeSessionStore) Get(ctx context.Context, sessionID string) (Session, error) {
	sess, ok := f.sessions[sessionID]
	if !ok {
		return Session{}, ErrSessionNotFound
	}
	return sess, nil
}

func (f *fakeSessionStore) Delete(ctx context.Context, sessionID string) error {
	delete(f.sessions, sessionID)
	return nil
}

func (f *fakeSessionStore) DeleteByToken(ctx context.Context, refreshTokenID string) error {
	for id, sess := range f.sessions {
		if sess.RefreshTokenID == refreshTokenID {
			delete(f.sessions, id)
		}
	}
	return nil
}
