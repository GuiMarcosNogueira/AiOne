package jobs

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryRepositoryCreateAndGet(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()
	job := Job{ID: "job-1", Status: StatusPending, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if _, err := repo.Create(ctx, job); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	got, err := repo.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.ID != job.ID {
		t.Fatalf("unexpected job id: %s", got.ID)
	}
	if got.Status != StatusPending {
		t.Fatalf("unexpected job status: %s", got.Status)
	}
}

func TestMemoryRepositoryGetMissing(t *testing.T) {
	repo := NewMemoryRepository()
	_, err := repo.Get(context.Background(), "missing")
	if !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("expected ErrJobNotFound, got %v", err)
	}
}

func TestMemoryRepositoryUpdateMissing(t *testing.T) {
	repo := NewMemoryRepository()
	_, err := repo.Update(context.Background(), Job{ID: "ghost"})
	if !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("expected ErrJobNotFound, got %v", err)
	}
}

func TestMemoryRepositoryClaimPending(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()
	now := time.Now()
	newer := Job{ID: "newer", Status: StatusPending, CreatedAt: now}
	older := Job{ID: "older", Status: StatusPending, CreatedAt: now.Add(-time.Minute)}
	if _, err := repo.Create(ctx, newer); err != nil {
		t.Fatalf("create newer: %v", err)
	}
	if _, err := repo.Create(ctx, older); err != nil {
		t.Fatalf("create older: %v", err)
	}
	claimed, err := repo.ClaimPending(ctx)
	if err != nil {
		t.Fatalf("ClaimPending failed: %v", err)
	}
	if claimed == nil || claimed.ID != older.ID {
		t.Fatalf("expected oldest job, got %+v", claimed)
	}
	if claimed.Status != StatusRunning {
		t.Fatalf("expected running status, got %s", claimed.Status)
	}
	if claimed.LastDispatchedAt.IsZero() {
		t.Fatalf("expected LastDispatchedAt to be set")
	}
	if claimed.UpdatedAt.IsZero() {
		t.Fatalf("expected UpdatedAt to be set")
	}
	// Ensure repository reflects the status change.
	stored, err := repo.Get(ctx, older.ID)
	if err != nil {
		t.Fatalf("Get after claim failed: %v", err)
	}
	if stored.Status != StatusRunning {
		t.Fatalf("expected stored job to be running, got %s", stored.Status)
	}
	// Subsequent claim should target the remaining pending job.
	claimed, err = repo.ClaimPending(ctx)
	if err != nil {
		t.Fatalf("second claim failed: %v", err)
	}
	if claimed == nil || claimed.ID != newer.ID {
		t.Fatalf("expected second pending job, got %+v", claimed)
	}
}

func TestMemoryRepositoryJobsNeedingCallback(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()
	now := time.Now()
	jobs := []Job{
		{ID: "ready", Status: StatusCompleted, CallbackURL: "http://callback", CallbackAttempts: 1, MaxCallbackAttempts: 3, NextCallbackAttempt: now.Add(-time.Minute)},
		{ID: "future", Status: StatusCompleted, CallbackURL: "http://callback", CallbackAttempts: 1, MaxCallbackAttempts: 3, NextCallbackAttempt: now.Add(time.Minute)},
		{ID: "missing-callback", Status: StatusCompleted},
		{ID: "exhausted", Status: StatusCompleted, CallbackURL: "http://callback", CallbackAttempts: 3, MaxCallbackAttempts: 3},
		{ID: "running", Status: StatusRunning, CallbackURL: "http://callback", MaxCallbackAttempts: 3},
	}
	for _, job := range jobs {
		if _, err := repo.Create(ctx, job); err != nil {
			t.Fatalf("create %s: %v", job.ID, err)
		}
	}
	list, err := repo.JobsNeedingCallback(ctx, now)
	if err != nil {
		t.Fatalf("JobsNeedingCallback failed: %v", err)
	}
	if len(list) != 1 || list[0].ID != "ready" {
		t.Fatalf("expected only ready job, got %+v", list)
	}
}
