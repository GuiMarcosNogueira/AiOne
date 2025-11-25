package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenManager issues JWT access and refresh tokens.
type TokenManager struct {
	accessSecret  []byte
	refreshSecret []byte
}

// NewTokenManager builds a token manager with secrets.
func NewTokenManager(accessSecret, refreshSecret string) (*TokenManager, error) {
	if accessSecret == "" || refreshSecret == "" {
		return nil, errors.New("missing jwt secrets")
	}
	return &TokenManager{accessSecret: []byte(accessSecret), refreshSecret: []byte(refreshSecret)}, nil
}

// AccessClaims describes the access token payload.
type AccessClaims struct {
	UserID string `json:"uid"`
	Email  string `json:"email"`
	jwt.RegisteredClaims
}

// RefreshClaims describes the refresh token payload.
type RefreshClaims struct {
	UserID    string `json:"uid"`
	Email     string `json:"email"`
	SessionID string `json:"sid"`
	jwt.RegisteredClaims
}

// GenerateAccess returns a signed access JWT.
func (m *TokenManager) GenerateAccess(userID, email string, ttl time.Duration) (string, time.Time, error) {
	if m == nil {
		return "", time.Time{}, errors.New("token manager not configured")
	}
	expiresAt := time.Now().Add(ttl)
	claims := AccessClaims{
		UserID: userID,
		Email:  email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.accessSecret)
	return token, expiresAt, err
}

// GenerateRefresh returns a signed refresh JWT.
func (m *TokenManager) GenerateRefresh(userID, email, sessionID string, ttl time.Duration) (string, time.Time, error) {
	if m == nil {
		return "", time.Time{}, errors.New("token manager not configured")
	}
	expiresAt := time.Now().Add(ttl)
	claims := RefreshClaims{
		UserID:    userID,
		Email:     email,
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ID:        sessionID,
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.refreshSecret)
	return token, expiresAt, err
}

// ParseAccess validates an access token.
func (m *TokenManager) ParseAccess(tokenStr string) (AccessClaims, error) {
	var claims AccessClaims
	if m == nil {
		return claims, errors.New("token manager not configured")
	}
	token, err := jwt.ParseWithClaims(tokenStr, &claims, func(token *jwt.Token) (interface{}, error) {
		return m.accessSecret, nil
	})
	if err != nil {
		return claims, err
	}
	if !token.Valid {
		return claims, errors.New("invalid access token")
	}
	return claims, nil
}

// ParseRefresh validates a refresh token.
func (m *TokenManager) ParseRefresh(tokenStr string) (RefreshClaims, error) {
	var claims RefreshClaims
	if m == nil {
		return claims, errors.New("token manager not configured")
	}
	token, err := jwt.ParseWithClaims(tokenStr, &claims, func(token *jwt.Token) (interface{}, error) {
		return m.refreshSecret, nil
	})
	if err != nil {
		return claims, err
	}
	if !token.Valid {
		return claims, errors.New("invalid refresh token")
	}
	return claims, nil
}
