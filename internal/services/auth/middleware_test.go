package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestAuthMiddlewareInjectsClaims(t *testing.T) {
	manager, err := NewTokenManager("access", "refresh")
	if err != nil {
		t.Fatalf("token manager: %v", err)
	}
	accessToken, _, err := manager.GenerateAccess("user-1", "user@example.com", time.Minute)
	if err != nil {
		t.Fatalf("generate access: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	rec := httptest.NewRecorder()

	called := false
	handler := AuthMiddleware(manager)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := ClaimsFromContext(r.Context())
		if !ok {
			t.Fatalf("claims missing")
		}
		if claims.UserID != "user-1" || claims.Email != "user@example.com" {
			t.Fatalf("unexpected claims: %+v", claims)
		}
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	handler.ServeHTTP(rec, req)
	if !called {
		t.Fatalf("handler not invoked")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestAuthMiddlewareRejectsInvalid(t *testing.T) {
	manager, err := NewTokenManager("access", "refresh")
	if err != nil {
		t.Fatalf("token manager: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	rec := httptest.NewRecorder()

	handler := AuthMiddleware(manager)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestRateLimiterMiddleware(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	limiter, err := NewRateLimiter(client, time.Second, 2)
	if err != nil {
		t.Fatalf("rate limiter: %v", err)
	}

	called := 0
	handler := limiter.Middleware(func(r *http.Request) string { return "1.1.1.1" })(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on second call, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after limit, got %d", rec.Code)
	}
	if called != 2 {
		t.Fatalf("expected handler executed twice, got %d", called)
	}
}
