package server

import (
	"context"

	apiv1 "github.com/midia/aione/api/grpc/aione/v1"
	"github.com/midia/aione/internal/services/auth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *authService) Register(ctx context.Context, req *apiv1.RegisterRequest) (*apiv1.AuthResponse, error) {
	svc, err := s.requireAuthService()
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request required")
	}
	input := auth.RegisterInput{
		Email:       req.GetEmail(),
		DisplayName: req.GetDisplayName(),
		Password:    req.GetPassword(),
		Preferences: mapStringToAny(req.GetPreferences()),
		Timezone:    req.GetTimezone(),
		Locale:      req.GetLocale(),
		IP:          clientIPFromContext(ctx),
		UserAgent:   userAgentFromMetadata(ctx),
	}
	resp, err := svc.Register(ctx, input)
	if err != nil {
		return nil, authStatus(err)
	}
	return authResponseToProto(resp), nil
}

func (s *authService) Login(ctx context.Context, req *apiv1.LoginRequest) (*apiv1.AuthResponse, error) {
	svc, err := s.requireAuthService()
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request required")
	}
	input := auth.LoginInput{
		Email:     req.GetEmail(),
		Password:  req.GetPassword(),
		IP:        clientIPFromContext(ctx),
		UserAgent: userAgentFromMetadata(ctx),
	}
	resp, err := svc.Login(ctx, input)
	if err != nil {
		return nil, authStatus(err)
	}
	return authResponseToProto(resp), nil
}

func (s *authService) Refresh(ctx context.Context, req *apiv1.RefreshRequest) (*apiv1.AuthResponse, error) {
	svc, err := s.requireAuthService()
	if err != nil {
		return nil, err
	}
	if req == nil || req.GetRefreshToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "refresh_token required")
	}
	input := auth.RefreshInput{
		RefreshToken: req.GetRefreshToken(),
		IP:           clientIPFromContext(ctx),
		UserAgent:    userAgentFromMetadata(ctx),
	}
	resp, err := svc.Refresh(ctx, input)
	if err != nil {
		return nil, authStatus(err)
	}
	return authResponseToProto(resp), nil
}

func (s *authService) Logout(ctx context.Context, req *apiv1.LogoutRequest) (*apiv1.LogoutResponse, error) {
	svc, err := s.requireAuthService()
	if err != nil {
		return nil, err
	}
	if req == nil || req.GetRefreshToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "refresh_token required")
	}
	if err := svc.Logout(ctx, req.GetRefreshToken()); err != nil {
		return nil, authStatus(err)
	}
	return &apiv1.LogoutResponse{Success: true}, nil
}

func authResponseToProto(resp auth.AuthResponse) *apiv1.AuthResponse {
	return &apiv1.AuthResponse{
		AccessToken:      resp.AccessToken,
		AccessExpiresIn:  int64(resp.AccessTokenExpiresIn.Seconds()),
		RefreshToken:     resp.RefreshToken,
		RefreshExpiresIn: int64(resp.RefreshTokenExpiresIn.Seconds()),
	}
}
