package auth

import (
	"testing"
	"time"
)

func TestTokenManagerAccessAndRefresh(t *testing.T) {
	manager, err := NewTokenManager("access-secret", "refresh-secret")
	if err != nil {
		t.Fatalf("new token manager: %v", err)
	}
	accessToken, _, err := manager.GenerateAccess("user-1", "user@example.com", time.Minute)
	if err != nil {
		t.Fatalf("access token: %v", err)
	}
	claims, err := manager.ParseAccess(accessToken)
	if err != nil {
		t.Fatalf("parse access: %v", err)
	}
	if claims.UserID != "user-1" || claims.Email != "user@example.com" {
		t.Fatalf("unexpected claims: %+v", claims)
	}

	refreshToken, _, err := manager.GenerateRefresh("user-1", "user@example.com", "session-123", time.Minute)
	if err != nil {
		t.Fatalf("refresh token: %v", err)
	}
	rClaims, err := manager.ParseRefresh(refreshToken)
	if err != nil {
		t.Fatalf("parse refresh: %v", err)
	}
	if rClaims.SessionID != "session-123" {
		t.Fatalf("expected session id, got %+v", rClaims)
	}
}
