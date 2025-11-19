package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Storage abstracts saving large uploads to persistent storage.
type Storage interface {
	Save(ctx context.Context, name string, r io.Reader) (string, error)
}

// LocalStorage stores files on disk under the configured base path.
type LocalStorage struct {
	basePath string
}

// NewLocal creates a LocalStorage instance rooted at basePath.
func NewLocal(basePath string) *LocalStorage {
	return &LocalStorage{basePath: basePath}
}

// Save streams the reader to disk and returns the resulting absolute path.
func (l *LocalStorage) Save(ctx context.Context, name string, r io.Reader) (string, error) {
	if err := os.MkdirAll(l.basePath, 0o755); err != nil {
		return "", fmt.Errorf("create storage dir: %w", err)
	}
	filename := fmt.Sprintf("%d-%s", time.Now().UnixNano(), filepath.Base(name))
	path := filepath.Join(l.basePath, filename)
	file, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create file: %w", err)
	}
	defer file.Close()
	if _, err := io.Copy(file, r); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}
	return path, nil
}
