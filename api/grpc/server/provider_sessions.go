package server

import (
	"context"

	apiv1 "github.com/midia/aione/api/grpc/aione/v1"
	"github.com/midia/aione/internal/services/providersessions"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *providerSessionService) CreateSession(ctx context.Context, req *apiv1.ProviderSessionCreateRequest) (*apiv1.ProviderSessionResponse, error) {
	svc, err := s.requireProviderSessions()
	if err != nil {
		return nil, err
	}
	claims, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request required")
	}
	input := providersessions.CreateSessionInput{
		UserID:       claims.UserID,
		ProviderName: req.GetProvider(),
		Title:        req.GetTitle(),
		Metadata:     metadataFromProto(req.GetMetadata()),
		ExpiresAt:    unixPtr(req.GetExpiresAtUnix()),
	}
	session, err := svc.CreateSession(ctx, input)
	if err != nil {
		return nil, providerSessionStatus(err)
	}
	return &apiv1.ProviderSessionResponse{Session: providerSessionToProto(session)}, nil
}

func (s *providerSessionService) ListSessions(ctx context.Context, req *apiv1.ProviderSessionListRequest) (*apiv1.ProviderSessionListResponse, error) {
	svc, err := s.requireProviderSessions()
	if err != nil {
		return nil, err
	}
	claims, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil {
		req = &apiv1.ProviderSessionListRequest{}
	}
	input := providersessions.ListSessionsInput{
		UserID:          claims.UserID,
		ProviderName:    req.GetProvider(),
		Limit:           int(req.GetLimit()),
		IncludeArchived: req.GetIncludeArchived(),
	}
	sessions, err := svc.ListSessions(ctx, input)
	if err != nil {
		return nil, providerSessionStatus(err)
	}
	resp := make([]*apiv1.ProviderSession, 0, len(sessions))
	for _, sess := range sessions {
		resp = append(resp, providerSessionToProto(sess))
	}
	return &apiv1.ProviderSessionListResponse{Sessions: resp}, nil
}

func (s *providerSessionService) GetSession(ctx context.Context, req *apiv1.ProviderSessionGetRequest) (*apiv1.ProviderSessionResponse, error) {
	svc, err := s.requireProviderSessions()
	if err != nil {
		return nil, err
	}
	claims, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil || req.GetSessionId() == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id required")
	}
	sess, err := svc.GetSession(ctx, claims.UserID, req.GetSessionId())
	if err != nil {
		return nil, providerSessionStatus(err)
	}
	return &apiv1.ProviderSessionResponse{Session: providerSessionToProto(sess)}, nil
}

func (s *providerSessionService) ArchiveSession(ctx context.Context, req *apiv1.ProviderSessionArchiveRequest) (*apiv1.ArchiveSessionResponse, error) {
	svc, err := s.requireProviderSessions()
	if err != nil {
		return nil, err
	}
	claims, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil || req.GetSessionId() == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id required")
	}
	if err := svc.ArchiveSession(ctx, claims.UserID, req.GetSessionId()); err != nil {
		return nil, providerSessionStatus(err)
	}
	return &apiv1.ArchiveSessionResponse{Archived: true}, nil
}
