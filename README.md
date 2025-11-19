# AI Provider Aggregator

Backend skeleton in Go following Clean Architecture conventions to fan-out requests across multiple AI providers (OpenAI, Google Gemini, Claude, Grok).

## Features
- Config loading via environment variables (`HTTP_PORT`, `LOG_LEVEL`, `SHUTDOWN_TIMEOUT`).
- Structured logging with Go's `slog` API.
- Basic HTTP server under `cmd/server` exposing `GET /healthz`.
- Clean layering: `internal/core` (config/logging), `internal/services` (domain logic), `internal/providers` (provider ports/adapters), `pkg/http` (delivery layer).
- Real adapters for OpenAI and Google Gemini (text, multimodal, image, video, ASR, embeddings) plus mock providers for local testing.

## Quickstart

### With `.env` (recommended)
1. Copy `.env.example` to `.env` and fill in the required variables, for example:
	```dotenv
	OPENAI_API_KEY=sk-live...
	HTTP_PORT=8080
	GEMINI_API_KEY=your-google-ai-studio-key
	GEMINI_TEXT_MODEL=gemini-2.5-flash
	GEMINI_VISION_MODEL=gemini-2.5-pro
	GEMINI_IMAGE_MODEL=imagen-3.0-generate
	GEMINI_TIMEOUT=30
	```
2. Load the file and start the server via PowerShell:
	```pwsh
	pwsh scripts/run_with_env.ps1
	```
	You can override the command if needed:
	```pwsh
	pwsh scripts/run_with_env.ps1 -Command @("go", "test", "./...")
	```
	When calling any `/v1/*` endpoint you may include an optional `provider` field in the JSON body (e.g. `{ "prompt": "hi", "provider": "openai" }`) to force that adapter; omit it to fall back to the configured routing strategy.

### Makefile targets (if `make` is available)
```bash
make build   # builds bin/ai-aggregator
make run     # runs cmd/server with go run
make test    # executes go test ./...
make lint    # runs go vet ./...
```

The server listens on `HTTP_PORT` (defaults to `8080`). Visit `http://localhost:8080/healthz` to check aggregated provider health. When both `OPENAI_API_KEY` and `GEMINI_API_KEY` are provided, `/v1/providers` lists each adapter with its capability matrix so you can drive routing strategies via `?strategy=`.

## Generic HTTP providers

You can plug in additional upstreams without touching Go code by dropping JSON/YAML files under `internal/providers/config` (override the directory with `GENERIC_PROVIDER_CONFIG_DIR`). Each file describes:

- `name`, `base_url`, optional global `headers` and `auth` (bearer/api key/basic). Values expand `${ENV_VAR}` placeholders so secrets stay in your environment.
- `endpoints.text|image|video|stt|tts|embeddings|moderation` blocks specifying HTTP method/path, optional static query params/headers, a request body template, and response JSON selectors.

Request bodies are rendered with Go's [`text/template`](https://pkg.go.dev/text/template) against the DTO JSON (snake_case keys). Helpers available inside templates:

- `toJSON value` &rarr; serializes any value so you can embed nested structures.
- `env "VAR"` &rarr; injects environment variables at render time.

Responses are parsed as JSON and projected using dot/index paths (for example, `choices.0.message.content`). Refer to `internal/providers/config/sample-openrouter.yaml` for a working example that wires OpenRouter in ~30 lines of config.

## Running with Docker Compose

Two compose files let you run dependencies on one host and the API on another:

### 1. Dependencies host (Postgres + Redis)

Requires a Linux-capable Docker engine. On the machine that will expose the databases:

```bash
docker compose -f docker-compose.deps.yml up -d
```

This starts:

- `postgres` (user/password/db: `aione`) on port `5432` with persistent volume `postgres_data`.
- `redis` on port `6379` with append-only persistence under `redis_data`.

Forward ports on your network/firewall so the API box can reach them.

### 2. API host (this repository)

On the machine that will run the Go API container:

1. Copy `.env` with all provider secrets.
2. Point the API at the dependency host by setting either full connection strings or IP-based vars, e.g.:

```dotenv
DATABASE_URL=postgres://aione:aione@192.168.1.50:5432/aione?sslmode=disable
REDIS_ADDR=192.168.1.50:6379
```

3. Build and run the API:

```pwsh
docker compose up --build
```

`docker-compose.yml` now only builds the `api` service. If `DATABASE_URL` / `REDIS_ADDR` are omitted it defaults to `host.docker.internal`, which is convenient when you develop locally on the same workstation that hosts Postgres/Redis.

### Building the API image directly (Windows containers)

If your Docker Engine runs in Windows-container mode (no WSL2), use `Dockerfile.windows` instead of Compose:

```pwsh
docker build -f Dockerfile.windows -t aione-api-win .
docker run --env-file .env `
	-e DATABASE_URL=postgres://aione:aione@192.168.1.232:5432/aione?sslmode=disable `
	-e REDIS_ADDR=192.168.1.232:6379 `
	-p 8080:8080 aione-api-win
```

Add any other env vars (`GEMINI_API_KEY`, `OPENAI_API_KEY`, etc.) via `--env-file` or individual `-e` flags.
