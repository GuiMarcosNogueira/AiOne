package logger

import (
	"context"
	"log/slog"
	"testing"
)

func TestNewCreatesLogger(t *testing.T) {
	log := New("debug")
	if log == nil {
		t.Fatalf("expected logger instance")
	}
	if !log.Enabled(context.TODO(), slog.LevelDebug) {
		t.Fatalf("expected debug level to be enabled with context")
	}
	if !log.Enabled(context.TODO(), slog.LevelDebug) {
		t.Fatalf("expected debug level to be enabled without context")
	}
}

func TestParseLevel(t *testing.T) {
	testCases := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
		"INFO":  slog.LevelInfo,
		"":      slog.LevelInfo,
	}
	for input, expected := range testCases {
		if got := parseLevel(input); got != expected {
			t.Fatalf("parseLevel(%q) = %v, want %v", input, got, expected)
		}
	}
}
