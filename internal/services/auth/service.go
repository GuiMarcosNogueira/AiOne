package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/midia/aione/internal/services/users"
)

// Service handles user registration and authentication flows.
type Service struct {
	repo       users.Repository
	hasher     *Hasher
	tokens     *TokenManager
	sessions   SessionRepository
	accessTTL  time.Duration
	refreshTTL time.Duration
}

var (
	// ErrInvalidCredentials indicates user/password mismatch.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrSessionMismatch indicates fingerprint mismatch during refresh.
	ErrSessionMismatch = errors.New("session fingerprint mismatch")
)

// NewService wires up dependencies for auth flows.
func NewService(repo users.Repository, hasher *Hasher, tokens *TokenManager, sessions SessionRepository, accessTTL, refreshTTL time.Duration) (*Service, error) {
	if repo == nil {
		return nil, errors.New("auth: repository required")
	}
	if hasher == nil || tokens == nil || sessions == nil {
		return nil, errors.New("auth: missing dependencies")
	}
	return &Service{
		repo:       repo,
		hasher:     hasher,
		tokens:     tokens,
		sessions:   sessions,
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
	}, nil
}

// Register creates a new user and returns auth tokens.
func (s *Service) Register(ctx context.Context, input RegisterInput) (AuthResponse, error) {
	if err := validatePassword(input.Password); err != nil {
		return AuthResponse{}, err
	}
	salt, err := s.hasher.GenerateSalt()
	if err != nil {
		return AuthResponse{}, err
	}
	encoded, err := s.hasher.Hash(input.Password, salt)
	if err != nil {
		return AuthResponse{}, err
	}
	userID := uuid.NewString()
	params := users.CreateParams{
		ID:           userID,
		Email:        input.Email,
		DisplayName:  input.DisplayName,
		PasswordHash: encoded,
		PasswordAlgo: "argon2id",
		Preferences:  input.Preferences,
		Timezone:     fallback(input.Timezone, "UTC"),
		Locale:       fallback(input.Locale, "en"),
	}
	agg, err := s.repo.Create(ctx, params)
	if err != nil {
		return AuthResponse{}, err
	}
	return s.issueTokens(ctx, agg.User.ID, agg.User.Email, fingerprint(input.IP, input.UserAgent))
}

// Login authenticates a user with credentials.
func (s *Service) Login(ctx context.Context, input LoginInput) (AuthResponse, error) {
	agg, err := s.repo.GetByEmail(ctx, input.Email)
	if err != nil {
		if errors.Is(err, users.ErrUserNotFound) {
			return AuthResponse{}, ErrInvalidCredentials
		}
		return AuthResponse{}, err
	}
	ok, err := s.hasher.Verify(input.Password, agg.Credentials.PasswordHash)
	if err != nil || !ok {
		return AuthResponse{}, ErrInvalidCredentials
	}
	_ = s.repo.UpdateLastLogin(ctx, agg.User.ID)
	return s.issueTokens(ctx, agg.User.ID, agg.User.Email, fingerprint(input.IP, input.UserAgent))
}

// Refresh exchanges a refresh token for new tokens.
func (s *Service) Refresh(ctx context.Context, input RefreshInput) (AuthResponse, error) {
	claims, err := s.tokens.ParseRefresh(input.RefreshToken)
	if err != nil {
		return AuthResponse{}, err
	}
	session, err := s.sessions.Get(ctx, claims.SessionID)
	if err != nil {
		return AuthResponse{}, err
	}
	finger := fingerprint(input.IP, input.UserAgent)
	if session.Fingerprint != finger {
		return AuthResponse{}, ErrSessionMismatch
	}
	// rotate session
	if err := s.sessions.Delete(ctx, session.SessionID); err != nil {
		return AuthResponse{}, err
	}
	return s.issueTokens(ctx, claims.UserID, claims.Email, finger)
}

// Logout revokes the refresh token/session.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	claims, err := s.tokens.ParseRefresh(refreshToken)
	if err != nil {
		return err
	}
	return s.sessions.Delete(ctx, claims.SessionID)
}

func (s *Service) issueTokens(ctx context.Context, userID, email, fingerprint string) (AuthResponse, error) {
	sessionID := uuid.NewString()
	access, _, err := s.tokens.GenerateAccess(userID, email, s.accessTTL)
	if err != nil {
		return AuthResponse{}, err
	}
	refresh, refreshExp, err := s.tokens.GenerateRefresh(userID, email, sessionID, s.refreshTTL)
	if err != nil {
		return AuthResponse{}, err
	}
	session := Session{
		UserID:         userID,
		SessionID:      sessionID,
		RefreshTokenID: sessionID,
		Fingerprint:    fingerprint,
		ExpiresAt:      refreshExp,
	}
	if err := s.sessions.Save(ctx, session, s.refreshTTL); err != nil {
		return AuthResponse{}, err
	}
	return AuthResponse{
		AccessToken:           access,
		AccessTokenExpiresIn:  s.accessTTL,
		RefreshToken:          refresh,
		RefreshTokenExpiresIn: s.refreshTTL,
	}, nil
}

func fingerprint(ip, ua string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(ip) + "|" + strings.TrimSpace(ua)))
	return hex.EncodeToString(sum[:])
}

func fallback(value, def string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return def
	}
	return value
}

func validatePassword(password string) error {
	if len(password) < 8 {
		return fmt.Errorf("password must be at least %d characters", 8)
	}
	return nil
}
