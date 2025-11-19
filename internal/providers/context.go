package providers

import (
	"context"
	"strings"
)

type contextKey string

const apiKeyContextKey contextKey = "provider-api-key"

// ContextWithAPIKey stores the per-request provider API key in the context.
func ContextWithAPIKey(ctx context.Context, key string) context.Context {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return ctx
	}
	return context.WithValue(ctx, apiKeyContextKey, trimmed)
}

// APIKeyFromContext extracts the per-request provider API key override, if any.
func APIKeyFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if val, ok := ctx.Value(apiKeyContextKey).(string); ok {
		return strings.TrimSpace(val)
	}
	return ""
}
