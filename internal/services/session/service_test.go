package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/midia/aione/internal/providers"
	"github.com/midia/aione/internal/providers/dto"
	"github.com/midia/aione/internal/services/history"
	providermanager "github.com/midia/aione/internal/services/provider"
	"github.com/midia/aione/internal/services/providersessions"
)

func TestSendMessagePersistsHistoryAndUsage(t *testing.T) {
	provider := &stubProviderClient{
		caps:       providers.Capabilities{Limits: providers.Limits{MaxTextTokens: 100}},
		textResult: providermanager.Result[dto.TextResp]{Provider: "openai", Data: dto.TextResp{Content: "assistant reply"}},
	}
	sessions := newStubSessionStore()
	sessions.sessions[sessionKey("user-1", "openai")] = providersessions.SessionDetails{ProviderName: "openai", ProviderKey: "sk-user"}
	historyStore := &stubHistoryStore{entries: []history.Entry{{Role: "system", Message: "context"}}}

	svc, err := NewService(nil, provider, sessions, historyStore)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	res, err := svc.SendMessage(context.Background(), MessageInput{
		SessionInput: SessionInput{UserID: "user-1", Provider: "openai"},
		Prompt:       "hello",
		MaxTokens:    16,
	})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	if res.Provider != "openai" || res.Payload.Content != "assistant reply" {
		t.Fatalf("unexpected response: %+v", res)
	}
	if provider.lastTextReq.Prompt == "" || provider.lastTextReq.Prompt == "hello" {
		t.Fatalf("expected history context to be prefixed, got %q", provider.lastTextReq.Prompt)
	}
	if sessions.lastUsage.TokensDelta == 0 {
		t.Fatalf("expected usage to be recorded")
	}
	if len(historyStore.saved) < 2 {
		t.Fatalf("expected user and assistant turns saved, got %d", len(historyStore.saved))
	}
}

func TestSendMessageAutoCreatesSession(t *testing.T) {
	provider := &stubProviderClient{
		caps:       providers.Capabilities{Limits: providers.Limits{MaxTextTokens: 64}},
		textResult: providermanager.Result[dto.TextResp]{Provider: "openai", Data: dto.TextResp{Content: "ok"}},
	}
	sessions := newStubSessionStore()
	historyStore := &stubHistoryStore{}
	svc, err := NewService(nil, provider, sessions, historyStore)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	_, err = svc.SendMessage(context.Background(), MessageInput{
		SessionInput: SessionInput{UserID: "user-1", Provider: "openai", ProviderKey: "sk-new"},
		Prompt:       "first",
	})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	if len(sessions.setCalls) != 1 {
		t.Fatalf("expected session auto create, got %d", len(sessions.setCalls))
	}
}

func TestSendMessageRequiresProviderKeyWhenMissingSession(t *testing.T) {
	provider := &stubProviderClient{caps: providers.Capabilities{Limits: providers.Limits{MaxTextTokens: 32}}}
	sessions := newStubSessionStore()
	historyStore := &stubHistoryStore{}
	svc, err := NewService(nil, provider, sessions, historyStore)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	_, err = svc.SendMessage(context.Background(), MessageInput{
		SessionInput: SessionInput{UserID: "user-1", Provider: "openai"},
		Prompt:       "hello",
	})
	if !errors.Is(err, ErrProviderKeyRequired) {
		t.Fatalf("expected provider key error, got %v", err)
	}
}

// --- test doubles ---

type stubProviderClient struct {
	caps        providers.Capabilities
	textResult  providermanager.Result[dto.TextResp]
	lastTextReq dto.TextReq
}

func (s *stubProviderClient) CapabilitiesFor(name string) (providers.Capabilities, bool) {
	return s.caps, true
}

func (s *stubProviderClient) TextGenerate(ctx context.Context, req dto.TextReq) (providermanager.Result[dto.TextResp], error) {
	s.lastTextReq = req
	return s.textResult, nil
}

func (s *stubProviderClient) ImageGenerate(ctx context.Context, req dto.ImageReq) (providermanager.Result[dto.ImageResp], error) {
	return providermanager.Result[dto.ImageResp]{}, errors.New("not implemented")
}

func (s *stubProviderClient) VideoGenerate(ctx context.Context, req dto.VideoReq) (providermanager.Result[dto.VideoResp], error) {
	return providermanager.Result[dto.VideoResp]{}, errors.New("not implemented")
}

func (s *stubProviderClient) SpeechToText(ctx context.Context, req dto.STTReq) (providermanager.Result[dto.STTResp], error) {
	return providermanager.Result[dto.STTResp]{}, errors.New("not implemented")
}

type stubSessionStore struct {
	sessions  map[string]providersessions.SessionDetails
	setCalls  []providersessions.SetKeyInput
	lastUsage providersessions.UsageInput
}

func newStubSessionStore() *stubSessionStore {
	return &stubSessionStore{sessions: make(map[string]providersessions.SessionDetails)}
}

func sessionKey(user, provider string) string {
	return user + "|" + provider
}

func (s *stubSessionStore) GetSession(ctx context.Context, userID, provider string) (providersessions.SessionDetails, error) {
	if sess, ok := s.sessions[sessionKey(userID, provider)]; ok {
		return sess, nil
	}
	return providersessions.SessionDetails{}, providersessions.ErrSessionNotFound
}

func (s *stubSessionStore) SetProviderKey(ctx context.Context, input providersessions.SetKeyInput) (providersessions.SessionDetails, error) {
	details := providersessions.SessionDetails{
		ProviderName:    input.ProviderName,
		ProviderKey:     input.ProviderKey,
		Metadata:        input.Metadata,
		ExpiresAt:       input.ExpiresAt,
		LastInteraction: time.Now().UTC(),
	}
	s.sessions[sessionKey(input.UserID, input.ProviderName)] = details
	s.setCalls = append(s.setCalls, input)
	return details, nil
}

func (s *stubSessionStore) RecordUsage(ctx context.Context, input providersessions.UsageInput) (providersessions.SessionDetails, error) {
	s.lastUsage = input
	details, ok := s.sessions[sessionKey(input.UserID, input.ProviderName)]
	if !ok {
		return providersessions.SessionDetails{}, providersessions.ErrSessionNotFound
	}
	details.TotalTokensUsed += input.TokensDelta
	details.LastInteraction = input.LastInteraction
	s.sessions[sessionKey(input.UserID, input.ProviderName)] = details
	return details, nil
}

type stubHistoryStore struct {
	entries []history.Entry
	saved   []history.SaveMessageInput
}

func (s *stubHistoryStore) TruncateHistoryToTokenLimit(ctx context.Context, userID, provider string, limit int) (int, error) {
	return 0, nil
}

func (s *stubHistoryStore) ListHistory(ctx context.Context, userID, provider string) ([]history.Entry, error) {
	return append([]history.Entry(nil), s.entries...), nil
}

func (s *stubHistoryStore) SaveMessage(ctx context.Context, input history.SaveMessageInput) (history.Entry, error) {
	s.saved = append(s.saved, input)
	return history.Entry{ID: int64(len(s.saved)), UserID: input.UserID, ProviderName: input.ProviderName, Role: input.Role, Message: input.Message}, nil
}
