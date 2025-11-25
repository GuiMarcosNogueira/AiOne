package server

import (
	"context"

	apiv1 "github.com/midia/aione/api/grpc/aione/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *historyService) GetHistory(ctx context.Context, req *apiv1.HistoryRequest) (*apiv1.HistoryResponse, error) {
	svc, err := s.requireHistory()
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
	entries, err := svc.ListSessionHistory(ctx, claims.UserID, req.GetSessionId())
	if err != nil {
		return nil, historyStatus(err)
	}
	return &apiv1.HistoryResponse{Entries: historyEntriesToProto(entries)}, nil
}

func (s *historyService) DeleteHistory(ctx context.Context, req *apiv1.HistoryRequest) (*apiv1.DeleteHistoryResponse, error) {
	svc, err := s.requireHistory()
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
	if err := svc.DeleteSessionHistory(ctx, claims.UserID, req.GetSessionId()); err != nil {
		return nil, historyStatus(err)
	}
	return &apiv1.DeleteHistoryResponse{Deleted: true}, nil
}
