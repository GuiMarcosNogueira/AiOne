package redisclient

import (
	"context"
	"errors"

	"github.com/redis/go-redis/v9"
)

// Config maps Redis connection information.
type Config struct {
	Addr     string
	Password string
	DB       int
}

// Connect instantiates a go-redis client and validates connectivity.
func Connect(cfg Config) (*redis.Client, error) {
	if cfg.Addr == "" {
		return nil, errors.New("redis: missing address")
	}
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
	if err := client.Ping(context.Background()).Err(); err != nil {
		client.Close()
		return nil, err
	}
	return client, nil
}
