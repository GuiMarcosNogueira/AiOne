package jobs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestServiceCreateWithUpload(t *testing.T) {
	repo := NewMemoryRepository()
	storage := &stubStorage{path: "/tmp/job-file"}
	svc := NewService(repo, storage, nil, testLogger(), Options{CallbackMaxAttempts: 5})
	input := CreateInput{
		Prompt:      "describe scene",
		CallbackURL: "http://callback",
		File:        newMultipartFile([]byte("payload")),
		FileName:    "clip.mp4",
	}
	job, err := svc.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if job.ID == "" {
		t.Fatalf("expected generated job id")
	}
	if job.Type != TypeGeneric {
		t.Fatalf("expected default type generic, got %s", job.Type)
	}
	if job.FilePath != storage.path {
		t.Fatalf("expected stored file path, got %s", job.FilePath)
	}
	if storage.calls != 1 {
		t.Fatalf("expected storage Save to be called once, got %d", storage.calls)
	}
	if job.MaxCallbackAttempts != 5 {
		t.Fatalf("expected callback attempts 5, got %d", job.MaxCallbackAttempts)
	}
}

func TestServiceCreateWithoutUpload(t *testing.T) {
	repo := NewMemoryRepository()
	storage := &stubStorage{}
	svc := NewService(repo, storage, nil, testLogger(), Options{})
	job, err := svc.Create(context.Background(), CreateInput{})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if job.Type != TypeGeneric {
		t.Fatalf("expected default type generic")
	}
	if job.FilePath != "" {
		t.Fatalf("expected empty file path")
	}
	if job.MaxCallbackAttempts != 1 {
		t.Fatalf("expected at least one callback attempt, got %d", job.MaxCallbackAttempts)
	}
	if storage.calls != 0 {
		t.Fatalf("expected storage not to be called")
	}
}

func TestServiceCreateRejectsInvalidType(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo, &stubStorage{}, nil, testLogger(), Options{})
	_, err := svc.Create(context.Background(), CreateInput{Type: Type("unknown")})
	if err == nil || !strings.Contains(err.Error(), "unsupported job type") {
		t.Fatalf("expected unsupported type error, got %v", err)
	}
}

func TestServiceCreateHandlesStorageError(t *testing.T) {
	repo := NewMemoryRepository()
	storage := &stubStorage{err: errors.New("disk full")}
	svc := NewService(repo, storage, nil, testLogger(), Options{})
	_, err := svc.Create(context.Background(), CreateInput{File: newMultipartFile([]byte("data")), FileName: "input.bin"})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("expected storage error, got %v", err)
	}
}

func TestServiceGet(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo, &stubStorage{}, nil, testLogger(), Options{})
	ctx := context.Background()
	expected := Job{ID: "known", Status: StatusPending, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if _, err := repo.Create(ctx, expected); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	job, err := svc.Get(ctx, "known")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if job.ID != expected.ID {
		t.Fatalf("expected job id %s, got %s", expected.ID, job.ID)
	}
	if _, err := svc.Get(ctx, "missing"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("expected ErrJobNotFound, got %v", err)
	}
}
