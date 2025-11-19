package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/midia/aione/internal/providers"
	"github.com/midia/aione/internal/providers/dto"
	mockproviders "github.com/midia/aione/internal/providers/mock"
	providermanager "github.com/midia/aione/internal/services/provider"
)

func TestChatSuccess(t *testing.T) {
	api := newTestAPI()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat?strategy=fast", strings.NewReader(`{"prompt":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.Chat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp responseEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Provider == "" || resp.Data == nil {
		t.Fatalf("expected provider response, got %+v", resp)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	api := newTestAPI()
	req := httptest.NewRequest(http.MethodGet, "/v1/chat", nil)
	rec := httptest.NewRecorder()
	api.Chat(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != http.MethodPost {
		t.Fatalf("expected Allow header POST, got %s", allow)
	}
}

func TestDecodeErrorReturnsBadRequest(t *testing.T) {
	api := newTestAPI()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat", strings.NewReader("{"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Chat(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestChatUnknownProviderReturnsBadRequest(t *testing.T) {
	api := newTestAPI()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat", strings.NewReader(`{"prompt":"hi","provider":"missing"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.Chat(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown provider, got %d", rec.Code)
	}
}

func TestHandleErrorMappings(t *testing.T) {
	api := newTestAPI()
	testCases := []struct {
		err    error
		expect int
	}{
		{providermanager.ErrNoProviders, http.StatusServiceUnavailable},
		{providermanager.ErrCapabilityUnavailable, http.StatusNotImplemented},
		{providermanager.ErrRateLimited, http.StatusTooManyRequests},
		{providermanager.ErrCircuitOpen, http.StatusServiceUnavailable},
		{errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range testCases {
		rec := httptest.NewRecorder()
		api.handleError(rec, tc.err)
		if rec.Code != tc.expect {
			t.Fatalf("error %v => status %d, expected %d", tc.err, rec.Code, tc.expect)
		}
	}
}

func TestProvidersEndpointReturnsMatrix(t *testing.T) {
	api := newTestAPI()
	req := httptest.NewRequest(http.MethodGet, "/v1/providers", nil)
	rec := httptest.NewRecorder()

	api.Providers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	var payload struct {
		Data struct {
			Providers []string                                `json:"providers"`
			Matrix    []providermanager.CapabilityMatrixEntry `json:"matrix"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Data.Providers) == 0 {
		t.Fatal("expected providers list in response")
	}
	if len(payload.Data.Matrix) == 0 {
		t.Fatal("expected capability matrix in response")
	}
}

func TestProvidersMethodNotAllowed(t *testing.T) {
	api := newTestAPI()
	req := httptest.NewRequest(http.MethodPost, "/v1/providers", nil)
	rec := httptest.NewRecorder()
	api.Providers(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestHandlePostErrorFlow(t *testing.T) {
	api := New(testLogger(), nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/test", strings.NewReader(`{"prompt":"p"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlePost(api, rec, req, func(ctx context.Context, req dto.TextReq) (any, string, error) {
		return nil, "", providermanager.ErrRateLimited
	})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected rate limited status, got %d", rec.Code)
	}
}

func TestDecodeBodyRejectsUnknownFields(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"unexpected":true}`))
	req.Header.Set("Content-Type", "application/json")
	type input struct {
		Name string `json:"name"`
	}
	if err := decodeBody(req, &input{}); err == nil {
		t.Fatalf("expected error for unknown field")
	}
}

func TestWriteJSONHandlesEncodeError(t *testing.T) {
	api := newTestAPI()
	fw := &failingWriter{header: http.Header{}}
	api.writeJSON(fw, http.StatusOK, responseEnvelope{Data: make(chan int)})
	if fw.written != http.StatusOK {
		t.Fatalf("expected status to be written")
	}
}

func newTestAPI() *API {
	log := testLogger()
	providers := []providers.Provider{mockproviders.New("mock-openai")}
	manager := providermanager.NewManager(providers)
	return New(log, manager)
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type failingWriter struct {
	header  http.Header
	written int
}

func (f *failingWriter) Header() http.Header { return f.header }

func (f *failingWriter) Write(b []byte) (int, error) { return 0, errors.New("write failed") }

func (f *failingWriter) WriteHeader(status int) { f.written = status }
