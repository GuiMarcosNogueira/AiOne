package router

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/midia/aione/api/handlers"
	"github.com/midia/aione/internal/providers"
	mockproviders "github.com/midia/aione/internal/providers/mock"
	"github.com/midia/aione/internal/services/health"
	providermanager "github.com/midia/aione/internal/services/provider"
)

func TestRouterHealthz(t *testing.T) {
	log := testLogger()
	healthSvc := &stubHealth{statuses: []health.Status{{Name: "mock", Healthy: true}}}
	api := handlers.New(log, providermanager.NewManager([]providers.Provider{mockproviders.New("mock")}))
	r := New(log, healthSvc, api, nil)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body struct {
		Status    string          `json:"status"`
		Providers []health.Status `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "ok" || len(body.Providers) != 1 {
		t.Fatalf("unexpected payload: %+v", body)
	}
}

func TestRouterDocs(t *testing.T) {
	r := New(testLogger(), &stubHealth{}, handlers.New(testLogger(), providermanager.NewManager(nil)), nil)
	req := httptest.NewRequest(http.MethodGet, "/docs", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "SwaggerUIBundle") {
		t.Fatalf("expected swagger html, got %s", rec.Body.String())
	}
}

func TestRouterOpenAPI(t *testing.T) {
	oldPath := openAPIPath
	t.Cleanup(func() { openAPIPath = oldPath })
	openAPIPath = filepath.Join("..", "..", "..", "openapi.yaml")
	r := New(testLogger(), &stubHealth{}, handlers.New(testLogger(), providermanager.NewManager(nil)), nil)
	req := httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected openapi file, got %d", rec.Code)
	}
}

func TestSwaggerHTML(t *testing.T) {
	html := swaggerHTML("/spec.yaml")
	if !strings.Contains(html, "/spec.yaml") {
		t.Fatalf("expected html to reference spec url")
	}
}

func TestWriteJSONEncodeError(t *testing.T) {
	fw := &failingWriter{header: http.Header{}}
	writeJSON(fw, testLogger(), http.StatusOK, map[string]any{"chan": make(chan int)})
	if fw.status != http.StatusOK {
		t.Fatalf("expected status written")
	}
}

type stubHealth struct {
	statuses []health.Status
}

func (s *stubHealth) Check(ctx context.Context) []health.Status {
	return s.statuses
}

type failingWriter struct {
	header http.Header
	status int
}

func (f *failingWriter) Header() http.Header { return f.header }

func (f *failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func (f *failingWriter) WriteHeader(status int) { f.status = status }

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
