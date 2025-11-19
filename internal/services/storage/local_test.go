package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalStorageSaveWritesFile(t *testing.T) {
	dir := t.TempDir()
	store := NewLocal(dir)
	path, err := store.Save(context.Background(), "video.mp4", strings.NewReader("payload"))
	if err != nil {
		t.Fatalf("save failed: %v", err)
	}
	if !strings.HasPrefix(path, dir) {
		t.Fatalf("expected file under temp dir, got %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if string(data) != "payload" {
		t.Fatalf("unexpected file contents: %s", data)
	}
}

func TestLocalStorageSaveErrorsWhenDirectoryBlocked(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte(""), 0o644); err != nil {
		t.Fatalf("setup blocked file: %v", err)
	}
	store := NewLocal(blocked)
	if _, err := store.Save(context.Background(), "video.mp4", strings.NewReader("payload")); err == nil {
		t.Fatal("expected error when base path is a file")
	}
}
