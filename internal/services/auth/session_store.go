package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Session encapsulates active refresh token metadata.
type Session struct {
	UserID         string    `json:"user_id"`
	SessionID      string    `json:"session_id"`
	RefreshTokenID string    `json:"refresh_token_id"`
	Fingerprint    string    `json:"fingerprint"`
	ExpiresAt      time.Time `json:"expires_at"`
}

// SessionRepository abstracts session persistence.
type SessionRepository interface {
	Save(ctx context.Context, data Session, ttl time.Duration) error
	Get(ctx context.Context, sessionID string) (Session, error)
	Delete(ctx context.Context, sessionID string) error
	DeleteByToken(ctx context.Context, refreshTokenID string) error
}

// SessionStore persists active sessions in Redis.
type SessionStore struct {
	client *redis.Client
	prefix string
}

var _ SessionRepository = (*SessionStore)(nil)

var (
	// ErrSessionNotFound indicates missing session entry.
	ErrSessionNotFound = errors.New("session not found")
)

// NewSessionStore builds a session store.
func NewSessionStore(client *redis.Client, prefix string) (*SessionStore, error) {
	if client == nil {
		return nil, errors.New("session store requires redis client")
	}
	if prefix == "" {
		prefix = "auth:session"
	}
	return &SessionStore{client: client, prefix: prefix}, nil
}

func (s *SessionStore) key(sessionID string) string {
	return fmt.Sprintf("%s:%s", s.prefix, sessionID)
}

// Save persists a session envelope with TTL.
func (s *SessionStore) Save(ctx context.Context, data Session, ttl time.Duration) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, s.key(data.SessionID), payload, ttl).Err()
}

// Get fetches a session by identifier.
func (s *SessionStore) Get(ctx context.Context, sessionID string) (Session, error) {
	val, err := s.client.Get(ctx, s.key(sessionID)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return Session{}, ErrSessionNotFound
		}
		return Session{}, err
	}
	var session Session
	if err := json.Unmarshal(val, &session); err != nil {
		return Session{}, err
	}
	return session, nil
}

// Delete removes a session entry.
func (s *SessionStore) Delete(ctx context.Context, sessionID string) error {
	return s.client.Del(ctx, s.key(sessionID)).Err()
}

// DeleteByToken removes session by refresh token identifier (scan-based fallback).
func (s *SessionStore) DeleteByToken(ctx context.Context, refreshTokenID string) error {
	iter := s.client.Scan(ctx, 0, s.prefix+":*", 0).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		val, err := s.client.Get(ctx, key).Bytes()
		if err != nil {
			continue
		}
		var session Session
		if err := json.Unmarshal(val, &session); err != nil {
			continue
		}
		if session.RefreshTokenID == refreshTokenID {
			_ = s.client.Del(ctx, key).Err()
		}
	}
	return iter.Err()
}
