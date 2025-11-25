package server

import (
	"context"

	apiv1 "github.com/midia/aione/api/grpc/aione/v1"
	"github.com/midia/aione/internal/services/session"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *conversationService) SendMessage(ctx context.Context, req *apiv1.SessionMessageRequest) (*apiv1.SessionResponse, error) {
	svc, err := s.requireConversation()
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
	input := session.MessageInput{
		SessionInput: sessionInputFromProto(claims, req.GetSession()),
		Prompt:       req.GetPrompt(),
		MaxTokens:    int(req.GetMaxTokens()),
		Temperature:  req.GetTemperature(),
		Media:        mediaToDTO(req.GetMedia()),
	}
	result, err := svc.SendMessage(ctx, input)
	if err != nil {
		return nil, sessionStatus(err)
	}
	resp := sessionEnvelope(result.Provider, result.Session)
	resp.Payload = &apiv1.SessionResponse_Text{Text: result.Payload.Content}
	return resp, nil
}

func (s *conversationService) GenerateImage(ctx context.Context, req *apiv1.SessionImageRequest) (*apiv1.SessionResponse, error) {
	svc, err := s.requireConversation()
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
	input := session.ImageInput{
		SessionInput: sessionInputFromProto(claims, req.GetSession()),
		Prompt:       req.GetPrompt(),
		Size:         req.GetSize(),
		Media:        mediaToDTO(req.GetMedia()),
	}
	result, err := svc.GenerateImage(ctx, input)
	if err != nil {
		return nil, sessionStatus(err)
	}
	resp := sessionEnvelope(result.Provider, result.Session)
	resp.Payload = &apiv1.SessionResponse_Image{Image: &apiv1.ImageAsset{Url: result.Payload.URL}}
	return resp, nil
}

func (s *conversationService) EditImage(ctx context.Context, req *apiv1.SessionImageEditRequest) (*apiv1.SessionResponse, error) {
	svc, err := s.requireConversation()
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
	input := session.ImageEditInput{
		SessionInput: sessionInputFromProto(claims, req.GetSession()),
		Prompt:       req.GetPrompt(),
		ImageURL:     req.GetImageUrl(),
		ImageBase64:  encodeBytes(req.GetImageData()),
		MaskURL:      req.GetMaskUrl(),
		MaskBase64:   encodeBytes(req.GetMaskData()),
		Size:         req.GetSize(),
		Media:        mediaToDTO(req.GetMedia()),
	}
	result, err := svc.EditImage(ctx, input)
	if err != nil {
		return nil, sessionStatus(err)
	}
	resp := sessionEnvelope(result.Provider, result.Session)
	resp.Payload = &apiv1.SessionResponse_Image{Image: &apiv1.ImageAsset{Url: result.Payload.URL}}
	return resp, nil
}

func (s *conversationService) GenerateVideo(ctx context.Context, req *apiv1.SessionVideoRequest) (*apiv1.SessionResponse, error) {
	svc, err := s.requireConversation()
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
	input := session.VideoInput{
		SessionInput:    sessionInputFromProto(claims, req.GetSession()),
		Prompt:          req.GetPrompt(),
		DurationSeconds: int(req.GetDurationSeconds()),
		Media:           mediaToDTO(req.GetMedia()),
	}
	result, err := svc.GenerateVideo(ctx, input)
	if err != nil {
		return nil, sessionStatus(err)
	}
	resp := sessionEnvelope(result.Provider, result.Session)
	resp.Payload = &apiv1.SessionResponse_Video{Video: &apiv1.VideoAsset{Url: result.Payload.URL}}
	return resp, nil
}

func (s *conversationService) TranscribeAudio(ctx context.Context, req *apiv1.SessionAudioRequest) (*apiv1.SessionResponse, error) {
	svc, err := s.requireConversation()
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
	input := session.AudioInput{
		SessionInput: sessionInputFromProto(claims, req.GetSession()),
		AudioURL:     req.GetAudioUrl(),
		Language:     req.GetLanguage(),
	}
	result, err := svc.TranscribeAudio(ctx, input)
	if err != nil {
		return nil, sessionStatus(err)
	}
	resp := sessionEnvelope(result.Provider, result.Session)
	resp.Payload = &apiv1.SessionResponse_Transcript{Transcript: result.Payload.Transcript}
	return resp, nil
}
