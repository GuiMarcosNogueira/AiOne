package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type ctxKey string

const claimsKey ctxKey = "auth-claims"

// AuthMiddleware validates the access token and stores claims in context.
func AuthMiddleware(tokens *TokenManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authorization := r.Header.Get("Authorization")
			parts := strings.SplitN(authorization, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				http.Error(w, "missing bearer token", http.StatusUnauthorized)
				return
			}
			claims, err := tokens.ParseAccess(parts[1])
			if err != nil {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), claimsKey, Claims{UserID: claims.UserID, Email: claims.Email})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClaimsFromContext extracts auth claims from the context.
func ClaimsFromContext(ctx context.Context) (Claims, bool) {
	claims, ok := ctx.Value(claimsKey).(Claims)
	return claims, ok
}

// RateLimiter enforces sliding window rate limits backed by Redis.
type RateLimiter struct {
	client *redis.Client
	window time.Duration
	limit  int
}

// NewRateLimiter builds a redis-based rate limiter.
func NewRateLimiter(client *redis.Client, window time.Duration, limit int) (*RateLimiter, error) {
	if client == nil {
		return nil, errors.New("ratelimiter requires redis client")
	}
	if window <= 0 || limit <= 0 {
		return nil, errors.New("invalid rate limit configuration")
	}
	return &RateLimiter{client: client, window: window, limit: limit}, nil
}

// Middleware returns an HTTP middleware applying rate limits by keyFn.
func (r *RateLimiter) Middleware(keyFn func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			key := keyFn(req)
			allowed, err := r.allow(req.Context(), key)
			if err != nil {
				http.Error(w, "rate limiter error", http.StatusInternalServerError)
				return
			}
			if !allowed {
				http.Error(w, "too many requests", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, req)
		})
	}
}

func (r *RateLimiter) allow(ctx context.Context, key string) (bool, error) {
	now := float64(time.Now().UnixNano())
	windowStart := float64(time.Now().Add(-r.window).UnixNano())
	pipe := r.client.TxPipeline()
	pipe.ZRemRangeByScore(ctx, key, "-inf", fmt.Sprintf("%f", windowStart))
	pipe.ZAdd(ctx, key, redis.Z{Score: now, Member: now})
	count := pipe.ZCard(ctx, key)
	pipe.Expire(ctx, key, r.window)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, err
	}
	val, err := count.Result()
	if err != nil {
		return false, err
	}
	return val <= int64(r.limit), nil
}
