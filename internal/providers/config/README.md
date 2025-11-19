# Generic HTTP providers

Drop JSON or YAML files in this directory to register additional HTTP providers without writing Go code. Each file must define at least the provider `name`, `base_url`, and one endpoint under `endpoints`.

See `sample-openrouter.yaml` for a minimal example. Common fields:

- `headers`: static headers applied to every request (values support `${ENV_VAR}` expansion).
- `auth`: describe bearer/api key/basic auth; `value_from_env` can pull secrets from env vars.
- `endpoints.<modality>`: optional blocks for `text`, `image`, `video`, `stt`, `tts`, `embeddings`, and `moderation`. Each block accepts:
  - `method`, `path`, optional `query`/`headers`.
  - `request.body`: Go `text/template` rendered with the DTO serialized as a map (snake case keys).
  - `response.*_path`: dot-indexed selectors (e.g., `choices.0.message.content`) to pluck values from the upstream JSON.

Restart the server (or rerun tests) after adding configs so they are picked up at boot.
