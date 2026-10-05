# CLI (`primus run`)

The headless entrypoint for CI: same engine as the web UI, machine-readable
output, exit codes a pipeline can gate on. Runs persist to the same SQLite
DB, so CLI runs show up in the web UI's history.

## Invocation

```sh
primus run <config.json> [--db <path>]
```

- `<config.json>` — a `TestConfig` JSON file (see [load](load.md) for the
  shape). The referenced `request_id` must already be in the DB: import
  the spec through the web UI first, or share the UI's DB file.
- `--db` — SQLite file, default `primus.db` (same default the server uses).
- Global auth comes from the same DB — set it in the UI once, or leave it
  unset for anonymous targets.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Run passed all assertions. |
| `1` | Run completed but an assertion failed (or every request failed). |
| `2` | Usage/config error: missing file, invalid JSON, failed validation, unknown `request_id`. |

## JSON output (stdout)

```json
{
  "id": "run-1790496283938613600",
  "test_config_id": "cfg-petstore-demo",
  "started_at": "2026-09-27T10:00:00Z",
  "finished_at": "2026-09-27T10:00:03Z",
  "summary": {
    "status_counts": { "200": 20 },
    "error_count": 0,
    "p50_millis": 120,
    "p95_millis": 480,
    "p99_millis": 900,
    "error_rate": 0.0,
    "total_requests": 20
  },
  "passed": true
}
```

`stderr` carries human-readable errors; `stdout` is always the JSON above
(on a completed run), so a pipeline step can parse or archive it.

## CI example

```yaml
- name: API load test
  run: |
    go run ./cmd/primus run ./examples/petstore-load.json --db primus.db
```

Exit `0` keeps the pipeline green; `1` fails the build on a real test
failure; `2` fails it on a misconfigured step — distinguishable at a
glance in the job log.

## Local Ollama service

From the `deploy/` directory, start Primus with the optional Ollama service:

```sh
docker compose --profile ai up --build -d
docker compose exec ollama ollama pull qwen2.5:3b
```

For NVIDIA GPU access, install and configure the [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html), then start with the GPU override:

```sh
docker compose -f docker-compose.yml -f docker-compose.gpu.yml --profile ai up --build -d
```

The Ollama service keeps downloaded models in the `ollama-models` volume. Primus uses rules-based insights by default. With `PRIMUS_ANALYZER=ollama` and the `ai` profile active, it also enables Ollama-backed insights and natural-language request authoring in the endpoint builder. Generated request details only pre-fill the form; they are not saved or tested until you explicitly submit the form.
