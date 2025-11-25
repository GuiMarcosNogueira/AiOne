package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestSessionStoreCRUD(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store, err := NewSessionStore(client, "auth:test")
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	sess := Session{
		UserID:         "user-1",
		SessionID:      "session-1",
		RefreshTokenID: "refresh-1",
		Fingerprint:    "fp",
		ExpiresAt:      time.Now().Add(time.Hour),
	}
	ctx := context.Background()
	if err := store.Save(ctx, sess, time.Minute); err != nil {
		t.Fatalf("save session: %v", err)
	}
	got, err := store.Get(ctx, sess.SessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.UserID != sess.UserID || got.RefreshTokenID != sess.RefreshTokenID {
		t.Fatalf("unexpected session data: %+v", got)
	}
	if err := store.Delete(ctx, sess.SessionID); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, err := store.Get(ctx, sess.SessionID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected session not found, got %v", err)
	}
}

func TestSessionStoreDeleteByToken(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store, err := NewSessionStore(client, "auth:test")
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	ctx := context.Background()
	sessions := []Session{
		{UserID: "user-1", SessionID: "s1", RefreshTokenID: "r1", Fingerprint: "fp1", ExpiresAt: time.Now().Add(time.Hour)},
		{UserID: "user-1", SessionID: "s2", RefreshTokenID: "r2", Fingerprint: "fp2", ExpiresAt: time.Now().Add(time.Hour)},
	}
	for _, sess := range sessions {
		if err := store.Save(ctx, sess, time.Hour); err != nil {
			t.Fatalf("save session %s: %v", sess.SessionID, err)
		}
	}
	if err := store.DeleteByToken(ctx, "r1"); err != nil {
		t.Fatalf("delete by token: %v", err)
	}
	if _, err := store.Get(ctx, "s1"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected s1 removed, got %v", err)
	}
	if _, err := store.Get(ctx, "s2"); err != nil {
		t.Fatalf("expected s2 to remain, got %v", err)
	}
}
