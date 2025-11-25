package assets

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/midia/aione/internal/providers/dto"
)

type stubStorage struct {
	lastName string
	data     []byte
	err      error
}

func (s *stubStorage) Save(ctx context.Context, name string, r io.Reader) (string, error) {
	s.lastName = name
	buf := &bytes.Buffer{}
	if _, err := buf.ReadFrom(r); err != nil {
		return "", err
	}
	s.data = buf.Bytes()
	if s.err != nil {
		return "", s.err
	}
	if strings.TrimSpace(name) == "" {
		name = "asset.bin"
	}
	return "/tmp/storage/" + name, nil
}

func TestNormalizeImageStoresDataURL(t *testing.T) {
	store := &stubStorage{}
	svc := &Service{storage: store, publicBaseURL: "https://cdn.local/media"}
	img := dto.ImageResp{URL: "data:image/png;base64,aGVsbG8="}
	normalized, err := svc.NormalizeImage(context.Background(), img)
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if !strings.HasPrefix(normalized.URL, "https://cdn.local/media/image-") || !strings.HasSuffix(normalized.URL, ".png") {
		t.Fatalf("expected public image url, got %s", normalized.URL)
	}
	if len(store.data) == 0 {
		t.Fatalf("expected storage to receive data")
	}
}

func TestNormalizeImageNoopWhenURLIsHTTP(t *testing.T) {
	svc := &Service{}
	img := dto.ImageResp{URL: "https://example.com/image.png"}
	normalized, err := svc.NormalizeImage(context.Background(), img)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if normalized.URL != img.URL {
		t.Fatalf("expected url unchanged")
	}
}

func TestNormalizeImagePropagatesPersistenceError(t *testing.T) {
	store := &stubStorage{err: errors.New("disk full")}
	svc := &Service{storage: store}
	img := dto.ImageResp{URL: "data:image/png;base64,aGVsbG8="}
	_, err := svc.NormalizeImage(context.Background(), img)
	if !errors.Is(err, ErrPersistence) {
		t.Fatalf("expected ErrPersistence, got %v", err)
	}
}

func TestNormalizeVideoStoresDataURL(t *testing.T) {
	store := &stubStorage{}
	svc := &Service{storage: store, publicBaseURL: "https://cdn.local/media"}
	vid := dto.VideoResp{URL: "data:video/mp4;base64,aGVsbG8="}
	normalized, err := svc.NormalizeVideo(context.Background(), vid)
	if err != nil {
		t.Fatalf("normalize video failed: %v", err)
	}
	if !strings.HasPrefix(normalized.URL, "https://cdn.local/media/video-") || !strings.HasSuffix(normalized.URL, ".mp4") {
		t.Fatalf("expected public video url, got %s", normalized.URL)
	}
	if store.lastName == "" || !strings.HasSuffix(store.lastName, ".mp4") {
		t.Fatalf("expected video filename with mp4 extension, got %s", store.lastName)
	}
}

func TestNormalizeVideoPropagatesPersistenceError(t *testing.T) {
	store := &stubStorage{err: errors.New("disk full")}
	svc := &Service{storage: store}
	vid := dto.VideoResp{URL: "data:video/mp4;base64,aGVsbG8="}
	_, err := svc.NormalizeVideo(context.Background(), vid)
	if !errors.Is(err, ErrPersistence) {
		t.Fatalf("expected ErrPersistence, got %v", err)
	}
}
