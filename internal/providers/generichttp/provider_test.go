package generichttp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/midia/aione/internal/providers/dto"
)

func TestLoadFromDirAndTextGenerate(t *testing.T) {
	t.Setenv("GENERIC_HTTP_TOKEN", "secret")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("unexpected auth header %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	t.Cleanup(srv.Close)

	cfg := fmt.Sprintf(`
name: demo
base_url: %s
auth:
  type: bearer
  value_from_env: GENERIC_HTTP_TOKEN
endpoints:
  text:
    method: POST
    path: /chat
    request:
      content_type: application/json
      body: |
        {"prompt": {{ toJSON .prompt }} }
    response:
      text_path: choices.0.message.content
`, srv.URL)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "demo.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	providers, err := LoadFromDir(dir)
	if err != nil {
		t.Fatalf("load dir: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("expected one provider, got %d", len(providers))
	}

	resp, err := providers[0].TextGenerate(context.Background(), dto.TextReq{Prompt: "hi"})
	if err != nil {
		t.Fatalf("text generate: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("unexpected content %q", resp.Content)
	}
}

func TestEmbeddingsMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"vectors":[[0.1,0.2],[0.3,0.4]]}`)
	}))
	t.Cleanup(srv.Close)

	cfg := fmt.Sprintf(`
name: embed-demo
base_url: %s
endpoints:
  embeddings:
    method: POST
    path: /embed
    request:
      body: |
        {"inputs": {{ toJSON .inputs }} }
    response:
      embeddings_path: vectors
`, srv.URL)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "embed.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	providers, err := LoadFromDir(dir)
	if err != nil {
		t.Fatalf("load dir: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("expected one provider")
	}
	resp, err := providers[0].Embeddings(context.Background(), dto.EmbeddingsReq{Inputs: []string{"a"}})
	if err != nil {
		t.Fatalf("embeddings: %v", err)
	}
	if len(resp.Vectors) != 2 {
		t.Fatalf("unexpected vector count %d", len(resp.Vectors))
	}
}
