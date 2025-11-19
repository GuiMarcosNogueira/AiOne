package jobs

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"mime/multipart"
)

// testLogger returns a logger that discards all output.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// stubStorage records calls to Save for assertions.
type stubStorage struct {
	path      string
	err       error
	calls     int
	savedName string
	savedData []byte
}

func (s *stubStorage) Save(ctx context.Context, name string, r io.Reader) (string, error) {
	s.calls++
	s.savedName = name
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	s.savedData = data
	if s.err != nil {
		return "", s.err
	}
	if s.path == "" {
		s.path = "/tmp/upload"
	}
	return s.path, nil
}

// nopSeekCloser adapts a *bytes.Reader into a multipart.File.
type nopSeekCloser struct {
	*bytes.Reader
}

func (n nopSeekCloser) Close() error { return nil }

func newMultipartFile(data []byte) multipart.File {
	return nopSeekCloser{bytes.NewReader(data)}
}
