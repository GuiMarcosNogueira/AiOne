# AI Provider Aggregator

Backend skeleton in Go following Clean Architecture conventions to fan-out requests across multiple AI providers (OpenAI, Google Gemini, Claude, Grok).

## Features
- Config loading via environment variables (`HTTP_PORT`, `LOG_LEVEL`, `SHUTDOWN_TIMEOUT`).
- Structured logging with Go's `slog` API.
- Basic HTTP server under `cmd/server` exposing `GET /healthz`.
- Clean layering: `internal/core` (config/logging), `internal/services` (domain logic), `internal/providers` (provider ports/adapters), `pkg/http` (delivery layer).
- Real adapters for OpenAI and Google Gemini (text, multimodal, image, video, ASR, embeddings) plus mock providers for local testing.
- Unified image editing/inpainting support (JSON or multipart uploads for base images and masks).
- Optional Argon2id + JWT authentication stack (register/login/refresh/logout) backed by Postgres, Redis sessions and a login rate limiter.
- Encrypted per-user provider sessions that store upstream API keys securely with AES-256-GCM.
- Authenticated chat history per user/provider so text prompts can be replayed and truncated to model limits automatically.

## Quickstart

### With `.env` (recommended)
1. Copy `.env.example` to `.env` and fill in the required variables, for example:
	```dotenv
	OPENAI_API_KEY=sk-live...
	HTTP_PORT=8089
	GEMINI_API_KEY=your-google-ai-studio-key
	GEMINI_TEXT_MODEL=gemini-2.5-flash
	GEMINI_VISION_MODEL=gemini-2.5-pro
	GEMINI_IMAGE_MODEL=gemini-2.5-flash-image
	OPENAI_VIDEO_MODEL=sora-2
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
make tools   # installs swag + protobuf plugins once
make build   # builds bin/ai-aggregator
make run     # runs cmd/server with go run
make test    # executes go test ./...
make lint    # runs go vet ./...
make swagger # regenerates api/docs via swag init
make proto   # regenerates gRPC stubs (protoc required)

### gRPC server runtime

- The gRPC server boots automatically when `GRPC_ENABLED=true` (default) and listens on `GRPC_PORT` (`9090`).
- Disable it by setting `GRPC_ENABLED=false` or change the port via `.env`/environment variables.
- Use any gRPC client (for example `grpcurl -plaintext localhost:9090 list aione.v1.PublicService`) to inspect the services. Auth-required RPCs expect an `Authorization: Bearer <access_token>` header, matching the HTTP APIs.
- When you run `docker compose up`, both `HTTP_PORT` and `GRPC_PORT` are published from the same `.env`, so customize those values before starting the stack if you need different host ports.
```

### API documentation

- Swagger UI now ships directly with the binary at [`/docs/`](http://localhost:8089/docs/). The handler proxies every request through `github.com/swaggo/http-swagger`, so `/docs/doc.json` always reflects the generated spec.
- Specs are generated from inline annotations via [swaggo/swag](https://github.com/swaggo/swag). Run `make tools` once to install the pinned CLI (`swag@v1.16.3`) together with the protobuf plugins under your `GOBIN`/`GOPATH/bin`.
- Run `make swagger` (or `go generate ./cmd/server`) whenever you change handlers/comments. The command refreshes `api/docs/docs.go`, `swagger.json`, and `swagger.yaml`, which are all checked into git so CI/CD and Docker builds work without extra tooling.
- Consumers that still prefer a raw spec can download `api/docs/swagger.yaml` directly from the repo. At runtime, `/docs/doc.json` exposes the same data for clients that need a machine-readable contract.

### Protobuf / gRPC code generation

- Execute `make tools` once to install `protoc-gen-go` (`v1.34.2`) and `protoc-gen-go-grpc` (`v1.5.1`) in your Go bin directory so `protoc` can find them.
- Run `make proto` to regenerate the stubs under `api/grpc`. The target expects the `protoc` binary at `.tools/protoc/bin/protoc` by default—override the path via `make PROTOC_BIN=/custom/protoc proto` if you keep it elsewhere.
- If you still need the compiler itself, download the official archive for your platform from the [protobuf releases](https://github.com/protocolbuffers/protobuf/releases), extract it, and point `PROTOC_BIN` at the resulting `bin/protoc`.

### One `.env` for every run mode

Set host-friendly values (`localhost`, relative storage paths, API port) in the canonical variables and pair them with `_DOCKER` overrides for container runs. `scripts/run_with_env.sh`, `go run`, or IDE debuggers use `DATABASE_URL`, `REDIS_ADDR`, `UPLOAD_DIR`, etc., while `docker compose` automatically injects their `_DOCKER` counterparts into each container:

```dotenv
DATABASE_URL=postgres://aione:aione@localhost:5432/aione?sslmode=disable
DATABASE_URL_DOCKER=postgres://aione:aione@postgres:5432/aione?sslmode=disable
REDIS_ADDR=localhost:6379
REDIS_ADDR_DOCKER=redis:6379
AUTH_SESSION_REDIS_ADDR=localhost:6379
AUTH_SESSION_REDIS_ADDR_DOCKER=redis:6379
AUTH_RATELIMIT_REDIS_ADDR=localhost:6379
AUTH_RATELIMIT_REDIS_ADDR_DOCKER=redis:6379
UPLOAD_DIR=storage
UPLOAD_DIR_DOCKER=/app/storage
STORAGE_PUBLIC_BASE_URL=http://localhost:8089/media
STORAGE_PUBLIC_BASE_URL_DOCKER=http://localhost:8090/media
STORAGE_SERVE_FROM_API=true
STORAGE_SERVE_FROM_API_DOCKER=false
```

When you migrate to an external object storage bucket, point both `STORAGE_PUBLIC_BASE_URL` variables at the bucket origin and set both `STORAGE_SERVE_FROM_API` flags to `false`. The API will keep writing files to the configured `UPLOAD_DIR` (mount it via Fuse/CSI or sync it to the bucket) while clients resolve media URLs through the CDN/object-storage endpoint regardless of how the process is started.

#### Provider configuration

OpenAI and Gemini adapters now rely on the official SDKs, so you only need to supply API keys, preferred model names, and optional timeout limits via the `OPENAI_*` and `GEMINI_*` variables. Endpoint overrides (`*_BASE_URL`, `*_PATH`) have been removed—run traffic through your own gateway or proxy if you need custom routing.

##### Gemini image-edit workflow

Gemini edits now follow the AI Studio workaround internally:

1. **Vision rewrite.** The original image (plus optional mask) is sent to `GEMINI_VISION_MODEL` (default `gemini-2.5-pro`) together with the user's instruction. The response is a detailed prompt tailored for downstream image generators while preserving the scene.
2. **Preview render.** That synthesized prompt is immediately forwarded to `gemini-3-pro-image-preview`, which returns the edited artifact surfaced back to the client.

Ensure your Google AI Studio key has access to both models. You can still override `GEMINI_VISION_MODEL` in `.env` to pick a different rewrite model; the preview step remains pinned to `gemini-3-pro-image-preview` until Google re-enables direct edit APIs.

The server listens on `HTTP_PORT` (defaults to `8089`). Visit `http://localhost:8089/healthz` to check aggregated provider health. When both `OPENAI_API_KEY` and `GEMINI_API_KEY` are provided, `/v1/providers` lists each adapter with its capability matrix so you can drive routing strategies via `?strategy=`.

### Live provider verification

To exercise the real OpenAI and Gemini adapters end-to-end, export valid API keys and run the opt-in test suite behind the `live` build tag:

```bash
export OPENAI_API_KEY=sk-...
export GEMINI_API_KEY=AIza...
go test -tags live ./tests
```

Each test skips automatically if the corresponding environment variable is absent, so you can target a single provider via `-run TestOpenAI` or `-run TestGemini` when needed. These tests perform real network calls, so expect them to take a few seconds and to incur upstream billing.

## Authentication (optional)

The `/auth/*` endpoints stay disabled until the required env vars are present. To enable user management:

1. Provision Postgres and Redis (the compose file `docker-compose.yml` spins up both by default).
2. Apply the SQL migrations (requires `psql` in your shell). With `DATABASE_URL` exported you can run:
	```bash
	make migrate
	```
	This sequentially executes each `migrations/*.sql` file against the configured database.
	If you're using Docker Compose, there's also a `migrate` service that runs the same target:
	```bash
	docker compose run --rm migrate
	# or
	make docker-migrate
	```
3. Add the following secrets to `.env` (override TTLs/costs as needed):
	```dotenv
	AUTH_ACCESS_SECRET=change-me
	AUTH_REFRESH_SECRET=change-me-too
	AUTH_ACCESS_TTL=900            # seconds
	AUTH_REFRESH_TTL=604800        # seconds
	AUTH_SESSION_PREFIX=auth:session
	AUTH_SESSION_REDIS_ADDR=localhost:6379
	AUTH_SESSION_REDIS_DB=1
	AUTH_SESSION_REDIS_PASSWORD=
	AUTH_RATELIMIT_WINDOW=60       # seconds
	AUTH_RATELIMIT_MAX_ATTEMPTS=5
	AUTH_RATELIMIT_REDIS_ADDR=localhost:6379
	AUTH_RATELIMIT_REDIS_DB=2
	AUTH_RATELIMIT_REDIS_PASSWORD=
	AUTH_ARGON_MEMORY=65536
	AUTH_ARGON_ITERATIONS=3
	AUTH_ARGON_PARALLELISM=2
	AUTH_ARGON_SALT_LENGTH=16
	AUTH_ARGON_KEY_LENGTH=32
	```

With those values set the server exposes:

- `POST /auth/register` – create an account and get an access/refresh token pair.
- `POST /auth/login` – exchange credentials for a new token pair (subject to Redis-backed rate limiting).
- `POST /auth/refresh` – rotate refresh tokens + sessions (invalidates the previous one).
- `POST /auth/logout` – revoke the provided refresh token/session.

Access tokens are standard JWTs signed with `AUTH_ACCESS_SECRET`. Refresh tokens are JWTs bound to a Redis session entry, so logout/refresh rotation invalidates stolen tokens immediately.

## Conversation sessions (optional)

Users can keep multiple named conversations per provider (similar to ChatGPT's left pane). Sessions record metadata, rolling usage counters, and expiration hints inside the `chat_sessions` table (see `migrations/004_chat_sessions.sql`). Clients may still supply a `provider_key` per request to override the global key, but no per-user secret storage is required anymore.

1. Apply the migration after the auth tables:
	```bash
	psql "$DATABASE_URL" -f migrations/004_chat_sessions.sql
	```
2. Once the schema is in place the authenticated routes become available under `/providers/{provider}`:
	- `POST /providers/{provider}/sessions` &rarr; creates a new chat session (body accepts `title`, optional `metadata`, `expires_at`).
	- `GET /providers/{provider}/sessions` &rarr; lists recent sessions; supports `?limit=` and `?include_archived=true`.
	- `GET /providers/{provider}/sessions/{session_id}` &rarr; fetches metadata for a specific session owned by the caller.
	- `DELETE /providers/{provider}/sessions/{session_id}` &rarr; archives the session (history stays until you delete it).

All endpoints require a valid access token and run behind the existing auth middleware, so each user can only touch their own sessions.

## Chat history (optional)

When authentication is enabled the API can remember recent chat turns per user/provider. The stored context is injected into `POST /v1/chat` requests and truncated automatically so the provider's `max_text_tokens` budget is not exceeded.

1. Apply `migrations/003_user_context_history.sql` after the provider-session tables:
	```bash
	psql "$DATABASE_URL" -f migrations/003_user_context_history.sql
	```
2. Ensure uploads have a writable location (defaults to the existing `UPLOAD_DIR` used by the storage service). Media references saved through `SaveMedia` are stored on disk and only the path is kept in the database.
3. Once the migration is applied and auth is on, the following endpoints become available behind the auth middleware:
	- `GET /history/{session_id}` – returns the ordered `UserContextHistory` entries for that session.
	- `DELETE /history/{session_id}` – removes every entry for that session/user pair.

Every call to `POST /v1/chat` now:

- Loads the persisted context (if any) for the preferred/provider override before contacting the upstream provider.
- Saves the user prompt plus the provider reply back into the history table.
- Truncates the stored history when its estimated token sum would exceed the provider's published `max_text_tokens` (or the request `max_tokens`, whichever is lower).

## Session-scoped messaging endpoints

When both provider sessions and chat history are enabled you can send provider-specific prompts via the `/session/{provider}/*` routes. Each call automatically loads/truncates the user's history, injects it into the outgoing request, and refreshes the persisted session counters.

- `POST /session/{provider}/message` &rarr; send a text prompt and receive the assistant reply.
- `POST /session/{provider}/image` &rarr; request image generation tied to the user's provider key.
- `POST /session/{provider}/image/edit` &rarr; upload an existing image (and optional mask) for editing/inpainting while tracking usage.
- `POST /session/{provider}/video` &rarr; trigger video generation.
- `POST /session/{provider}/audio` &rarr; transcribe audio while tracking session usage.

All endpoints expect the same auth token used elsewhere plus a JSON payload. Each payload may include `session_id` to reuse an existing conversation or `session_title` to name a new one. `provider_key` remains optional and only overrides the globally configured key for that specific request.

```json
POST /session/openai/message
{
	"prompt": "Summarize the following:",
	"max_tokens": 256,
	"temperature": 0.4,
	"provider_key": "sk-user-override",
	"session_metadata": {"team": "alpha"}
}
```

Responses always include the upstream payload plus the refreshed session object so the client can keep track of `total_tokens_used`, `last_interaction`, and any metadata updates.

## Image editing API

`POST /v1/image/edit` exposes provider-agnostic image editing and inpainting. Requests accept either `application/json` or `multipart/form-data`:

- JSON payloads mirror the DTO (`prompt`, `image_url` or `image_base64`, optional `mask_url` / `mask_base64`, `size`, `media`, `provider`).
- Multipart payloads use text fields for the same metadata plus file fields: `image_file` (required when `image_url`/`image_base64` are missing) and `mask_file` (optional). Uploaded files are converted to data URLs internally and capped at ~25&nbsp;MiB.

When the request resolves to the Gemini provider, the backend runs the two-stage process described earlier: the base image and optional mask are ingested by the vision model to craft a high-fidelity edit prompt, then that rewritten text—annotated with the requested `size`/aspect ratio—is rendered by `gemini-3-pro-image-preview`. Other providers (OpenAI, mock adapters, generic HTTP) continue to use their native edit APIs directly.

Session routes reuse the same semantics via `POST /session/{provider}/image/edit`. Include the usual `session_id`, `session_title`, `session_metadata`, `provider_key`, and `expires_at` fields alongside the edit payload. Multipart requests simply add those values as extra form fields (JSON blobs for `session_metadata`/`media`).

## Media storage

Generated/edited images are persisted under `UPLOAD_DIR` (defaults to `storage/`). Set `STORAGE_PUBLIC_BASE_URL` to the HTTP origin clients should use—`http://localhost:8090/media` is wired up automatically when you run the Compose stack, which also spins up a lightweight Nginx container (`storage`) that serves the shared `storage_data` volume over port `8090`. The API can still expose `GET /media/*` directly for bare-metal deployments; toggle this via `STORAGE_SERVE_FROM_API=true` (the default outside Compose). Leave it `false` when you rely on the dedicated storage service or a CDN.

When using `docker compose up`, the `storage_data` volume keeps `/app/storage` alive across container restarts and is mounted read-only by the `storage` service. Mount a host directory instead if you need to inspect artifacts directly, and remember to point `STORAGE_PUBLIC_BASE_URL` at whichever CDN/object-store origin you deploy later so clients always receive full URLs.

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

`docker-compose.yml` now builds the `api` service plus the static `storage` file server. The stack reads `DATABASE_URL_DOCKER`, `REDIS_ADDR_DOCKER`, and the storage-specific `_DOCKER` variables, falling back to the internal service hostnames when unspecified. This lets you keep workstation values (e.g., `localhost`) for `go run` while containers automatically talk to `postgres`, `redis`, and `/app/storage`. The storage container listens on `localhost:8090` so the asset URLs returned by the API resolve immediately unless you point `STORAGE_PUBLIC_BASE_URL_DOCKER` at an external bucket/CDN.

### Building the API image directly (Windows containers)

If your Docker Engine runs in Windows-container mode (no WSL2), use `Dockerfile.windows` instead of Compose:

```pwsh
docker build -f Dockerfile.windows -t aione-api-win .
docker run --env-file .env `
	-e DATABASE_URL=postgres://aione:aione@192.168.1.232:5432/aione?sslmode=disable `
	-e REDIS_ADDR=192.168.1.232:6379 `
	-p 8089:8089 aione-api-win
```

Add any other env vars (`GEMINI_API_KEY`, `OPENAI_API_KEY`, etc.) via `--env-file` or individual `-e` flags.
