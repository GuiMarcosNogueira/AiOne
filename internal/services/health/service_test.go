package health

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/midia/aione/internal/providers"
	"github.com/midia/aione/internal/providers/dto"
)

func TestServiceAggregatesProviderHealth(t *testing.T) {
	providersList := []providers.Provider{
		&fakeProvider{name: "stable"},
		&fakeProvider{name: "flaky", healthErr: errors.New("timeout")},
	}
	svc := NewService(providersList)
	statuses := svc.Check(context.Background())
	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(statuses))
	}
	if !statuses[0].Healthy || statuses[0].Name != "stable" {
		t.Fatalf("expected stable provider to be healthy, got %+v", statuses[0])
	}
	if statuses[1].Healthy || statuses[1].Error == "" {
		t.Fatalf("expected flaky provider to return error, got %+v", statuses[1])
	}
	if time.Since(statuses[0].Checked) > time.Second {
		t.Fatalf("expected checked timestamp to be recent")
	}
}

type fakeProvider struct {
	name      string
	healthErr error
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Capabilities() providers.Capabilities { return providers.Capabilities{} }

func (f *fakeProvider) Health(ctx context.Context) error { return f.healthErr }

func (f *fakeProvider) TextGenerate(ctx context.Context, req dto.TextReq) (dto.TextResp, error) {
	return dto.TextResp{}, nil
}

func (f *fakeProvider) ImageGenerate(ctx context.Context, req dto.ImageReq) (dto.ImageResp, error) {
	return dto.ImageResp{}, nil
}

func (f *fakeProvider) ImageEdit(ctx context.Context, req dto.ImageEditReq) (dto.ImageResp, error) {
	return dto.ImageResp{}, nil
}

func (f *fakeProvider) VideoGenerate(ctx context.Context, req dto.VideoReq) (dto.VideoResp, error) {
	return dto.VideoResp{}, nil
}

func (f *fakeProvider) SpeechToText(ctx context.Context, req dto.STTReq) (dto.STTResp, error) {
	return dto.STTResp{}, nil
}

func (f *fakeProvider) TextToSpeech(ctx context.Context, req dto.TTSReq) (dto.TTSResp, error) {
	return dto.TTSResp{}, nil
}

func (f *fakeProvider) Embeddings(ctx context.Context, req dto.EmbeddingsReq) (dto.EmbeddingsResp, error) {
	return dto.EmbeddingsResp{}, nil
}

func (f *fakeProvider) Moderation(ctx context.Context, req dto.ModerationReq) (dto.ModerationResp, error) {
	return dto.ModerationResp{}, nil
}
