package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/midia/aione/internal/providers"
	"github.com/midia/aione/internal/providers/dto"
	mockproviders "github.com/midia/aione/internal/providers/mock"
	"github.com/midia/aione/internal/services/assets"
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

func TestImageEditJSONPayload(t *testing.T) {
	api := newTestAPI()
	body := `{"prompt":"edit","image_base64":"ZGF0YQ=="}`
	req := httptest.NewRequest(http.MethodPost, "/v1/image/edit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.ImageEdit(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestImageEditMultipartPayload(t *testing.T) {
	api := newTestAPI()
	buf := &bytes.Buffer{}
	writer := multipart.NewWriter(buf)
	if err := writer.WriteField("prompt", "blend"); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	fileWriter, err := writer.CreateFormFile("image_file", "photo.png")
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	if _, err := fileWriter.Write([]byte("png")); err != nil {
		t.Fatalf("write file: %v", err)
	}
	writer.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/image/edit", buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()

	api.ImageEdit(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
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
		{assets.ErrPersistence, http.StatusInternalServerError},
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

func TestModelsEndpointProviderQuery(t *testing.T) {
	provider := newCatalogProvider("catalog")
	manager := providermanager.NewManager([]providers.Provider{provider})
	api := New(testLogger(), manager, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/models?provider=catalog", nil)
	rec := httptest.NewRecorder()

	api.Models(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var payload struct {
		Data struct {
			Provider string                      `json:"provider"`
			Models   []providers.ModelDescriptor `json:"models"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Data.Provider != "catalog" || len(payload.Data.Models) == 0 {
		t.Fatalf("expected models for provider, got %+v", payload.Data)
	}
}

func TestModelsEndpointAllProviders(t *testing.T) {
	provider := newCatalogProvider("catalog")
	manager := providermanager.NewManager([]providers.Provider{provider})
	api := New(testLogger(), manager, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()

	api.Models(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var payload struct {
		Data map[string][]providers.ModelDescriptor `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Data["catalog"]) == 0 {
		t.Fatalf("expected catalog models, got %+v", payload.Data)
	}
}

func TestModelsEndpointUnavailableCatalog(t *testing.T) {
	api := newTestAPI()
	req := httptest.NewRequest(http.MethodGet, "/v1/models?provider=mock-openai", nil)
	rec := httptest.NewRecorder()

	api.Models(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing catalog, got %d", rec.Code)
	}
}

func TestHandlePostErrorFlow(t *testing.T) {
	api := New(testLogger(), nil, nil, nil)
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
	return New(log, manager, nil, nil)
}

type catalogProvider struct {
	base   providers.Provider
	models []providers.ModelDescriptor
}

func newCatalogProvider(name string) *catalogProvider {
	return &catalogProvider{
		base: mockproviders.New(name),
		models: []providers.ModelDescriptor{
			{Provider: name, Name: "test-model", Capability: providers.CapabilityText, Default: true},
		},
	}
}

func (c *catalogProvider) ListModels(ctx context.Context) ([]providers.ModelDescriptor, error) {
	return c.models, nil
}

func (c *catalogProvider) Name() string { return c.base.Name() }

func (c *catalogProvider) Capabilities() providers.Capabilities { return c.base.Capabilities() }

func (c *catalogProvider) Health(ctx context.Context) error { return c.base.Health(ctx) }

func (c *catalogProvider) TextGenerate(ctx context.Context, req dto.TextReq) (dto.TextResp, error) {
	return c.base.TextGenerate(ctx, req)
}

func (c *catalogProvider) ImageGenerate(ctx context.Context, req dto.ImageReq) (dto.ImageResp, error) {
	return c.base.ImageGenerate(ctx, req)
}

func (c *catalogProvider) ImageEdit(ctx context.Context, req dto.ImageEditReq) (dto.ImageResp, error) {
	return c.base.ImageEdit(ctx, req)
}

func (c *catalogProvider) VideoGenerate(ctx context.Context, req dto.VideoReq) (dto.VideoResp, error) {
	return c.base.VideoGenerate(ctx, req)
}

func (c *catalogProvider) SpeechToText(ctx context.Context, req dto.STTReq) (dto.STTResp, error) {
	return c.base.SpeechToText(ctx, req)
}

func (c *catalogProvider) TextToSpeech(ctx context.Context, req dto.TTSReq) (dto.TTSResp, error) {
	return c.base.TextToSpeech(ctx, req)
}

func (c *catalogProvider) Embeddings(ctx context.Context, req dto.EmbeddingsReq) (dto.EmbeddingsResp, error) {
	return c.base.Embeddings(ctx, req)
}

func (c *catalogProvider) Moderation(ctx context.Context, req dto.ModerationReq) (dto.ModerationResp, error) {
	return c.base.Moderation(ctx, req)
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
