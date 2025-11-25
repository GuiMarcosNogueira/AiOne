package server

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	apiv1 "github.com/midia/aione/api/grpc/aione/v1"
	"github.com/midia/aione/internal/providers/dto"
	"github.com/midia/aione/internal/services/auth"
	"github.com/midia/aione/internal/services/history"
	providermanager "github.com/midia/aione/internal/services/provider"
	"github.com/midia/aione/internal/services/providersessions"
	"github.com/midia/aione/internal/services/session"
	"github.com/midia/aione/internal/services/users"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// MediaNormalizer exposes the subset of the asset service needed by gRPC handlers.
type MediaNormalizer interface {
	NormalizeImage(ctx context.Context, image dto.ImageResp) (dto.ImageResp, error)
	NormalizeVideo(ctx context.Context, video dto.VideoResp) (dto.VideoResp, error)
}

// Config wires the dependencies for the gRPC server implementation.
type Config struct {
	Log              *slog.Logger
	Providers        *providermanager.Manager
	Assets           MediaNormalizer
	Auth             *auth.Service
	ProviderSessions *providersessions.Service
	Conversation     *session.Service
	History          *history.Service
}

// Server implements the generated gRPC interfaces backed by the existing services.
type Server struct {
	log              *slog.Logger
	providers        *providermanager.Manager
	assets           MediaNormalizer
	auth             *auth.Service
	providerSessions *providersessions.Service
	conversation     *session.Service
	history          *history.Service
}

type publicService struct {
	*Server
	apiv1.UnimplementedPublicServiceServer
}

type authService struct {
	*Server
	apiv1.UnimplementedAuthServiceServer
}

type providerSessionService struct {
	*Server
	apiv1.UnimplementedProviderSessionServiceServer
}

type conversationService struct {
	*Server
	apiv1.UnimplementedConversationServiceServer
}

type historyService struct {
	*Server
	apiv1.UnimplementedHistoryServiceServer
}

// NewServer builds a new gRPC server façade.
func NewServer(cfg Config) *Server {
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		log:              log,
		providers:        cfg.Providers,
		assets:           cfg.Assets,
		auth:             cfg.Auth,
		providerSessions: cfg.ProviderSessions,
		conversation:     cfg.Conversation,
		history:          cfg.History,
	}
}

// RegisterServices wires every implemented service to the provided gRPC server instance.
func (s *Server) RegisterServices(srv *grpc.Server) {
	if srv == nil {
		return
	}
	apiv1.RegisterPublicServiceServer(srv, &publicService{Server: s})
	if s.auth != nil {
		apiv1.RegisterAuthServiceServer(srv, &authService{Server: s})
	}
	if s.providerSessions != nil {
		apiv1.RegisterProviderSessionServiceServer(srv, &providerSessionService{Server: s})
	}
	if s.conversation != nil {
		apiv1.RegisterConversationServiceServer(srv, &conversationService{Server: s})
	}
	if s.history != nil {
		apiv1.RegisterHistoryServiceServer(srv, &historyService{Server: s})
	}
}

func (s *Server) applyRouting(ctx context.Context, routing *apiv1.ProviderOverride) context.Context {
	if routing == nil {
		return ctx
	}
	if provider := strings.TrimSpace(routing.GetProvider()); provider != "" {
		ctx = providermanager.ContextWithProvider(ctx, provider)
	}
	if strategy := strings.TrimSpace(routing.GetStrategy()); strategy != "" {
		ctx = providermanager.ContextWithStrategy(ctx, providermanager.ParseStrategy(strategy))
	}
	return ctx
}

func overrideProvider(routing *apiv1.ProviderOverride) string {
	if routing == nil {
		return ""
	}
	return strings.TrimSpace(routing.GetProvider())
}

func mediaToDTO(inputs []*apiv1.MediaInput) []dto.MediaInput {
	if len(inputs) == 0 {
		return nil
	}
	out := make([]dto.MediaInput, 0, len(inputs))
	for _, in := range inputs {
		if in == nil {
			continue
		}
		payload := dto.MediaInput{
			Type:     in.GetType(),
			URL:      strings.TrimSpace(in.GetUrl()),
			MIMEType: strings.TrimSpace(in.GetMimeType()),
		}
		if len(in.GetData()) > 0 {
			payload.Data = base64.StdEncoding.EncodeToString(in.GetData())
		}
		out = append(out, payload)
	}
	return out
}

func metadataFromProto(meta *apiv1.SessionMetadata) map[string]any {
	if meta == nil || len(meta.GetFields()) == 0 {
		return nil
	}
	result := make(map[string]any, len(meta.GetFields()))
	for k, v := range meta.GetFields() {
		result[k] = v
	}
	return result
}

func metadataToProto(meta map[string]any) *apiv1.SessionMetadata {
	if len(meta) == 0 {
		return nil
	}
	fields := make(map[string]string, len(meta))
	for k, v := range meta {
		fields[k] = fmt.Sprint(v)
	}
	return &apiv1.SessionMetadata{Fields: fields}
}

func sessionInputFromProto(claims auth.Claims, protoCtx *apiv1.SessionContext) session.SessionInput {
	var meta map[string]any
	var expires *time.Time
	provider := ""
	providerKey := ""
	sessionID := ""
	title := ""
	if protoCtx != nil {
		provider = protoCtx.GetProvider()
		providerKey = protoCtx.GetProviderKey()
		sessionID = protoCtx.GetSessionId()
		title = protoCtx.GetSessionTitle()
		meta = metadataFromProto(protoCtx.GetMetadata())
		expires = unixPtr(protoCtx.GetExpiresAtUnix())
	}
	return session.SessionInput{
		UserID:          claims.UserID,
		Provider:        provider,
		ProviderKey:     providerKey,
		SessionID:       sessionID,
		SessionTitle:    title,
		SessionMetadata: meta,
		ExpiresAt:       expires,
	}
}

func mapStringToAny(values map[string]string) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for k, v := range values {
		out[k] = v
	}
	return out
}

func unixPtr(ts int64) *time.Time {
	if ts <= 0 {
		return nil
	}
	t := time.Unix(ts, 0).UTC()
	return &t
}

func unixSeconds(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.Unix()
}

func providerSessionToProto(details providersessions.SessionDetails) *apiv1.ProviderSession {
	return &apiv1.ProviderSession{
		Id:                  details.ID,
		ProviderName:        details.ProviderName,
		Title:               details.Title,
		LastInteractionUnix: details.LastInteraction.Unix(),
		TotalTokensUsed:     details.TotalTokensUsed,
		ExpiresAtUnix:       unixSeconds(details.ExpiresAt),
		Metadata:            metadataToProto(details.Metadata),
		ArchivedAtUnix:      unixSeconds(details.ArchivedAt),
	}
}

func sessionDetailsToProto(details providersessions.SessionDetails) *apiv1.SessionDetails {
	return &apiv1.SessionDetails{
		Id:                  details.ID,
		ProviderName:        details.ProviderName,
		Title:               details.Title,
		LastInteractionUnix: details.LastInteraction.Unix(),
		TotalTokensUsed:     details.TotalTokensUsed,
		ExpiresAtUnix:       unixSeconds(details.ExpiresAt),
		Metadata:            metadataToProto(details.Metadata),
		ArchivedAtUnix:      unixSeconds(details.ArchivedAt),
	}
}

func sessionEnvelope(provider string, details providersessions.SessionDetails) *apiv1.SessionResponse {
	return &apiv1.SessionResponse{
		Provider: provider,
		Session:  sessionDetailsToProto(details),
	}
}

func historyEntriesToProto(entries []history.Entry) []*apiv1.HistoryEntry {
	result := make([]*apiv1.HistoryEntry, 0, len(entries))
	for _, entry := range entries {
		cloned := entry
		result = append(result, &apiv1.HistoryEntry{
			Id:              cloned.ID,
			UserId:          cloned.UserID,
			SessionId:       cloned.SessionID,
			ProviderName:    cloned.ProviderName,
			Role:            cloned.Role,
			Message:         cloned.Message,
			MediaType:       cloned.MediaType,
			MediaPath:       cloned.MediaPath,
			TokensEstimated: int32(cloned.TokensEstimated),
			CreatedAtUnix:   cloned.CreatedAt.Unix(),
		})
	}
	return result
}

func (s *Server) requireAuthService() (*auth.Service, error) {
	if s.auth == nil {
		return nil, status.Error(codes.Unimplemented, "auth module disabled")
	}
	return s.auth, nil
}

func (s *Server) requireProviderSessions() (*providersessions.Service, error) {
	if s.providerSessions == nil {
		return nil, status.Error(codes.Unimplemented, "provider session module disabled")
	}
	return s.providerSessions, nil
}

func (s *Server) requireConversation() (*session.Service, error) {
	if s.conversation == nil {
		return nil, status.Error(codes.Unimplemented, "conversation module disabled")
	}
	return s.conversation, nil
}

func (s *Server) requireHistory() (*history.Service, error) {
	if s.history == nil {
		return nil, status.Error(codes.Unimplemented, "history module disabled")
	}
	return s.history, nil
}

func (s *Server) requireProviders() (*providermanager.Manager, error) {
	if s.providers == nil {
		return nil, status.Error(codes.Unavailable, "provider manager unavailable")
	}
	return s.providers, nil
}

func (s *Server) requireUser(ctx context.Context) (auth.Claims, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok || strings.TrimSpace(claims.UserID) == "" {
		return auth.Claims{}, status.Error(codes.Unauthenticated, "missing auth context")
	}
	return claims, nil
}

func providerStatus(err error) error {
	switch {
	case errors.Is(err, providermanager.ErrNoProviders):
		return status.Error(codes.Unavailable, err.Error())
	case errors.Is(err, providermanager.ErrCapabilityUnavailable):
		return status.Error(codes.Unimplemented, err.Error())
	case errors.Is(err, providermanager.ErrRateLimited):
		return status.Error(codes.ResourceExhausted, err.Error())
	case errors.Is(err, providermanager.ErrCircuitOpen):
		return status.Error(codes.Unavailable, err.Error())
	case errors.Is(err, providermanager.ErrUnknownProvider):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Errorf(codes.Internal, "provider request failed: %v", err)
	}
}

func providerSessionStatus(err error) error {
	switch {
	case errors.Is(err, providersessions.ErrUserIDRequired),
		errors.Is(err, providersessions.ErrProviderRequired),
		errors.Is(err, providersessions.ErrTitleRequired),
		errors.Is(err, providersessions.ErrSessionIDRequired):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, providersessions.ErrSessionNotFound):
		return status.Error(codes.NotFound, err.Error())
	default:
		return status.Errorf(codes.Internal, "session service error: %v", err)
	}
}

func sessionStatus(err error) error {
	switch {
	case errors.Is(err, session.ErrUserIDRequired),
		errors.Is(err, session.ErrProviderRequired),
		errors.Is(err, session.ErrPromptRequired),
		errors.Is(err, session.ErrAudioURLRequired),
		errors.Is(err, session.ErrProviderKeyRequired),
		errors.Is(err, session.ErrSessionProviderMatch),
		errors.Is(err, session.ErrImageSourceRequired):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Errorf(codes.Internal, "conversation service error: %v", err)
	}
}

func historyStatus(err error) error {
	switch {
	case errors.Is(err, history.ErrUserIDRequired),
		errors.Is(err, history.ErrProviderRequired),
		errors.Is(err, history.ErrSessionIDRequired):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Errorf(codes.Internal, "history service error: %v", err)
	}
}

func authStatus(err error) error {
	switch {
	case errors.Is(err, users.ErrUserExists):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, auth.ErrInvalidCredentials),
		errors.Is(err, auth.ErrSessionMismatch),
		errors.Is(err, auth.ErrSessionNotFound):
		return status.Error(codes.Unauthenticated, err.Error())
	default:
		return status.Errorf(codes.Internal, "auth service error: %v", err)
	}
}

func normalizeIP(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	host := addr.String()
	if tcp, ok := addr.(*net.TCPAddr); ok {
		return tcp.IP.String()
	}
	if udp, ok := addr.(*net.UDPAddr); ok {
		return udp.IP.String()
	}
	return host
}

func userAgentFromMetadata(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	if values := md.Get("user-agent"); len(values) > 0 {
		return values[0]
	}
	return ""
}

func clientIPFromContext(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok {
		return normalizeIP(p.Addr)
	}
	return ""
}

func (s *Server) normalizeImage(ctx context.Context, image dto.ImageResp) (dto.ImageResp, error) {
	if s.assets == nil {
		return image, nil
	}
	return s.assets.NormalizeImage(ctx, image)
}

func (s *Server) normalizeVideo(ctx context.Context, video dto.VideoResp) (dto.VideoResp, error) {
	if s.assets == nil {
		return video, nil
	}
	return s.assets.NormalizeVideo(ctx, video)
}
