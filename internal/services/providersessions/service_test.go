package providersessions

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/midia/aione/pkg/encryption"
)

func TestServiceSetProviderKeyStoresEncryptedSecret(t *testing.T) {
	svc, repo := newTestService(t)
	expires := time.Now().Add(24 * time.Hour).UTC()
	details, err := svc.SetProviderKey(context.Background(), SetKeyInput{
		UserID:       "user-1",
		ProviderName: "openai",
		ProviderKey:  "sk-test",
		Metadata:     map[string]any{"env": "dev"},
		ExpiresAt:    &expires,
	})
	if err != nil {
		t.Fatalf("set provider key: %v", err)
	}
	if details.ProviderKey != "sk-test" {
		t.Fatalf("expected decrypted key, got %q", details.ProviderKey)
	}
	stored, ok := repo.sessions[repoKey("user-1", "openai")]
	if !ok {
		t.Fatalf("session not stored")
	}
	if stored.EncryptionKeyID != svc.enc.ActiveKeyID() {
		t.Fatalf("expected encryption key id to be stored")
	}
	if stored.Metadata["env"] != "dev" {
		t.Fatalf("expected metadata to persist")
	}
}

func TestServiceValidatesInputs(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.SetProviderKey(context.Background(), SetKeyInput{}); !errors.Is(err, ErrUserIDRequired) {
		t.Fatalf("expected user id error, got %v", err)
	}
	if _, err := svc.SetProviderKey(context.Background(), SetKeyInput{UserID: "u"}); !errors.Is(err, ErrProviderRequired) {
		t.Fatalf("expected provider error, got %v", err)
	}
	if _, err := svc.SetProviderKey(context.Background(), SetKeyInput{UserID: "u", ProviderName: "openai"}); !errors.Is(err, ErrProviderKeyRequired) {
		t.Fatalf("expected provider key error, got %v", err)
	}
	if err := svc.ResetSession(context.Background(), "", "openai"); !errors.Is(err, ErrUserIDRequired) {
		t.Fatalf("expected reset user id error")
	}
	if err := svc.ResetSession(context.Background(), "user", ""); !errors.Is(err, ErrProviderRequired) {
		t.Fatalf("expected reset provider error")
	}
}

func TestServiceGetSessionDecrypts(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.SetProviderKey(context.Background(), SetKeyInput{UserID: "user-1", ProviderName: "openai", ProviderKey: "sk-live"})
	if err != nil {
		t.Fatalf("set provider key: %v", err)
	}
	details, err := svc.GetSession(context.Background(), "user-1", "openai")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if details.ProviderKey != "sk-live" {
		t.Fatalf("expected decrypted key, got %s", details.ProviderKey)
	}
}

func TestServiceRecordUsage(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.SetProviderKey(context.Background(), SetKeyInput{UserID: "user-1", ProviderName: "openai", ProviderKey: "sk-usage"})
	if err != nil {
		t.Fatalf("set provider key: %v", err)
	}
	now := time.Now().UTC()
	details, err := svc.RecordUsage(context.Background(), UsageInput{
		UserID:          "user-1",
		ProviderName:    "openai",
		TokensDelta:     123,
		Metadata:        map[string]any{"last_request": "chat"},
		LastInteraction: now,
	})
	if err != nil {
		t.Fatalf("record usage: %v", err)
	}
	if details.TotalTokensUsed != 123 {
		t.Fatalf("expected tokens to increment, got %d", details.TotalTokensUsed)
	}
	if details.Metadata["last_request"] != "chat" {
		t.Fatalf("expected metadata update")
	}
}

func TestServiceResetSession(t *testing.T) {
	svc, repo := newTestService(t)
	_, err := svc.SetProviderKey(context.Background(), SetKeyInput{UserID: "user-1", ProviderName: "openai", ProviderKey: "sk-reset"})
	if err != nil {
		t.Fatalf("set provider key: %v", err)
	}
	if err := svc.ResetSession(context.Background(), "user-1", "openai"); err != nil {
		t.Fatalf("reset session: %v", err)
	}
	if _, ok := repo.sessions[repoKey("user-1", "openai")]; ok {
		t.Fatalf("expected session to be deleted")
	}
}

func newTestService(t *testing.T) (*Service, *memoryRepository) {
	t.Helper()
	repo := newMemoryRepository()
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	enc, err := encryption.NewManager("primary", map[string]string{"primary": key})
	if err != nil {
		t.Fatalf("encryption manager: %v", err)
	}
	svc, err := NewService(repo, enc)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return svc, repo
}

type memoryRepository struct {
	sessions  map[string]Session
	upsertErr error
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{sessions: map[string]Session{}}
}

func repoKey(userID, provider string) string {
	return userID + "|" + provider
}

func (m *memoryRepository) Upsert(ctx context.Context, params UpsertParams) (Session, error) {
	if m.upsertErr != nil {
		return Session{}, m.upsertErr
	}
	key := repoKey(params.UserID, params.ProviderName)
	now := params.LastInteraction
	if now.IsZero() {
		now = time.Now().UTC()
	}
	sess := Session{
		UserID:          params.UserID,
		ProviderName:    params.ProviderName,
		EncryptedKey:    append([]byte(nil), params.EncryptedKey...),
		EncryptionKeyID: params.EncryptionKeyID,
		LastInteraction: now,
		Metadata:        cloneMetadata(params.Metadata),
		ExpiresAt:       params.ExpiresAt,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if existing, ok := m.sessions[key]; ok {
		sess.TotalTokensUsed = existing.TotalTokensUsed
		sess.CreatedAt = existing.CreatedAt
	}
	m.sessions[key] = sess
	return sess, nil
}

func (m *memoryRepository) Get(ctx context.Context, userID, provider string) (Session, error) {
	key := repoKey(userID, provider)
	sess, ok := m.sessions[key]
	if !ok {
		return Session{}, ErrSessionNotFound
	}
	return sess, nil
}

func (m *memoryRepository) UpdateUsage(ctx context.Context, params UsageUpdateParams) (Session, error) {
	key := repoKey(params.UserID, params.ProviderName)
	sess, ok := m.sessions[key]
	if !ok {
		return Session{}, ErrSessionNotFound
	}
	sess.TotalTokensUsed += params.TokensDelta
	if !params.LastInteraction.IsZero() {
		sess.LastInteraction = params.LastInteraction
	}
	if params.Metadata != nil {
		sess.Metadata = cloneMetadata(params.Metadata)
	}
	if params.ExpiresAt != nil {
		sess.ExpiresAt = params.ExpiresAt
	}
	sess.UpdatedAt = time.Now().UTC()
	m.sessions[key] = sess
	return sess, nil
}

func (m *memoryRepository) Delete(ctx context.Context, userID, provider string) error {
	key := repoKey(userID, provider)
	if _, ok := m.sessions[key]; !ok {
		return ErrSessionNotFound
	}
	delete(m.sessions, key)
	return nil
}

func cloneMetadata(meta map[string]any) map[string]any {
	if meta == nil {
		return map[string]any{}
	}
	cp := make(map[string]any, len(meta))
	for k, v := range meta {
		cp[k] = v
	}
	return cp
}
