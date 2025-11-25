package assets

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"log/slog"

	"github.com/midia/aione/internal/providers/dto"
	"github.com/midia/aione/internal/services/storage"
)

// ImageManager normalizes provider image payloads into publicly accessible URLs.
type ImageManager interface {
	NormalizeImage(ctx context.Context, image dto.ImageResp) (dto.ImageResp, error)
}

// Service persists inline media to the configured storage backend and generates public URLs.
var ErrPersistence = errors.New("failed to persist image")

type Service struct {
	log           *slog.Logger
	storage       storage.Storage
	publicBaseURL string
}

// NewService builds an asset service. When storage is nil the service is disabled.
func NewService(log *slog.Logger, storageBackend storage.Storage, publicBaseURL string) *Service {
	if storageBackend == nil {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		log:           log,
		storage:       storageBackend,
		publicBaseURL: strings.TrimRight(publicBaseURL, "/"),
	}
}

// NormalizeImage stores inline data URLs in persistent storage and replaces the payload with a public URL.
func (s *Service) NormalizeImage(ctx context.Context, image dto.ImageResp) (dto.ImageResp, error) {
	if s == nil || s.storage == nil {
		return image, nil
	}
	if !strings.HasPrefix(image.URL, "data:") {
		return image, nil
	}
	mimeType, data, err := decodeDataURL(image.URL)
	if err != nil {
		s.logger().Error("asset data url decode failed", slog.Any("error", err))
		return image, fmt.Errorf("%w: %v", ErrPersistence, err)
	}
	filename := s.buildFilename("image", mimeType, ".png")
	path, err := s.storage.Save(ctx, filename, bytes.NewReader(data))
	if err != nil {
		s.logger().Error("asset storage save failed", slog.Any("error", err), slog.String("filename", filename), slog.Int("bytes", len(data)))
		return image, fmt.Errorf("%w: %v", ErrPersistence, err)
	}
	image.URL = s.publicURL(path)
	return image, nil
}

// NormalizeVideo stores inline data URLs for videos and returns a persistent public URL.
func (s *Service) NormalizeVideo(ctx context.Context, video dto.VideoResp) (dto.VideoResp, error) {
	if s == nil || s.storage == nil {
		return video, nil
	}
	if !strings.HasPrefix(video.URL, "data:") {
		return video, nil
	}
	mimeType, data, err := decodeDataURL(video.URL)
	if err != nil {
		s.logger().Error("asset data url decode failed", slog.Any("error", err))
		return video, fmt.Errorf("%w: %v", ErrPersistence, err)
	}
	filename := s.buildFilename("video", mimeType, ".mp4")
	path, err := s.storage.Save(ctx, filename, bytes.NewReader(data))
	if err != nil {
		s.logger().Error("asset storage save failed", slog.Any("error", err), slog.String("filename", filename), slog.Int("bytes", len(data)))
		return video, fmt.Errorf("%w: %v", ErrPersistence, err)
	}
	video.URL = s.publicURL(path)
	return video, nil
}

func (s *Service) logger() *slog.Logger {
	if s != nil && s.log != nil {
		return s.log
	}
	return slog.Default()
}

func (s *Service) publicURL(savedPath string) string {
	if s == nil {
		return savedPath
	}
	name := filepath.Base(savedPath)
	if s.publicBaseURL == "" {
		return name
	}
	return s.publicBaseURL + "/" + url.PathEscape(name)
}

func (s *Service) buildFilename(prefix, mimeType, fallbackExt string) string {
	ext := extensionFromMIME(mimeType)
	if ext == "" {
		ext = fallbackExt
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return fmt.Sprintf("%s-%d%s", prefix, time.Now().UnixNano(), ext)
}

func decodeDataURL(raw string) (string, []byte, error) {
	parts := strings.SplitN(raw, ",", 2)
	if len(parts) != 2 {
		return "", nil, errors.New("invalid data url format")
	}
	meta := parts[0]
	payload := parts[1]
	mimeType := ""
	if strings.HasPrefix(meta, "data:") {
		meta = strings.TrimPrefix(meta, "data:")
		if idx := strings.Index(meta, ";"); idx != -1 {
			mimeType = meta[:idx]
			meta = meta[idx+1:]
		} else {
			mimeType = meta
			meta = ""
		}
	}
	if !strings.Contains(meta, "base64") {
		return "", nil, errors.New("data url must be base64 encoded")
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", nil, fmt.Errorf("decode data url: %w", err)
	}
	return mimeType, decoded, nil
}

func extensionFromMIME(mimeType string) string {
	if mimeType == "" {
		return ""
	}
	switch strings.ToLower(mimeType) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "video/quicktime":
		return ".mov"
	default:
		if exts, err := mime.ExtensionsByType(mimeType); err == nil && len(exts) > 0 {
			return exts[0]
		}
		return ""
	}
}
