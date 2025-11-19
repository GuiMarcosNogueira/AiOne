package generichttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/midia/aione/internal/providers"
	"github.com/midia/aione/internal/providers/dto"
)

type endpointKind string

type httpClient interface {
	Do(*http.Request) (*http.Response, error)
}

const (
	endpointText       endpointKind = "text"
	endpointImage      endpointKind = "image"
	endpointVideo      endpointKind = "video"
	endpointSTT        endpointKind = "stt"
	endpointTTS        endpointKind = "tts"
	endpointEmbeddings endpointKind = "embeddings"
	endpointModeration endpointKind = "moderation"

	defaultTimeout = 30 * time.Second
)

// Provider exposes a dynamic HTTP adapter backed by configuration files.
type Provider struct {
	name       string
	baseURL    string
	headers    map[string]string
	auth       AuthConfig
	caps       providers.Capabilities
	httpClient httpClient
	timeout    time.Duration
	endpoints  map[endpointKind]*endpointHandler
}

// Option customizes the provider.
type Option func(*Provider)

// WithHTTPClient overrides the default http.Client.
func WithHTTPClient(client httpClient) Option {
	return func(p *Provider) {
		if client != nil {
			p.httpClient = client
		}
	}
}

// NewFromConfig builds a Provider based on the supplied file configuration.
func NewFromConfig(cfg FileConfig, opts ...Option) (providers.Provider, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	timeout := defaultTimeout
	if cfg.TimeoutSeconds > 0 {
		timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
	}
	p := &Provider{
		name:      strings.TrimSpace(cfg.Name),
		baseURL:   strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		headers:   normalizeMap(cfg.Headers),
		auth:      cfg.Auth,
		caps:      cfg.deriveCapabilities(),
		timeout:   timeout,
		endpoints: make(map[endpointKind]*endpointHandler),
	}
	p.httpClient = &http.Client{Timeout: p.timeout}
	// register endpoints based on config
	if err := p.registerEndpoint(endpointText, cfg.Endpoints.Text); err != nil {
		return nil, fmt.Errorf("configure text endpoint: %w", err)
	}
	if err := p.registerEndpoint(endpointImage, cfg.Endpoints.Image); err != nil {
		return nil, fmt.Errorf("configure image endpoint: %w", err)
	}
	if err := p.registerEndpoint(endpointVideo, cfg.Endpoints.Video); err != nil {
		return nil, fmt.Errorf("configure video endpoint: %w", err)
	}
	if err := p.registerEndpoint(endpointSTT, cfg.Endpoints.STT); err != nil {
		return nil, fmt.Errorf("configure stt endpoint: %w", err)
	}
	if err := p.registerEndpoint(endpointTTS, cfg.Endpoints.TTS); err != nil {
		return nil, fmt.Errorf("configure tts endpoint: %w", err)
	}
	if err := p.registerEndpoint(endpointEmbeddings, cfg.Endpoints.Embeddings); err != nil {
		return nil, fmt.Errorf("configure embeddings endpoint: %w", err)
	}
	if err := p.registerEndpoint(endpointModeration, cfg.Endpoints.Moderation); err != nil {
		return nil, fmt.Errorf("configure moderation endpoint: %w", err)
	}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

func (p *Provider) registerEndpoint(kind endpointKind, cfg *EndpointConfig) error {
	if cfg == nil || !cfg.enabled() {
		return nil
	}
	method := strings.ToUpper(strings.TrimSpace(cfg.Method))
	if method == "" {
		method = http.MethodPost
	}
	path := strings.TrimSpace(cfg.Path)
	if path == "" {
		return errors.New("path is required")
	}
	tmpl, err := compileTemplate(p.name, kind, cfg.Request.BodyTemplate)
	if err != nil {
		return err
	}
	handler := &endpointHandler{
		kind:     kind,
		method:   method,
		path:     path,
		query:    normalizeMap(cfg.Query),
		headers:  normalizeMap(cfg.Headers),
		success:  newStatusSet(cfg.SuccessStatuses),
		timeout:  cfg.TimeoutSeconds,
		request:  cfg.Request,
		response: cfg.Response,
		tmpl:     tmpl,
	}
	p.endpoints[kind] = handler
	return nil
}

// Name implements providers.Provider.
func (p *Provider) Name() string { return p.name }

// Capabilities exposes the routing metadata for this provider.
func (p *Provider) Capabilities() providers.Capabilities { return p.caps }

// Health performs a lightweight GET to the configured base URL.
func (p *Provider) Health(ctx context.Context) error {
	if p.httpClient == nil {
		return errors.New("http client not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL, nil)
	if err != nil {
		return err
	}
	p.applyHeaders(req, nil)
	p.applyAuth(req)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("provider %s health failed: %s", p.name, resp.Status)
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

// TextGenerate forwards text prompts.
func (p *Provider) TextGenerate(ctx context.Context, req dto.TextReq) (dto.TextResp, error) {
	result, err := p.invoke(ctx, endpointText, req)
	if err != nil {
		return dto.TextResp{}, err
	}
	text, err := extractString(result.data, result.mapping.TextPath)
	if err != nil {
		return dto.TextResp{}, err
	}
	return dto.TextResp{Content: text}, nil
}

func (p *Provider) ImageGenerate(ctx context.Context, req dto.ImageReq) (dto.ImageResp, error) {
	result, err := p.invoke(ctx, endpointImage, req)
	if err != nil {
		return dto.ImageResp{}, err
	}
	url, err := extractString(result.data, result.mapping.URLPath)
	if err != nil {
		return dto.ImageResp{}, err
	}
	return dto.ImageResp{URL: url}, nil
}

func (p *Provider) VideoGenerate(ctx context.Context, req dto.VideoReq) (dto.VideoResp, error) {
	result, err := p.invoke(ctx, endpointVideo, req)
	if err != nil {
		return dto.VideoResp{}, err
	}
	url, err := extractString(result.data, result.mapping.URLPath)
	if err != nil {
		return dto.VideoResp{}, err
	}
	return dto.VideoResp{URL: url}, nil
}

func (p *Provider) SpeechToText(ctx context.Context, req dto.STTReq) (dto.STTResp, error) {
	result, err := p.invoke(ctx, endpointSTT, req)
	if err != nil {
		return dto.STTResp{}, err
	}
	transcript, err := extractString(result.data, result.mapping.TranscriptPath)
	if err != nil {
		return dto.STTResp{}, err
	}
	return dto.STTResp{Transcript: transcript}, nil
}

func (p *Provider) TextToSpeech(ctx context.Context, req dto.TTSReq) (dto.TTSResp, error) {
	result, err := p.invoke(ctx, endpointTTS, req)
	if err != nil {
		return dto.TTSResp{}, err
	}
	audioURL, err := extractString(result.data, result.mapping.AudioURLPath)
	if err != nil {
		return dto.TTSResp{}, err
	}
	return dto.TTSResp{AudioURL: audioURL}, nil
}

func (p *Provider) Embeddings(ctx context.Context, req dto.EmbeddingsReq) (dto.EmbeddingsResp, error) {
	result, err := p.invoke(ctx, endpointEmbeddings, req)
	if err != nil {
		return dto.EmbeddingsResp{}, err
	}
	vectors, err := extractEmbeddings(result.data, result.mapping.EmbeddingsPath)
	if err != nil {
		return dto.EmbeddingsResp{}, err
	}
	return dto.EmbeddingsResp{Vectors: vectors}, nil
}

func (p *Provider) Moderation(ctx context.Context, req dto.ModerationReq) (dto.ModerationResp, error) {
	result, err := p.invoke(ctx, endpointModeration, req)
	if err != nil {
		return dto.ModerationResp{}, err
	}
	flagged, err := extractBool(result.data, result.mapping.FlaggedPath)
	if err != nil {
		return dto.ModerationResp{}, err
	}
	reason, _ := extractString(result.data, result.mapping.ReasonPath)
	return dto.ModerationResp{Flagged: flagged, Reason: reason}, nil
}

type endpointResult struct {
	data    any
	mapping ResponseMapping
}

func (p *Provider) invoke(ctx context.Context, kind endpointKind, req any) (endpointResult, error) {
	handler, ok := p.endpoints[kind]
	if !ok {
		return endpointResult{}, fmt.Errorf("provider %s does not support %s", p.name, kind)
	}
	data, err := handler.call(ctx, p, req)
	if err != nil {
		return endpointResult{}, err
	}
	return endpointResult{data: data, mapping: handler.response}, nil
}

type endpointHandler struct {
	kind     endpointKind
	method   string
	path     string
	query    map[string]string
	headers  map[string]string
	success  map[int]struct{}
	timeout  int
	request  RequestMapping
	response ResponseMapping
	tmpl     *template.Template
}

func (h *endpointHandler) call(ctx context.Context, p *Provider, req any) (any, error) {
	reqCtx := ctx
	cancel := func() {}
	if h.timeout > 0 {
		reqCtx, cancel = context.WithTimeout(ctx, time.Duration(h.timeout)*time.Second)
	}
	defer cancel()

	body, contentType, err := h.renderBody(req)
	if err != nil {
		return nil, err
	}

	endpoint := p.buildURL(h.path, h.query)
	httpReq, err := http.NewRequestWithContext(reqCtx, h.method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if len(body) > 0 {
		ct := h.request.ContentType
		if strings.TrimSpace(ct) == "" {
			ct = "application/json"
		}
		httpReq.Header.Set("Content-Type", ct)
	}
	p.applyHeaders(httpReq, h.headers)
	if contentType != "" {
		httpReq.Header.Set("Content-Type", contentType)
	}
	p.applyAuth(httpReq)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if !h.successStatus(resp.StatusCode) {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		return nil, fmt.Errorf("provider %s %s %s failed: %s %s", p.name, h.method, h.path, resp.Status, strings.TrimSpace(string(payload)))
	}

	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	var data any
	if err := decoder.Decode(&data); err != nil {
		return nil, err
	}
	return data, nil
}

func (h *endpointHandler) renderBody(request any) ([]byte, string, error) {
	if h.tmpl == nil {
		return nil, "", nil
	}
	data, err := toTemplateMap(request)
	if err != nil {
		return nil, "", err
	}
	var buf bytes.Buffer
	if err := h.tmpl.Execute(&buf, data); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), strings.TrimSpace(h.request.ContentType), nil
}

func (h *endpointHandler) successStatus(code int) bool {
	if len(h.success) == 0 {
		return code >= 200 && code < 300
	}
	_, ok := h.success[code]
	return ok
}

func (p *Provider) buildURL(path string, query map[string]string) string {
	endpoint := path
	if !strings.HasPrefix(path, "http") {
		if strings.HasPrefix(path, "/") {
			endpoint = p.baseURL + path
		} else {
			endpoint = p.baseURL + "/" + path
		}
	}
	if len(query) == 0 {
		return endpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	values := u.Query()
	for key, value := range query {
		values.Set(key, os.ExpandEnv(value))
	}
	u.RawQuery = values.Encode()
	return u.String()
}

func (p *Provider) applyHeaders(req *http.Request, extra map[string]string) {
	for key, value := range p.headers {
		if value == "" {
			continue
		}
		req.Header.Set(key, os.ExpandEnv(value))
	}
	for key, value := range extra {
		if value == "" {
			continue
		}
		req.Header.Set(key, os.ExpandEnv(value))
	}
}

func (p *Provider) applyAuth(req *http.Request) {
	authType := strings.ToLower(strings.TrimSpace(p.auth.Type))
	switch authType {
	case "bearer":
		token := p.auth.resolveValue()
		if token == "" {
			return
		}
		prefix := strings.TrimSpace(p.auth.Prefix)
		if prefix == "" {
			prefix = "Bearer"
		}
		req.Header.Set("Authorization", fmt.Sprintf("%s %s", prefix, token))
	case "apikey", "api_key":
		value := p.auth.resolveValue()
		if value == "" {
			return
		}
		location := strings.ToLower(strings.TrimSpace(p.auth.In))
		name := p.auth.Name
		if name == "" {
			name = "X-API-Key"
		}
		value = strings.TrimSpace(strings.Join([]string{strings.TrimSpace(p.auth.Prefix), value}, " "))
		value = strings.TrimSpace(value)
		if location == "query" {
			q := req.URL.Query()
			q.Set(name, value)
			req.URL.RawQuery = q.Encode()
		} else {
			req.Header.Set(name, value)
		}
	case "basic":
		username := p.auth.resolveUsername()
		password := p.auth.resolvePassword()
		req.SetBasicAuth(username, password)
	}
}

func (cfg AuthConfig) resolveValue() string {
	if v := strings.TrimSpace(cfg.ValueFromEnv); v != "" {
		if env, ok := os.LookupEnv(v); ok {
			return env
		}
	}
	if strings.TrimSpace(cfg.Value) == "" {
		return ""
	}
	return os.ExpandEnv(cfg.Value)
}

func (cfg AuthConfig) resolveUsername() string {
	if v := strings.TrimSpace(cfg.UsernameFromEnv); v != "" {
		if env, ok := os.LookupEnv(v); ok {
			return env
		}
	}
	if strings.TrimSpace(cfg.Username) == "" {
		return ""
	}
	return os.ExpandEnv(cfg.Username)
}

func (cfg AuthConfig) resolvePassword() string {
	if v := strings.TrimSpace(cfg.PasswordFromEnv); v != "" {
		if env, ok := os.LookupEnv(v); ok {
			return env
		}
	}
	if strings.TrimSpace(cfg.Password) == "" {
		return ""
	}
	return os.ExpandEnv(cfg.Password)
}

func compileTemplate(provider string, kind endpointKind, body string) (*template.Template, error) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return nil, nil
	}
	tmpl, err := template.New(fmt.Sprintf("%s-%s", provider, kind)).Funcs(template.FuncMap{
		"toJSON": func(v any) (string, error) {
			b, err := json.Marshal(v)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
		"env": func(key string) string {
			return os.Getenv(key)
		},
	}).Parse(trimmed)
	if err != nil {
		return nil, err
	}
	return tmpl, nil
}

func toTemplateMap(req any) (map[string]any, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	data["raw"] = req
	return data, nil
}

func newStatusSet(list []int) map[int]struct{} {
	if len(list) == 0 {
		return nil
	}
	set := make(map[int]struct{}, len(list))
	for _, code := range list {
		set[code] = struct{}{}
	}
	return set
}

func normalizeMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	clone := make(map[string]string, len(input))
	for key, value := range input {
		clone[key] = value
	}
	return clone
}

func extractString(data any, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("response mapping missing string path")
	}
	value, err := lookupPath(data, path)
	if err != nil {
		return "", err
	}
	str, ok := value.(string)
	if ok {
		return str, nil
	}
	if num, ok := value.(json.Number); ok {
		return num.String(), nil
	}
	return "", fmt.Errorf("expected string at %s", path)
}

func extractBool(data any, path string) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, errors.New("response mapping missing bool path")
	}
	value, err := lookupPath(data, path)
	if err != nil {
		return false, err
	}
	if b, ok := value.(bool); ok {
		return b, nil
	}
	return false, fmt.Errorf("expected bool at %s", path)
}

func extractEmbeddings(data any, path string) ([][]float32, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("response mapping missing embeddings path")
	}
	value, err := lookupPath(data, path)
	if err != nil {
		return nil, err
	}
	array, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("expected array at %s", path)
	}
	vectors := make([][]float32, len(array))
	for i, item := range array {
		rowAny, ok := item.([]any)
		if !ok {
			return nil, fmt.Errorf("expected float array at %s[%d]", path, i)
		}
		row := make([]float32, len(rowAny))
		for j, val := range rowAny {
			switch typed := val.(type) {
			case float64:
				row[j] = float32(typed)
			case json.Number:
				f, err := typed.Float64()
				if err != nil {
					return nil, err
				}
				row[j] = float32(f)
			default:
				return nil, fmt.Errorf("non numeric embedding at %s[%d][%d]", path, i, j)
			}
		}
		vectors[i] = row
	}
	return vectors, nil
}

func lookupPath(data any, path string) (any, error) {
	cursor := data
	segments := strings.Split(path, ".")
	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		switch current := cursor.(type) {
		case map[string]any:
			next, ok := current[seg]
			if !ok {
				return nil, fmt.Errorf("path %s not found", path)
			}
			cursor = next
		case []any:
			idx, err := parseIndex(seg)
			if err != nil {
				return nil, err
			}
			if idx < 0 || idx >= len(current) {
				return nil, fmt.Errorf("index %d out of range for %s", idx, path)
			}
			cursor = current[idx]
		default:
			return nil, fmt.Errorf("cannot descend into %T at %s", current, seg)
		}
	}
	return cursor, nil
}

func parseIndex(seg string) (int, error) {
	var idx int
	_, err := fmt.Sscanf(seg, "%d", &idx)
	if err != nil {
		return 0, fmt.Errorf("segment %s is not an index", seg)
	}
	return idx, nil
}
