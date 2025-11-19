package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/midia/aione/internal/providers/dto"
	"github.com/midia/aione/internal/services/auth"
	"github.com/midia/aione/internal/services/providersessions"
	"github.com/midia/aione/internal/services/session"
)

func TestSessionMessageRequiresAuth(t *testing.T) {
	handler := NewSessionAPI(testLogger(), &stubSessionAPIService{})
	req := httptest.NewRequest(http.MethodPost, "/session/openai/message", strings.NewReader(`{"prompt":"hi"}`))
	rec := httptest.NewRecorder()
	handler.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestSessionMessageSuccess(t *testing.T) {
	stub := &stubSessionAPIService{
		messageResult: session.Result[dto.TextResp]{
			Provider: "openai",
			Payload:  dto.TextResp{Content: "ok"},
			Session:  providersessions.SessionDetails{ProviderName: "openai", ProviderKey: "sk", TotalTokensUsed: 10},
		},
	}
	handler := NewSessionAPI(testLogger(), stub)
	tokens, err := auth.NewTokenManager("access", "refresh")
	if err != nil {
		t.Fatalf("token manager: %v", err)
	}
	token, _, err := tokens.GenerateAccess("user-1", "user@example.com", time.Minute)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/session/openai/message", strings.NewReader(`{"prompt":"hi"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	auth.AuthMiddleware(tokens)(handler.Handler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if stub.lastMessage.UserID != "user-1" || stub.lastMessage.Provider != "openai" {
		t.Fatalf("unexpected session input: %+v", stub.lastMessage)
	}
	var resp responseEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Session == nil || resp.Session.ProviderName != "openai" {
		t.Fatalf("expected session payload, got %+v", resp.Session)
	}
}

// stubSessionAPIService implements SessionService for tests.
type stubSessionAPIService struct {
	messageResult session.Result[dto.TextResp]
	messageErr    error
	lastMessage   session.MessageInput
}

func (s *stubSessionAPIService) SendMessage(ctx context.Context, input session.MessageInput) (session.Result[dto.TextResp], error) {
	s.lastMessage = input
	if s.messageErr != nil {
		return session.Result[dto.TextResp]{}, s.messageErr
	}
	return s.messageResult, nil
}

func (s *stubSessionAPIService) GenerateImage(ctx context.Context, input session.ImageInput) (session.Result[dto.ImageResp], error) {
	return session.Result[dto.ImageResp]{}, nil
}

func (s *stubSessionAPIService) GenerateVideo(ctx context.Context, input session.VideoInput) (session.Result[dto.VideoResp], error) {
	return session.Result[dto.VideoResp]{}, nil
}

func (s *stubSessionAPIService) TranscribeAudio(ctx context.Context, input session.AudioInput) (session.Result[dto.STTResp], error) {
	return session.Result[dto.STTResp]{}, nil
}
