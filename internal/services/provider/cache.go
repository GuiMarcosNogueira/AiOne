package provider

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrCacheMiss is returned when a cache key is missing.
var ErrCacheMiss = errors.New("provider cache miss")

// Cache abstracts the cache backend used by the provider manager.
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
}

// RedisCache wires go-redis to the Cache interface.
type RedisCache struct {
	client *redis.Client
}

// RedisCacheConfig defines connection options for Redis.
type RedisCacheConfig struct {
	Addr     string
	Password string
	DB       int
}

// NewRedisCache instantiates a Redis-backed cache and validates connectivity.
func NewRedisCache(cfg RedisCacheConfig) (*RedisCache, error) {
	if strings.TrimSpace(cfg.Addr) == "" {
		return nil, errors.New("redis: missing address")
	}
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, err
	}
	return &RedisCache{client: client}, nil
}

// Get fetches a cached value.
func (r *RedisCache) Get(ctx context.Context, key string) ([]byte, error) {
	if r == nil || r.client == nil {
		return nil, ErrCacheMiss
	}
	val, err := r.client.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrCacheMiss
		}
		return nil, err
	}
	return val, nil
}

// Set stores a value in Redis.
func (r *RedisCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if r == nil || r.client == nil {
		return errors.New("redis: not configured")
	}
	return r.client.Set(ctx, key, value, ttl).Err()
}
