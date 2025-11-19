# AI Provider Aggregator

Backend skeleton in Go following Clean Architecture conventions to fan-out requests across multiple AI providers (OpenAI, Google Gemini, Claude, Grok).

## Features
- Config loading via environment variables (`HTTP_PORT`, `LOG_LEVEL`, `SHUTDOWN_TIMEOUT`).
- Structured logging with Go's `slog` API.
- Basic HTTP server under `cmd/server` exposing `GET /healthz`.
- Clean layering: `internal/core` (config/logging), `internal/services` (domain logic), `internal/providers` (provider ports/adapters), `pkg/http` (delivery layer).
- Real adapters for OpenAI and Google Gemini (text, multimodal, image, video, ASR, embeddings) plus mock providers for local testing.
- Optional Argon2id + JWT authentication stack (register/login/refresh/logout) backed by Postgres, Redis sessions and a login rate limiter.
- Encrypted per-user provider sessions that store upstream API keys securely with AES-256-GCM.
- Authenticated chat history per user/provider so text prompts can be replayed and truncated to model limits automatically.

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

## Authentication (optional)

The `/auth/*` endpoints stay disabled until the required env vars are present. To enable user management:

1. Provision Postgres and Redis (the compose file `docker-compose.deps.yml` already spins up both).
2. Apply the migration under `migrations/001_users.sql` to your Postgres database:
	```bash
	psql "$DATABASE_URL" -f migrations/001_users.sql
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

## Provider-specific sessions (optional)

Users can store their own API keys for each upstream provider without exposing them to other accounts. The API keeps those secrets encrypted at rest via AES-256-GCM.

1. Apply migration `migrations/002_user_provider_sessions.sql` after the auth tables:
	```bash
	psql "$DATABASE_URL" -f migrations/002_user_provider_sessions.sql
	```
2. Generate at least one 32-byte key (base64-encoded) to build a key ring, then point the service at it via:
	```dotenv
	PROVIDER_SESSION_PRIMARY_KEY_ID=2025-01-rot
	PROVIDER_SESSION_KEYS=2025-01-rot:5kP2gAa2wQ3jG3Y0v2wR8byS0b7fQeLNE5Hk2N1QWz4=
	```
	To rotate keys, append more comma-separated `id:key` pairs to `PROVIDER_SESSION_KEYS` and move `PROVIDER_SESSION_PRIMARY_KEY_ID` to the new entry once every instance is updated.
3. With those vars set, the authenticated routes become available under `/providers/{provider}`:
	- `POST /providers/{provider}/set-key` &rarr; stores/updates the user's key (body accepts `provider_key`, optional `metadata`, `expires_at`).
	- `GET /providers/{provider}/session` &rarr; retrieves the decrypted key + usage metadata for that provider.
	- `DELETE /providers/{provider}/session/reset` &rarr; wipes the stored key and usage counters.

All endpoints require a valid access token and run behind the existing auth middleware, so each user can only touch their own sessions.

## Chat history (optional)

When authentication is enabled the API can remember recent chat turns per user/provider. The stored context is injected into `POST /v1/chat` requests and truncated automatically so the provider's `max_text_tokens` budget is not exceeded.

1. Apply `migrations/003_user_context_history.sql` after the provider-session tables:
	```bash
	psql "$DATABASE_URL" -f migrations/003_user_context_history.sql
	```
2. Ensure uploads have a writable location (defaults to the existing `UPLOAD_DIR` used by the storage service). Media references saved through `SaveMedia` are stored on disk and only the path is kept in the database.
3. Once the migration is applied and auth is on, the following endpoints become available behind the auth middleware:
	- `GET /history/{provider}` – returns the ordered `UserContextHistory` entries for that provider.
	- `DELETE /history/{provider}/clear` – removes every entry for that provider/user pair.

Every call to `POST /v1/chat` now:

- Loads the persisted context (if any) for the preferred/provider override before contacting the upstream provider.
- Saves the user prompt plus the provider reply back into the history table.
- Truncates the stored history when its estimated token sum would exceed the provider's published `max_text_tokens` (or the request `max_tokens`, whichever is lower).

## Session-scoped messaging endpoints

When both provider sessions and chat history are enabled you can send provider-specific prompts via the `/session/{provider}/*` routes. Each call automatically loads/truncates the user's history, injects it into the outgoing request, and refreshes the persisted session counters.

- `POST /session/{provider}/message` &rarr; send a text prompt and receive the assistant reply.
- `POST /session/{provider}/image` &rarr; request image generation tied to the user's provider key.
- `POST /session/{provider}/video` &rarr; trigger video generation.
- `POST /session/{provider}/audio` &rarr; transcribe audio while tracking session usage.

All endpoints expect the same auth token used elsewhere plus a JSON payload. `provider_key` is optional and only required the first time a user calls a provider if they have not stored a key yet.

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
