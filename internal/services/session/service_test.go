package session

import (
	"context"
	"errors"
	"fmt"
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
	const sessionID = "session-1"
	sessions.sessions[sessionID] = providersessions.SessionDetails{ID: sessionID, ProviderName: "openai"}
	sessions.owners[sessionID] = "user-1"
	historyStore := &stubHistoryStore{entries: []history.Entry{{Role: "system", Message: "context"}}}

	svc, err := NewService(nil, provider, sessions, historyStore, nil)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	res, err := svc.SendMessage(context.Background(), MessageInput{
		SessionInput: SessionInput{UserID: "user-1", Provider: "openai", SessionID: sessionID},
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
	svc, err := NewService(nil, provider, sessions, historyStore, nil)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	_, err = svc.SendMessage(context.Background(), MessageInput{
		SessionInput: SessionInput{UserID: "user-1", Provider: "openai", SessionTitle: "First"},
		Prompt:       "first",
	})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	if len(sessions.createCalls) != 1 {
		t.Fatalf("expected session auto create, got %d", len(sessions.createCalls))
	}
}

func TestEditImageRequiresSource(t *testing.T) {
	provider := &stubProviderClient{caps: providers.Capabilities{Limits: providers.Limits{MaxTextTokens: 32}}}
	sessions := newStubSessionStore()
	historyStore := &stubHistoryStore{}
	svc, err := NewService(nil, provider, sessions, historyStore, nil)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	_, err = svc.EditImage(context.Background(), ImageEditInput{
		SessionInput: SessionInput{UserID: "user-1", Provider: "openai", SessionTitle: "img"},
		Prompt:       "edit this",
	})
	if !errors.Is(err, ErrImageSourceRequired) {
		t.Fatalf("expected ErrImageSourceRequired, got %v", err)
	}
}

func TestEditImagePersistsHistoryAndUsage(t *testing.T) {
	provider := &stubProviderClient{
		caps:            providers.Capabilities{Limits: providers.Limits{MaxTextTokens: 80}},
		imageEditResult: providermanager.Result[dto.ImageResp]{Provider: "openai", Data: dto.ImageResp{URL: "https://example.com/edit.png"}},
	}
	sessions := newStubSessionStore()
	const sessionID = "session-edit"
	sessions.sessions[sessionID] = providersessions.SessionDetails{ID: sessionID, ProviderName: "openai"}
	sessions.owners[sessionID] = "user-1"
	historyStore := &stubHistoryStore{}
	svc, err := NewService(nil, provider, sessions, historyStore, nil)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	res, err := svc.EditImage(context.Background(), ImageEditInput{
		SessionInput: SessionInput{UserID: "user-1", Provider: "openai", SessionID: sessionID},
		Prompt:       "refine",
		ImageBase64:  "ZGF0YQ==",
	})
	if err != nil {
		t.Fatalf("edit image: %v", err)
	}
	if res.Payload.URL == "" || res.Provider == "" {
		t.Fatalf("unexpected response: %+v", res)
	}
	if provider.lastImageEditReq.ImageBase64 == "" {
		t.Fatalf("expected image payload to be forwarded")
	}
	if sessions.lastUsage.SessionID != sessionID || sessions.lastUsage.TokensDelta == 0 {
		t.Fatalf("usage not recorded: %+v", sessions.lastUsage)
	}
	if len(historyStore.saved) < 2 {
		t.Fatalf("expected prompt and reply saved, got %d", len(historyStore.saved))
	}
}

func TestGenerateImageNormalizesPayload(t *testing.T) {
	provider := &stubProviderClient{
		caps:        providers.Capabilities{Limits: providers.Limits{MaxTextTokens: 50}},
		imageResult: providermanager.Result[dto.ImageResp]{Provider: "openai", Data: dto.ImageResp{URL: "data:image/png;base64,AAAA"}},
	}
	sessions := newStubSessionStore()
	const sessionID = "session-img"
	sessions.sessions[sessionID] = providersessions.SessionDetails{ID: sessionID, ProviderName: "openai"}
	sessions.owners[sessionID] = "user-1"
	assets := &stubAssetManager{imageResp: dto.ImageResp{URL: "https://cdn.local/image.png"}}
	svc, err := NewService(nil, provider, sessions, nil, assets)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	res, err := svc.GenerateImage(context.Background(), ImageInput{
		SessionInput: SessionInput{UserID: "user-1", Provider: "openai", SessionID: sessionID},
		Prompt:       "paint",
	})
	if err != nil {
		t.Fatalf("generate image: %v", err)
	}
	if res.Payload.URL != "https://cdn.local/image.png" {
		t.Fatalf("expected normalized url, got %s", res.Payload.URL)
	}
	if assets.lastImage.URL != "data:image/png;base64,AAAA" {
		t.Fatalf("expected asset service to see original payload")
	}
}

func TestGenerateImageNormalizationError(t *testing.T) {
	provider := &stubProviderClient{
		caps:        providers.Capabilities{Limits: providers.Limits{MaxTextTokens: 10}},
		imageResult: providermanager.Result[dto.ImageResp]{Provider: "openai", Data: dto.ImageResp{URL: "data:image/png;base64,AAAA"}},
	}
	sessions := newStubSessionStore()
	sessions.sessions["s1"] = providersessions.SessionDetails{ID: "s1", ProviderName: "openai"}
	sessions.owners["s1"] = "user-1"
	assets := &stubAssetManager{imageErr: errors.New("disk full")}
	svc, err := NewService(nil, provider, sessions, nil, assets)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	_, err = svc.GenerateImage(context.Background(), ImageInput{
		SessionInput: SessionInput{UserID: "user-1", Provider: "openai", SessionID: "s1"},
		Prompt:       "paint",
	})
	if err == nil || err.Error() != "disk full" {
		t.Fatalf("expected normalization error, got %v", err)
	}
}

// --- test doubles ---

type stubProviderClient struct {
	caps             providers.Capabilities
	textResult       providermanager.Result[dto.TextResp]
	lastTextReq      dto.TextReq
	imageResult      providermanager.Result[dto.ImageResp]
	imageErr         error
	lastImageReq     dto.ImageReq
	imageEditResult  providermanager.Result[dto.ImageResp]
	imageEditErr     error
	lastImageEditReq dto.ImageEditReq
}

func (s *stubProviderClient) CapabilitiesFor(name string) (providers.Capabilities, bool) {
	return s.caps, true
}

func (s *stubProviderClient) TextGenerate(ctx context.Context, req dto.TextReq) (providermanager.Result[dto.TextResp], error) {
	s.lastTextReq = req
	return s.textResult, nil
}

func (s *stubProviderClient) ImageGenerate(ctx context.Context, req dto.ImageReq) (providermanager.Result[dto.ImageResp], error) {
	s.lastImageReq = req
	if s.imageErr != nil {
		return providermanager.Result[dto.ImageResp]{}, s.imageErr
	}
	if s.imageResult.Provider == "" {
		s.imageResult.Provider = "openai"
	}
	if s.imageResult.Data.URL == "" {
		s.imageResult.Data.URL = "https://example.com/generated.png"
	}
	return s.imageResult, nil
}

func (s *stubProviderClient) ImageEdit(ctx context.Context, req dto.ImageEditReq) (providermanager.Result[dto.ImageResp], error) {
	s.lastImageEditReq = req
	if s.imageEditErr != nil {
		return providermanager.Result[dto.ImageResp]{}, s.imageEditErr
	}
	if s.imageEditResult.Provider == "" {
		s.imageEditResult.Provider = "openai"
	}
	if s.imageEditResult.Data.URL == "" {
		s.imageEditResult.Data.URL = "https://example.com/image.png"
	}
	return s.imageEditResult, nil
}

func (s *stubProviderClient) VideoGenerate(ctx context.Context, req dto.VideoReq) (providermanager.Result[dto.VideoResp], error) {
	return providermanager.Result[dto.VideoResp]{}, errors.New("not implemented")
}

func (s *stubProviderClient) SpeechToText(ctx context.Context, req dto.STTReq) (providermanager.Result[dto.STTResp], error) {
	return providermanager.Result[dto.STTResp]{}, errors.New("not implemented")
}

type stubSessionStore struct {
	sessions    map[string]providersessions.SessionDetails
	owners      map[string]string
	createCalls []providersessions.CreateSessionInput
	lastUsage   providersessions.UsageInput
}

func newStubSessionStore() *stubSessionStore {
	return &stubSessionStore{sessions: make(map[string]providersessions.SessionDetails), owners: make(map[string]string)}
}

func (s *stubSessionStore) CreateSession(ctx context.Context, input providersessions.CreateSessionInput) (providersessions.SessionDetails, error) {
	s.createCalls = append(s.createCalls, input)
	id := fmt.Sprintf("session-%d", len(s.sessions)+1)
	details := providersessions.SessionDetails{
		ID:              id,
		ProviderName:    input.ProviderName,
		Title:           input.Title,
		Metadata:        input.Metadata,
		ExpiresAt:       input.ExpiresAt,
		LastInteraction: time.Now().UTC(),
	}
	s.sessions[id] = details
	s.owners[id] = input.UserID
	return details, nil
}

func (s *stubSessionStore) GetSession(ctx context.Context, userID, sessionID string) (providersessions.SessionDetails, error) {
	sess, ok := s.sessions[sessionID]
	if !ok || s.owners[sessionID] != userID {
		return providersessions.SessionDetails{}, providersessions.ErrSessionNotFound
	}
	return sess, nil
}

func (s *stubSessionStore) RecordUsage(ctx context.Context, input providersessions.UsageInput) (providersessions.SessionDetails, error) {
	s.lastUsage = input
	sess, ok := s.sessions[input.SessionID]
	if !ok || s.owners[input.SessionID] != input.UserID {
		return providersessions.SessionDetails{}, providersessions.ErrSessionNotFound
	}
	sess.TotalTokensUsed += input.TokensDelta
	sess.LastInteraction = input.LastInteraction
	s.sessions[input.SessionID] = sess
	return sess, nil
}

type stubHistoryStore struct {
	entries []history.Entry
	saved   []history.SaveMessageInput
}

func (s *stubHistoryStore) TruncateSessionHistoryToTokenLimit(ctx context.Context, userID, sessionID string, limit int) (int, error) {
	return 0, nil
}

func (s *stubHistoryStore) ListSessionHistory(ctx context.Context, userID, sessionID string) ([]history.Entry, error) {
	return append([]history.Entry(nil), s.entries...), nil
}

func (s *stubHistoryStore) SaveMessage(ctx context.Context, input history.SaveMessageInput) (history.Entry, error) {
	s.saved = append(s.saved, input)
	return history.Entry{ID: int64(len(s.saved)), UserID: input.UserID, ProviderName: input.ProviderName, Role: input.Role, Message: input.Message}, nil
}

type stubAssetManager struct {
	lastImage dto.ImageResp
	imageResp dto.ImageResp
	imageErr  error
	lastVideo dto.VideoResp
	videoResp dto.VideoResp
	videoErr  error
}

func (s *stubAssetManager) NormalizeImage(ctx context.Context, image dto.ImageResp) (dto.ImageResp, error) {
	s.lastImage = image
	if s.imageErr != nil {
		return image, s.imageErr
	}
	if s.imageResp.URL != "" {
		return s.imageResp, nil
	}
	return image, nil
}

func (s *stubAssetManager) NormalizeVideo(ctx context.Context, video dto.VideoResp) (dto.VideoResp, error) {
	s.lastVideo = video
	if s.videoErr != nil {
		return video, s.videoErr
	}
	if s.videoResp.URL != "" {
		return s.videoResp, nil
	}
	return video, nil
}
